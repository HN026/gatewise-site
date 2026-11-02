// Package adaptive implements bounded, request-driven feedback control.
package adaptive

import (
	"github.com/HN026/gatewise/internal/admission"
	"sync"
	"time"
)

type Config struct {
	Profile              string
	Min, Max, MinSamples int
	Interval             time.Duration
}
type Adjustment struct {
	Profile, ControlPriority             string
	ControlRatio, ControlFailureFraction float64
	Previous, Limit, Samples             int
	Reason                               string
	P90Ratio                             float64
}

// Evaluation describes the last completed feedback window, including hold decisions.
type Evaluation struct {
	ControlRatio, ControlFailureFraction          float64
	ControlPriority                               string
	Profile                                       string
	SmoothedControlRatio                          float64
	SparseWindow, ExternalLimit                   bool
	PressureStreak, HealthyStreak, PendingSamples int
	WindowStartedAt                               time.Time
	WindowInterval                                time.Duration
	EvaluatedAt                                   time.Time
	Samples                                       int
	P90Ratio, FailureFraction                     float64
	PreviousLimit, Limit                          int
	Saturated                                     bool
	Decision, Reason                              string
}

type Controller struct {
	stable            stableState
	interval          time.Duration
	externallyLimited bool
	mu                sync.Mutex
	limiter           *admission.Limiter
	config            Config
	generation        uint64
	start, lastSample time.Time
	histogram         [8]int
	samples, failures int
	saturated         bool
	last              Evaluation
}

func New(limiter *admission.Limiter, c Config) *Controller {
	if c.Profile == "" {
		c.Profile = "legacy"
	}
	stable := newStableState()
	if c.Profile == "stable" {
		stable.tiers = make(map[admission.Priority]*tierFeedback)
		for name := range limiter.TierCounts() {
			stable.tiers[name] = &tierFeedback{}
		}
	}
	return &Controller{limiter: limiter, config: c, stable: stable, interval: c.Interval}
}

// Begin coordinates the admission decision and measurement generation. There is
// no capacity wait. Total/background saturation can justify cautious growth.
func (c *Controller) Begin(priority admission.Priority, now time.Time) (admission.Decision, uint64) {
	return c.BeginCohort(priority, now, -1, 0)
}
func (c *Controller) BeginCohort(priority admission.Priority, now time.Time, bucket, buckets int) (admission.Decision, uint64) {
	return c.BeginBudget(priority, now, bucket, buckets, 0)
}
func (c *Controller) BeginBudget(priority admission.Priority, now time.Time, bucket, buckets, ceiling int) (admission.Decision, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	decision := c.limiter.TryAcquireBudget(priority, bucket, buckets, ceiling)
	state := c.limiter.Snapshot()
	if c.start.IsZero() {
		c.reset(now)
	}
	idleInterval := 2 * c.config.Interval
	if c.config.Profile == "stable" {
		idleInterval = scaledInterval(c.config.Interval, 4)
		if idleInterval < 3*time.Second {
			idleInterval = 3 * time.Second
		}
	}
	if decision == admission.Admitted && state.Active == 1 && !c.lastSample.IsZero() && now.Sub(c.lastSample) > idleInterval {
		c.stable.idle()
		c.reset(now)
	}
	binding := ceiling > 0 && ceiling < state.Limit
	if c.config.Profile == "stable" && binding {
		c.externallyLimited = true
	} else if c.config.Profile == "stable" {
		// Occupying the last slot is not evidence of unmet demand. Only a local
		// capacity refusal can justify growth; signal/cohort vetoes cannot.
		if decision == admission.ConcurrencyLimit || decision == admission.CriticalCapacityReserved || decision == admission.TierCapacityReserved {
			c.saturated = true
		}
	} else {
		if decision != admission.Admitted && decision != admission.CohortLimited && decision != admission.SignalConcurrencyLimit || state.Active >= state.Limit || (priority == admission.Background && state.Background >= state.Limit-state.ReservedCritical) {
			c.saturated = true
		}
	}
	return decision, c.generation
}

