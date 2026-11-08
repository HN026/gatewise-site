package adaptive_test

import (
	"github.com/HN026/gatewise/internal/adaptive"
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/middleware"
	"testing"
	"time"
)

func stableWindow(t *testing.T, c *adaptive.Controller, l *admission.Limiter, now *time.Time, elapsed time.Duration) {
	t.Helper()
	tokens := []uint64{}
	for i := 0; i < l.Snapshot().Limit; i++ {
		d, g := c.Begin(admission.Critical, *now)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		tokens = append(tokens, g)
	}
	if d, _ := c.Begin(admission.Critical, *now); d != admission.ConcurrencyLimit {
		t.Fatal("missing real saturation", d)
	}
	*now = now.Add(2 * time.Second)
	for _, g := range tokens {
		c.End(admission.Critical, g, *now, elapsed, 100*time.Millisecond, false, false)
	}
	if l.Snapshot().Active != 0 {
		t.Fatal("old generation leaked")
	}
}
func TestStablePressureRecoveryAndBounds(t *testing.T) {
	l := admission.New(16, 1)
	c := adaptive.New(l, adaptive.Config{Profile: "stable", Min: 2, Max: 32, Interval: time.Second, MinSamples: 2})
	now := time.Unix(100, 0)
	stableWindow(t, c, l, &now, 300*time.Millisecond)
	if l.Snapshot().Limit != 8 || c.Snapshot().Decision != "decrease" {
		t.Fatal(l.Snapshot(), c.Snapshot())
	}
	stableWindow(t, c, l, &now, 10*time.Millisecond)
	if l.Snapshot().Limit != 8 {
		t.Fatal("grew from one healthy window")
	}
	for i := 0; i < 8; i++ {
		stableWindow(t, c, l, &now, 10*time.Millisecond)
	}
	if l.Snapshot().Limit != 32 {
		t.Fatal("recovery failed", l.Snapshot())
	}
	for i := 0; i < 8; i++ {
		stableWindow(t, c, l, &now, 500*time.Millisecond)
	}
	if l.Snapshot().Limit != 2 || c.Snapshot().Reason != "min_bound" {
		t.Fatal("operator floor not respected/explained", l.Snapshot(), c.Snapshot())
	}
	v := c.Snapshot()
	if v.Profile != "stable" || v.WindowInterval < 900*time.Millisecond || v.WindowInterval > 1100*time.Millisecond {
		t.Fatal(v)
	}
}
func TestStableSparseFeedbackHolds(t *testing.T) {
	l := admission.New(6, 1)
	c := adaptive.New(l, adaptive.Config{Profile: "stable", Min: 2, Max: 12, Interval: time.Second, MinSamples: 10})
	now := time.Unix(100, 0)
	for i := 0; i < 3; i++ {
		_, g := c.Begin(admission.Critical, now)
		now = now.Add(2 * time.Second)
		c.End(admission.Critical, g, now, 10*time.Millisecond, 100*time.Millisecond, false, false)
	}
	v := c.Snapshot()
	if !v.SparseWindow || v.Samples != 3 || v.Decision != "hold" || v.Reason != "sparse_feedback" || l.Snapshot().Limit != 6 {
		t.Fatal(v)
	}
	before := v.EvaluatedAt
	_, g := c.Begin(admission.Critical, now)
	c.End(admission.Critical, g, now.Add(time.Second), time.Second, time.Millisecond, true, true)
	if l.Snapshot().Active != 0 || c.Snapshot().EvaluatedAt != before {
		t.Fatal("cancellation changed feedback or leaked")
	}
}
func TestStableSignalCeilingCannotJustifyGrowth(t *testing.T) {
	l := admission.New(6, 2)
	c := adaptive.New(l, adaptive.Config{Profile: "stable", Min: 3, Max: 12, Interval: time.Second, MinSamples: 2})
	now := time.Unix(100, 0)
	if d, _ := c.BeginBudget(admission.Background, now, -1, 0, 2); d != admission.SignalConcurrencyLimit {
		t.Fatal(d)
	}
	tokens := []uint64{}
	for i := 0; i < 2; i++ {
		d, g := c.BeginBudget(admission.Critical, now, -1, 0, 2)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		tokens = append(tokens, g)
	}
	now = now.Add(2 * time.Second)
	for _, g := range tokens {
		c.End(admission.Critical, g, now, time.Millisecond, 100*time.Millisecond, false, false)
	}
	v := c.Snapshot()
	if l.Snapshot().Limit != 6 || !v.ExternalLimit || v.Decision != "hold" || v.Reason != "external_limit" {
		t.Fatal(l.Snapshot(), v)
	}
	if _, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 6, Adaptive: &middleware.AdaptiveConfig{Profile: "other"}}); err == nil {
		t.Fatal("invalid profile accepted")
	}
}

