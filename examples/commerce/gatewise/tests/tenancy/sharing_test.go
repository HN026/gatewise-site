package tenancy_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/tenancy"
	"testing"
	"time"
)

func sharing() *tenancy.Limiter {
	return tenancy.New(tenancy.Config{Limit: 2, MaxTracked: 8, IdleTTL: time.Second, Sharing: &tenancy.Sharing{Capacity: 8, MaxConcurrent: 8, DemandTTL: 100 * time.Millisecond}})
}
func TestSharingReturnAndRecovery(t *testing.T) {
	l := sharing()
	now := time.Unix(100, 0)
	leases := []*tenancy.Lease{}
	for i := 0; i < 8; i++ {
		a, d := l.AcquireWithLimit("busy", now, 8)
		if d != "" {
			t.Fatal(d)
		}
		a.Retain()
		leases = append(leases, a)
		if (i >= 2) != (a.Allocation() == "borrowed") {
			t.Fatal(i, a.Allocation())
		}
	}
	b, d := l.AcquireWithLimit("returning", now, 8)
	if d != "" || b.Share() != 2 {
		t.Fatal(d)
	}
	b.Rejected(admission.ConcurrencyLimit, now)
	b.Release(now)
	if !l.Snapshot(now).BorrowingPaused {
		t.Fatal("reclaim not recorded")
	}
	if _, d = l.AcquireWithLimit("busy", now, 8); d != admission.TenantConcurrencyLimit {
		t.Fatal(d)
	}
	for _, a := range leases[2:] {
		a.Release(now)
	}
	if _, d = l.AcquireWithLimit("busy", now, 8); d != admission.TenantShareReserved {
		t.Fatal("borrower did not back off", d)
	}
	b, d = l.AcquireWithLimit("returning", now, 8)
	if d != "" || b.Allocation() != "share" {
		t.Fatal(d)
	}
	b.Retain()
	b.Release(now)
	for _, a := range leases[:2] {
		a.Release(now)
	}
	later := now.Add(200 * time.Millisecond)
	for i := 0; i < 8; i++ {
		a, d := l.AcquireWithLimit("busy", later, 8)
		if d != "" {
			t.Fatal("unused capacity not lent", i, d)
		}
		leases = append(leases, a)
	}
	for _, a := range leases {
		a.Release(later)
		a.Release(later)
	}
	v := l.Snapshot(later.Add(2 * time.Second))
	if v.Tracked != 0 || v.Demanding != 0 || v.DemandShares != 0 || v.BorrowedActive != 0 {
		t.Fatal(v)
	}
}
func TestSharingProtectsRecentDemandAndScalesTargets(t *testing.T) {
	now := time.Unix(100, 0)
	l := sharing()
	b, _ := l.AcquireWithLimit("other", now, 8)
	b.Retain()
	b.Release(now)
	var leases []*tenancy.Lease
	for i := 0; i < 6; i++ {
		a, d := l.AcquireWithLimit("busy", now, 8)
		if d != "" {
			t.Fatal(d)
		}
		leases = append(leases, a)
	}
	if _, d := l.AcquireWithLimit("busy", now, 8); d != admission.TenantShareReserved {
		t.Fatal("did not reserve recent peer share", d)
	}
	for _, a := range leases {
		a.Release(now)
	}
	// 4 configured shares compete for capacity 3. Targets scale to one each;
	// no borrowing is allowed, even though configured shares are two each.
	a, d := l.AcquireWithLimit("busy", now, 3)
	if d != "" || a.Share() != 1 {
		t.Fatal(d, a.Share())
	}
	if _, d = l.AcquireWithLimit("busy", now, 3); d != admission.TenantShareReserved {
		t.Fatal(d)
	}
	a.Release(now)
	// Cohort filtering must not trigger a lending pause.
	a, _ = l.AcquireWithLimit("busy", now, 8)
	a.Rejected(admission.CohortLimited, now)
	a.Release(now)
	if l.Snapshot(now).BorrowingPaused {
		t.Fatal("cohort rejection triggered reclaim")
	}
}
func TestWeightedSharesAndFallback(t *testing.T) {
	now := time.Unix(100, 0)
	l := tenancy.New(tenancy.Config{Limit: 2, Limits: map[string]int{"premium": 4}, MaxTracked: 2, IdleTTL: time.Second, MissingShared: true, OverflowShared: true, Sharing: &tenancy.Sharing{Capacity: 4, MaxConcurrent: 4, DemandTTL: time.Second}})
	a, _ := l.AcquireWithLimit("premium", now, 4)
	b, d := l.AcquireWithLimit("ordinary", now, 4)
	if d != "" || b.Share() != 1 {
		t.Fatal(d)
	}
	a.Release(now)
	b.Release(now)
	a, d = l.AcquireWithLimit("premium", now, 4)
	if d != "" || a.Share() != 2 {
		t.Fatal(d, a.Share())
	}
	missing, _ := l.AcquireWithLimit("", now, 4)
	overflow, _ := l.AcquireWithLimit("untracked", now, 4)
	if missing.Allocation() != "fallback" || overflow.Allocation() != "fallback" {
		t.Fatal("fallback borrowed")
	}
	a.Release(now)
	missing.Release(now)
	overflow.Release(now)
}
