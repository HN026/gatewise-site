package adaptive_test

import (
	"github.com/HN026/gatewise/internal/adaptive"
	"github.com/HN026/gatewise/internal/admission"
	"testing"
	"time"
)

func TestIdleGapDiscardsIncompleteSaturationWindow(t *testing.T) {
	now := time.Unix(100, 0)
	l := admission.New(2, 0)
	c := adaptive.New(l, adaptive.Config{Min: 2, Max: 6, Interval: time.Second, MinSamples: 3})
	tokens := make([]uint64, 2)
	for i := range tokens {
		_, tokens[i] = c.Begin(admission.Critical, now)
	}
	now = now.Add(2 * time.Second)
	for _, g := range tokens {
		c.End(admission.Critical, g, now, 10*time.Millisecond, 100*time.Millisecond, false, false)
	}
	if !c.Snapshot().EvaluatedAt.IsZero() {
		t.Fatal("evaluated incomplete window")
	}
	now = now.Add(3 * time.Second)
	for i := 0; i < 3; i++ {
		_, g := c.Begin(admission.Critical, now)
		now = now.Add(100 * time.Millisecond)
		c.End(admission.Critical, g, now, 10*time.Millisecond, 100*time.Millisecond, false, false)
		if i == 1 {
			now = now.Add(time.Second)
		} else {
			now = now.Add(100 * time.Millisecond)
		}
	}
	e := c.Snapshot()
	if e.Samples != 3 || e.Saturated || e.Decision != "hold" || l.Snapshot().Limit != 2 {
		t.Fatalf("idle window carried old saturation: %+v", e)
	}
}
