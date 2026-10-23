package adaptive_test

import (
	"github.com/HN026/gatewise/internal/adaptive"
	"github.com/HN026/gatewise/internal/admission"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSlowdownRecoveryAndBounds(t *testing.T) {
	l := admission.New(6, 1)
	c := adaptive.New(l, adaptive.Config{Min: 2, Max: 6, Interval: time.Second, MinSamples: 2})
	now := time.Unix(100, 0)
	batch := func(slow bool) {
		t.Helper()
		limit := l.Snapshot().Limit
		tokens := make([]uint64, limit)
		for i := range tokens {
			decision, token := c.Begin(admission.Critical, now)
			if decision != admission.Admitted {
				t.Fatal("unexpected rejection")
			}
			tokens[i] = token
		}
		now = now.Add(2 * time.Second)
		duration := 10 * time.Millisecond
		if slow {
			duration = 250 * time.Millisecond
		}
		for _, token := range tokens {
			c.End(admission.Critical, token, now, duration, 100*time.Millisecond, false, false)
		}
		if l.Snapshot().Active != 0 {
			t.Fatal("old-generation requests leaked capacity")
		}
	}
	batch(true)
	if got := l.Snapshot().Limit; got != 4 {
		t.Fatalf("slow limit=%d", got)
	}
	batch(true)
	batch(true)
	batch(true)
	if got := l.Snapshot().Limit; got != 2 {
		t.Fatalf("minimum limit=%d", got)
	}
	for i := 0; i < 8; i++ {
		batch(false)
	}
	if got := l.Snapshot().Limit; got != 6 {
		t.Fatalf("recovery maximum=%d", got)
	}
	// Reducing a limit does not preempt already admitted work.
	for i := 0; i < 6; i++ {
		if l.TryAcquire(admission.Critical) != admission.Admitted {
			t.Fatal("missing capacity")
		}
	}
	l.SetLimit(2)
	if l.Snapshot().Active != 6 || l.TryAcquire(admission.Critical) != admission.ConcurrencyLimit {
		t.Fatal("shrink invalidated active work")
	}
	for i := 0; i < 6; i++ {
		l.Release(admission.Critical)
	}
	if l.TryAcquire(admission.Background) != admission.Admitted || l.TryAcquire(admission.Background) != admission.CriticalCapacityReserved || l.TryAcquire(admission.Critical) != admission.Admitted {
		t.Fatal("reservation failed after shrink")
	}
	l.Release(admission.Background)
	l.Release(admission.Critical)
}

func TestEvidenceAndRouteBudgets(t *testing.T) {
	l := admission.New(4, 1)
	c := adaptive.New(l, adaptive.Config{Min: 2, Max: 6, Interval: time.Second, MinSamples: 2})
	now := time.Unix(100, 0)
	// One healthy request at a time is not evidence that more capacity is needed.
	for i := 0; i < 4; i++ {
		d, g := c.Begin(admission.Critical, now)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		now = now.Add(time.Second)
		c.End(admission.Critical, g, now, 10*time.Millisecond, 100*time.Millisecond, false, false)
	}
	if l.Snapshot().Limit != 4 {
		t.Fatal("grew without saturation")
	}
	// A naturally slow handler within its explicit budget should not cause shrink.
	for i := 0; i < 2; i++ {
		_, g := c.Begin(admission.Critical, now)
		now = now.Add(time.Second)
		c.End(admission.Critical, g, now, 250*time.Millisecond, time.Second, false, false)
	}
	if l.Snapshot().Limit != 4 {
		t.Fatal("mixed handler cost caused false pressure")
	}
	// Canceled requests release capacity without producing misleading fast samples.
	_, g := c.Begin(admission.Critical, now)
	now = now.Add(time.Second)
	c.End(admission.Critical, g, now, 10*time.Second, time.Millisecond, true, true)
	if l.Snapshot().Limit != 4 || l.Snapshot().Active != 0 {
		t.Fatal("cancellation changed limit or leaked capacity")
	}
	// Fast server errors must not be treated as healthy growth evidence.
	for i := 0; i < 2; i++ {
		_, g := c.Begin(admission.Critical, now)
		now = now.Add(time.Second)
		c.End(admission.Critical, g, now, time.Millisecond, time.Second, true, false)
	}
	if l.Snapshot().Limit >= 4 {
		t.Fatal("server failures did not decrease admission")
	}
}

func TestConcurrentFeedbackAndSnapshots(t *testing.T) {
	l := admission.New(4, 1)
	c := adaptive.New(l, adaptive.Config{Min: 2, Max: 8, Interval: time.Nanosecond, MinSamples: 2})
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			priority := admission.Critical
			if index%2 == 0 {
				priority = admission.Background
			}
			for j := 0; j < 50; j++ {
				decision, generation := c.Begin(priority, time.Now())
				if decision == admission.Admitted {
					runtime.Gosched()
					elapsed := time.Millisecond
					if j%3 == 0 {
						elapsed = 100 * time.Millisecond
					}
					c.End(priority, generation, time.Now(), elapsed, 10*time.Millisecond, false, false)
				}
				state := l.Snapshot()
				if state.Limit < 2 || state.Limit > 8 || state.Active < 0 || state.Background < 0 {
					t.Errorf("invalid concurrent state: %+v", state)
				}
			}
		}(i)
	}
	workers.Wait()
	if state := l.Snapshot(); state.Active != 0 || state.Background != 0 {
		t.Fatalf("concurrent feedback leaked work: %+v", state)
	}
}