func TestStableMinorityCriticalTailIsNotHidden(t *testing.T) {
	l := admission.New(8, 2)
	c := adaptive.New(l, adaptive.Config{Profile: "stable", Min: 3, Max: 32, Interval: time.Second, MinSamples: 10})
	now := time.Unix(100, 0)
	for i := 0; i < 110; i++ {
		p, elapsed, target := admission.Background, time.Millisecond, 500*time.Millisecond
		if i >= 100 {
			p, elapsed, target = admission.Critical, 150*time.Millisecond, 100*time.Millisecond
		}
		d, g := c.Begin(p, now)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		c.End(p, g, now.Add(time.Duration(i)*time.Millisecond), elapsed, target, false, false)
	}
	d, g := c.Begin(admission.Critical, now.Add(2*time.Second))
	if d != admission.Admitted {
		t.Fatal(d)
	}
	c.End(admission.Critical, g, now.Add(2*time.Second), 150*time.Millisecond, 100*time.Millisecond, false, false)
	v := c.Snapshot()
	if v.Reason != "tier_confirmation" || v.ControlPriority != "critical" {
		t.Fatal("missing tier-tail confirmation", v)
	}
	for i := 0; i < 110; i++ {
		p, elapsed, target := admission.Background, time.Millisecond, 500*time.Millisecond
		if i >= 100 {
			p, elapsed, target = admission.Critical, 150*time.Millisecond, 100*time.Millisecond
		}
		d, g := c.Begin(p, now.Add(2*time.Second))
		if d != admission.Admitted {
			t.Fatal(d)
		}
		c.End(p, g, now.Add(2*time.Second+time.Duration(i)*time.Millisecond), elapsed, target, false, false)
	}
	d, g = c.Begin(admission.Critical, now.Add(4*time.Second))
	if d != admission.Admitted {
		t.Fatal(d)
	}
	c.End(admission.Critical, g, now.Add(4*time.Second), 150*time.Millisecond, 100*time.Millisecond, false, false)
	v = c.Snapshot()
	if v.P90Ratio > .5 || v.ControlRatio <= 1 || v.ControlPriority != "critical" || l.Snapshot().Limit >= 8 {
		t.Fatal("aggregate masked critical tail", v, l.Snapshot())
	}
}

func TestStableRefusedDemandKeepsMinimumSampleRequirement(t *testing.T) {
	l := admission.New(6, 1)
	c := adaptive.New(l, adaptive.Config{Profile: "stable", Min: 2, Max: 12, Interval: time.Second, MinSamples: 10})
	now := time.Unix(100, 0)
	tokens := []uint64{}
	for i := 0; i < 6; i++ {
		d, g := c.Begin(admission.Critical, now)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		tokens = append(tokens, g)
	}
	if d, _ := c.Begin(admission.Critical, now); d != admission.ConcurrencyLimit {
		t.Fatal(d)
	}
	for i := 0; i < 3; i++ {
		c.End(admission.Critical, tokens[i], now.Add(5*time.Second), time.Second, 100*time.Millisecond, false, false)
	}
	if !c.Snapshot().EvaluatedAt.IsZero() || c.Snapshot().PendingSamples != 3 {
		t.Fatal("discarded pending overloaded feedback", c.Snapshot())
	}
	for _, g := range tokens[3:] {
		c.End(admission.Critical, g, now.Add(5*time.Second), time.Second, 100*time.Millisecond, false, false)
	}
	if l.Snapshot().Active != 0 {
		t.Fatal("leaked demand")
	}
}

func TestStableRisingLatencyDoesNotJustifyGrowth(t *testing.T) {
	l := admission.New(8, 1)
	c := adaptive.New(l, adaptive.Config{Profile: "stable", Min: 2, Max: 32, Interval: time.Second, MinSamples: 2})
	now := time.Unix(100, 0)
	// Establish a healthy, genuinely saturated window without a speculative increase.
	stableWindow(t, c, l, &now, 10*time.Millisecond)
	before := l.Snapshot().Limit
	stableWindow(t, c, l, &now, 40*time.Millisecond)
	if l.Snapshot().Limit != before || c.Snapshot().Reason != "latency_trend" {
		t.Fatal("grew during rising latency", l.Snapshot(), c.Snapshot())
	}
}
