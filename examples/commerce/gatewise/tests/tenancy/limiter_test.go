package tenancy_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/tenancy"
	"testing"
	"time"
)

func TestBoundsExpiryAndFallbacks(t *testing.T) {
	now := time.Unix(100, 0)
	l := tenancy.New(tenancy.Config{Limit: 1, MaxTracked: 1, IdleTTL: time.Second, MissingShared: true, OverflowShared: true})
	a, d := l.Acquire("a", now)
	if d != admission.Admitted {
		t.Fatal(d)
	}
	a.Retain()
	if _, d = l.Acquire("a", now); d != admission.TenantConcurrencyLimit {
		t.Fatal(d)
	}
	// An active tenant must survive arbitrarily long time and cannot be evicted.
	b, d := l.Acquire("b", now.Add(time.Hour))
	if d != admission.Admitted {
		t.Fatal(d)
	}
	if _, d = l.Acquire("c", now.Add(time.Hour)); d != admission.TenantConcurrencyLimit {
		t.Fatal("overflow bucket bypass", d)
	}
	missing, d := l.Acquire("", now)
	if d != admission.Admitted {
		t.Fatal(d)
	}
	if _, d = l.Acquire("", now); d != admission.TenantConcurrencyLimit {
		t.Fatal(d)
	}
	state := l.Snapshot(now.Add(time.Hour))
	if state.Tracked != 1 || state.IdentifiedActive != 1 || state.OverflowActive != 1 || state.MissingActive != 1 {
		t.Fatal(state)
	}
	a.Release(now.Add(time.Hour))
	a.Release(now.Add(time.Hour))
	b.Release(now)
	missing.Release(now)
	state = l.Snapshot(now.Add(time.Hour + 2*time.Second))
	if state.Tracked != 0 || state.Expired != 1 || state.IdentifiedActive != 0 {
		t.Fatal(state)
	}
}
func TestUncommittedReservationDoesNotFillRegistry(t *testing.T) {
	l := tenancy.New(tenancy.Config{Limit: 1, MaxTracked: 1, IdleTTL: time.Hour})
	now := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		lease, d := l.Acquire(id, now)
		if d != admission.Admitted {
			t.Fatal(d)
		}
		lease.Release(now)
	}
	if l.Snapshot(now).Tracked != 0 {
		t.Fatal("service-rejected identity retained")
	}
	a, d := l.Acquire("a", now)
	if d != admission.Admitted {
		t.Fatal(d)
	}
	a.Retain()
	a.Release(now)
	if _, d = l.Acquire("b", now); d != admission.TenantRegistryFull {
		t.Fatal(d)
	}
	if _, d = l.Acquire("", now); d != admission.TenantMissing {
		t.Fatal(d)
	}
}
