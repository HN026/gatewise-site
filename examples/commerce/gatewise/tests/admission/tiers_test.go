package admission_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"testing"
)

func TestCohortGatingAndAdaptiveShrink(t *testing.T) {
	l := admission.NewTiers(4, []admission.Tier{{Name: "urgent", Reserved: 1}, {Name: "batch"}})
	if l.TryAcquire("batch") != "" {
		t.Fatal("initial")
	}
	if l.TryAcquireCohort("batch", 99, 100) != admission.CohortLimited {
		t.Fatal("rank gating")
	}
	if l.TryAcquireCohort("batch", 0, 100) != "" {
		t.Fatal("preferred cohort")
	}
	l.SetLimit(2)
	if l.TryAcquire("urgent") != admission.ConcurrencyLimit {
		t.Fatal("shrink bypassed")
	}
	l.Release("batch")
	if l.TryAcquire("batch") != admission.TierCapacityReserved {
		t.Fatal("reservation bypassed")
	}
	if l.TryAcquire("urgent") != "" {
		t.Fatal("urgent blocked")
	}
	l.Release("urgent")
	l.Release("batch")
	if l.Snapshot().Active != 0 {
		t.Fatal("leak")
	}
}
