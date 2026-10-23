package admission_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCriticalReservation(t *testing.T) {
	l := admission.New(3, 1)
	acquire := func(p admission.Priority, want admission.Decision) {
		t.Helper()
		if got := l.TryAcquire(p); got != want {
			t.Fatalf("%s: got %q, want %q", p, got, want)
		}
	}
	acquire(admission.Background, admission.Admitted)
	acquire(admission.Background, admission.Admitted)
	acquire(admission.Background, admission.CriticalCapacityReserved)
	acquire(admission.Critical, admission.Admitted)
	acquire(admission.Critical, admission.ConcurrencyLimit)
	l.Release(admission.Critical)
	acquire(admission.Background, admission.CriticalCapacityReserved)
	l.Release(admission.Background)
	l.Release(admission.Background)
	// Critical work can borrow every unused slot, not just its reservation.
	for i := 0; i < 3; i++ {
		acquire(admission.Critical, admission.Admitted)
	}
	acquire(admission.Background, admission.ConcurrencyLimit)
	for i := 0; i < 3; i++ {
		l.Release(admission.Critical)
	}
	acquire(admission.Background, admission.Admitted)
	l.Release(admission.Background)
	reservedAll := admission.New(1, 1)
	if reservedAll.TryAcquire(admission.Background) != admission.CriticalCapacityReserved || reservedAll.TryAcquire(admission.Critical) != admission.Admitted {
		t.Fatal("full reservation policy failed")
	}
	reservedAll.Release(admission.Critical)
}

func TestMixedPriorityConcurrency(t *testing.T) {
	l := admission.New(3, 1)
	var total, background atomic.Int64
	var ready, done sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < 60; i++ {
		priority := admission.Background
		if i%3 == 0 {
			priority = admission.Critical
		}
		ready.Add(1)
		done.Add(1)
		go func(p admission.Priority) {
			defer done.Done()
			accepted := l.TryAcquire(p) == admission.Admitted
			if accepted {
				total.Add(1)
				if p == admission.Background {
					background.Add(1)
				}
			}
			ready.Done()
			if accepted {
				<-release
				l.Release(p)
			}
		}(priority)
	}
	ready.Wait()
	if total.Load() != 3 || background.Load() > 2 {
		t.Errorf("total=%d background=%d", total.Load(), background.Load())
	}
	close(release)
	done.Wait()
	for i := 0; i < 2; i++ {
		if l.TryAcquire(admission.Background) != admission.Admitted {
			t.Fatal("background capacity leaked")
		}
	}
	if l.TryAcquire(admission.Critical) != admission.Admitted {
		t.Fatal("critical capacity leaked")
	}
	l.Release(admission.Background)
	l.Release(admission.Background)
	l.Release(admission.Critical)
}
