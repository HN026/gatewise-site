package adaptive

import (
	"github.com/HN026/gatewise/internal/admission"
	"sync/atomic"
	"time"
)

var instanceSequence atomic.Uint64

type tierFeedback struct {
	histogram         [8]int
	samples, failures int
}
type stableState struct {
	tiers             map[admission.Priority]*tierFeedback
	ratio             float64
	pressure, healthy int
	seed              uint64
}

func newStableState() stableState {
	seed := uint64(time.Now().UnixNano()) ^ instanceSequence.Add(1)*0x9e3779b97f4a7c15
	if seed == 0 {
		seed = 1
	}
	return stableState{seed: seed}
}
func scaledInterval(d time.Duration, n int64) time.Duration {
	const max = time.Duration(1<<63 - 1)
	if d > max/time.Duration(n) {
		return max
	}
	return d * time.Duration(n)
}

// A bounded local timing offset reduces exact phase alignment. It is not
// cross-instance coordination and does not guarantee desynchronization.
func (s *stableState) interval(base time.Duration) time.Duration {
	s.seed ^= s.seed << 13
	s.seed ^= s.seed >> 7
	s.seed ^= s.seed << 17
	delta := base / 100 * time.Duration(int64(s.seed%21)-10)
	if delta > 0 && base > time.Duration(1<<63-1)-delta {
		delta = 0
	}
	return base + delta
}
func (s *stableState) idle() { s.ratio = 0; s.pressure = 0; s.healthy = 0 }

// decide uses normalized window feedback, retaining all operator hard bounds.
// Sparse windows are observable holds. Signal-limited windows cannot justify
// growth. The returned adjustment reason remains compatible with old counters.
func (c *Controller) stableDecision(current int, p90, fraction float64, sparse bool, aggregate float64) (int, string) {
	s := &c.stable
	if sparse {
		s.healthy = 0
		s.pressure = 0
		return current, "sparse_feedback"
	}
	previousRatio := s.ratio
	if s.ratio == 0 {
		s.ratio = p90
	} else {
		s.ratio = .5*s.ratio + .5*p90
	}
	if p90 > 1 || fraction >= .2 {
		s.pressure++
		s.healthy = 0
		if current == c.config.Min {
			return current, "min_bound"
		}
		// Confirm a small moderate tier-only cut. Larger cuts, aggregate pressure,
		// severe tails and errors react immediately while capacity is still high.
		if aggregate <= 1 && p90 < 2 && fraction < .2 && current/5 <= 1 && s.pressure < 2 {
			return current, "tier_confirmation"
		}
		step := current / 5 // gentler response to moderate latency pressure
		if p90 >= 2 || s.ratio >= 2 {
			step = current / 2
		}
		reason := "latency_pressure"
		if fraction >= .2 {
			step = current / 3
			reason = "server_errors"
		}
		if step < 1 {
			step = 1
		}
		next := current - step
		if next < c.config.Min {
			next = c.config.Min
		}
		if next == current {
			return current, "min_bound"
		}
		return next, reason
	}
	s.pressure = 0
	if c.externallyLimited {
		s.healthy = 0
		return current, "external_limit"
	}
	if c.failures > 0 {
		s.healthy = 0
		return current, "mixed_errors"
	}
	if p90 > .5 {
		s.healthy = 0
		return current, "latency_headroom"
	}
	if previousRatio > 0 && p90 > 1.5*previousRatio {
		s.healthy = 0
		return current, "latency_trend"
	}
	s.healthy++
	if !c.saturated {
		return current, "no_saturation"
	}
	if current >= c.config.Max {
		return current, "max_bound"
	}
	strong := p90 <= .25 && c.samples/2 >= c.config.MinSamples
	if (s.healthy < 2 && !strong) || s.ratio > .8 {
		return current, "healthy_confirmation"
	}
	step := current / 4
	if current%4 > 0 {
		step++
	} // ceil(current/4), without multiplying the bound
	next := current
	if step > c.config.Max-current {
		next = c.config.Max
	} else {
		next = current + step
	}
	return next, "healthy_saturation"
}

// Well-sampled tiers protect their tails even when aggregate traffic hides them.
// The map is initialized from configured tiers, never from request metadata.
func (c *Controller) controlFeedback(p90, failure float64) (float64, float64, string) {
	selected := "aggregate"
	for priority, t := range c.stable.tiers {
		if t.samples < 10 {
			continue
		}
		rank := (t.samples*95 + 99) / 100
		sum := 0
		ratio := 5.0
		bounds := [7]float64{.25, .5, .8, 1, 1.5, 2, 4}
		for i, count := range t.histogram {
			sum += count
			if sum >= rank {
				if i < 7 {
					ratio = bounds[i]
				}
				break
			}
		}
		if ratio > p90 || ratio == p90 && selected != "aggregate" && string(priority) < selected {
			p90 = ratio
			selected = string(priority)
		}
		if f := float64(t.failures) / float64(t.samples); f > failure {
			failure = f
		}
	}
	return p90, failure, selected
}
