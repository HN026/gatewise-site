package adaptive_test

import (
	"github.com/HN026/gatewise/internal/adaptive"
	"github.com/HN026/gatewise/internal/admission"
	"testing"
	"time"
)

func TestEvaluationExplainsHoldAndBoundedPressure(t *testing.T) {
	now := time.Unix(100, 0)
	l := admission.New(2, 1)
	c := adaptive.New(l, adaptive.Config{Min: 2, Max: 4, Interval: time.Second, MinSamples: 2})
	if !c.Snapshot().EvaluatedAt.IsZero() {
		t.Fatal("evaluated without traffic")
	}
	complete := func(duration time.Duration, failed bool) {
		d, g := c.Begin(admission.Critical, now)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		now = now.Add(time.Second)
		c.End(admission.Critical, g, now, duration, 100*time.Millisecond, failed, false)
	}
	complete(10*time.Millisecond, false)
	if !c.Snapshot().EvaluatedAt.IsZero() {
		t.Fatal("evaluated before minimum samples")
	}
	complete(10*time.Millisecond, false)
	e := c.Snapshot()
	if e.Decision != "hold" || e.Samples != 2 || e.Saturated || e.FailureFraction != 0 {
		t.Fatalf("wrong healthy hold: %+v", e)
	}
	complete(250*time.Millisecond, true)
	complete(250*time.Millisecond, false)
	e = c.Snapshot()
	if e.Decision != "hold" || e.Reason != "server_errors" || e.FailureFraction != 0.5 || e.Limit != 2 || e.P90Ratio != 4 {
		t.Fatalf("minimum-bound pressure not explained: %+v", e)
	}
	if l.Snapshot().Active != 0 {
		t.Fatal("capacity leaked")
	}
}
