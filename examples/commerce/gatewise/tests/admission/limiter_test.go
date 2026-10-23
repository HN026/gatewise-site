package admission_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLimitAndRelease(t *testing.T) {
	const limit = 3
	l := admission.New(limit, 0)
	var admitted atomic.Int64
	var ready sync.WaitGroup
	release := make(chan struct{})
	var done sync.WaitGroup
	for i := 0; i < 40; i++ {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ok := l.TryAcquire(admission.Critical) == admission.Admitted
			if ok {
				admitted.Add(1)
			}
			ready.Done()
			if ok {
				<-release
				l.Release(admission.Critical)
			}
		}()
	}
	ready.Wait()
	if got := admitted.Load(); got != limit {
		t.Errorf("admitted %d; want %d", got, limit)
	}
	close(release)
	done.Wait()
	for i := 0; i < limit; i++ {
		if l.TryAcquire(admission.Critical) != admission.Admitted {
			t.Fatal("released capacity was not reusable")
		}
	}
	if l.TryAcquire(admission.Critical) == admission.Admitted {
		t.Fatal("limit exceeded")
	}
	for i := 0; i < limit; i++ {
		l.Release(admission.Critical)
	}
}
