package loadplan_test

import (
	"github.com/HN026/gatewise-examples/commerce/internal/loadplan"
	"testing"
)

func TestSeededPlan(t *testing.T) {
	a := loadplan.New(1000, 1, 1000, 1000, 4, 101)
	b := loadplan.New(1000, 1, 1000, 1000, 4, 101)
	c := loadplan.New(1000, 1, 1000, 1000, 4, 102)
	if loadplan.Digest(a) != loadplan.Digest(b) || loadplan.Digest(a) == loadplan.Digest(c) {
		t.Fatal("seed did not identify the offered plan")
	}
	counts := map[string]int{}
	critical := 0
	irregular := false
	for i, p := range a {
		counts[p.Role]++
		if p.Critical {
			critical++
		}
		if p.Seconds < 0 || p.Seconds >= 1 || i > 0 && p.Seconds < a[i-1].Seconds {
			t.Fatal("invalid schedule")
		}
		if i > 1 && p.Seconds-a[i-1].Seconds != a[i-1].Seconds-a[i-2].Seconds {
			irregular = true
		}
	}
	if counts["orders"] != 600 || counts["catalog"] != 200 || counts["inventory"] != 200 || critical != 800 || !irregular {
		t.Fatal(counts, critical, irregular)
	}
	legacy := loadplan.New(25, 1, 25, 25, 1, 0)
	if legacy[5].Role != "catalog" || !legacy[5].Critical || legacy[1].Critical || legacy[5].Seconds != .2 {
		t.Fatal("legacy plan changed")
	}
}