// End always releases admitted work, including canceled/stale-generation work.
// Only current, noncanceled completions contribute feedback. Timings are divided
// by each handler's configured latency budget before aggregation.
func (c *Controller) End(priority admission.Priority, generation uint64, now time.Time, elapsed, target time.Duration, failed, canceled bool) *Adjustment {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limiter.Release(priority)
	if canceled || generation != c.generation {
		return nil
	}
	ratio := float64(elapsed) / float64(target)
	boundaries := [7]float64{.25, .5, .8, 1, 1.5, 2, 4}
	bucket := 7
	for i, boundary := range boundaries {
		if ratio <= boundary {
			bucket = i
			break
		}
	}
	c.histogram[bucket]++
	if c.config.Profile == "stable" {
		if t := c.stable.tiers[priority]; t != nil {
			t.histogram[bucket]++
			t.samples++
			if failed {
				t.failures++
			}
		}
	}
	c.samples++
	c.lastSample = now
	if failed {
		c.failures++
	}
	sparse := false
	if now.Sub(c.start) < c.interval || c.samples < c.config.MinSamples {
		if c.config.Profile != "stable" || now.Sub(c.start) < scaledInterval(c.config.Interval, 4) || c.samples < 3 || c.saturated {
			return nil
		}
		sparse = true
	}
	rank := (c.samples*9 + 9) / 10
	cumulative := 0
	p90 := 5.0
	for i, count := range c.histogram {
		cumulative += count
		if cumulative >= rank {
			if i < 7 {
				p90 = boundaries[i]
			}
			break
		}
	}
	current := c.limiter.Snapshot().Limit
	next := current
	reason := ""
	controlRatio, controlFailure, controlPriority := p90, float64(c.failures)/float64(c.samples), "aggregate"
	if c.config.Profile == "stable" {
		controlRatio, controlFailure, controlPriority = c.controlFeedback(p90, controlFailure)
		next, reason = c.stableDecision(current, controlRatio, controlFailure, sparse, p90)
	} else if p90 > 1 || c.failures*5 >= c.samples {
		step := current / 3
		if step < 1 {
			step = 1
		}
		next = current - step
		if next < c.config.Min {
			next = c.config.Min
		}
		reason = "latency_pressure"
		if c.failures*5 >= c.samples {
			reason = "server_errors"
		}
	} else if c.saturated && c.failures == 0 && p90 <= .8 {
		if next < c.config.Max {
			next++
		}
		reason = "healthy_saturation"
	}
	decision := "hold"
	if next < current {
		decision = "decrease"
	} else if next > current {
		decision = "increase"
	}
	evaluationReason := reason
	if evaluationReason == "" {
		evaluationReason = "no_change"
	}
	c.last = Evaluation{ControlRatio: controlRatio, ControlFailureFraction: controlFailure, ControlPriority: controlPriority, Profile: c.config.Profile, SmoothedControlRatio: c.stable.ratio, SparseWindow: sparse, ExternalLimit: c.externallyLimited, PressureStreak: c.stable.pressure, HealthyStreak: c.stable.healthy,
		EvaluatedAt: now, Samples: c.samples, P90Ratio: p90,
		FailureFraction: float64(c.failures) / float64(c.samples), PreviousLimit: current,
		Limit: next, Saturated: c.saturated, Decision: decision, Reason: evaluationReason}
	samples := c.samples
	c.reset(now)
	if next == current {
		return nil
	}
	c.limiter.SetLimit(next)
	c.generation++
	return &Adjustment{Profile: c.config.Profile, ControlPriority: controlPriority, ControlRatio: controlRatio, ControlFailureFraction: controlFailure, Previous: current, Limit: next, Samples: samples, Reason: reason, P90Ratio: p90}
}

func (c *Controller) reset(now time.Time) {
	c.start = now
	c.externallyLimited = false
	c.interval = c.config.Interval
	if c.config.Profile == "stable" {
		c.interval = c.stable.interval(c.config.Interval)
	}
	c.histogram = [8]int{}
	for _, t := range c.stable.tiers {
		*t = tierFeedback{}
	}
	c.samples = 0
	c.failures = 0
	c.saturated = false
}

// Snapshot does not evaluate or reset the controller. A zero timestamp means no
// eligible ordinary or sparse window has been evaluated yet.
func (c *Controller) Snapshot() Evaluation {
	c.mu.Lock()
	defer c.mu.Unlock()
	last := c.last
	last.Profile = c.config.Profile
	last.PendingSamples = c.samples
	last.WindowStartedAt = c.start
	last.WindowInterval = c.interval
	return last
}
