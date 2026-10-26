package middleware

import (
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/priority"
	"sync"
	"time"
)

// Statistics is a bounded snapshot independent of any metrics backend. Histograms
// contain admitted work only. Rejected requests do not pollute handler latency.
type Statistics struct {
	TenantAllocations [3]uint64 // share, borrowed, fallback; successful service admissions only.
	Name              string
	State             ServiceState
	Controller        ControllerState
	Tenants           TenantState
	Priorities        [2]PriorityStatistics
	Tiers             []PriorityStatistics
	Resolutions       []ResolutionStatistics
	TierActive        map[Priority]int
	PriorityEnabled   bool
	DurationBounds    [11]float64
	Adjustments       [2][3]uint64 // decrease/increase; latency_pressure/server_errors/healthy_saturation
}

type ResolutionStatistics struct {
	Priority Priority
	Source   string
	Count    uint64
}

type PriorityStatistics struct {
	RejectedTenantShare                                                                                           uint64
	RejectedTier, RejectedCohort, RejectedPriorityInvalid, RejectedPriorityUntrusted                              uint64
	Priority                                                                                                      Priority
	Admitted, RejectedConcurrency, RejectedReservation                                                            uint64
	RejectedTenant, RejectedTenantRegistry, RejectedTenantMissing, RejectedTenantInvalid, RejectedTenantUntrusted uint64
	Outcomes                                                                                                      [3]OutcomeStatistics // completed, canceled, panicked
}

type OutcomeStatistics struct {
	Outcome         string
	StatusClasses   [7]uint64 // 1xx, 2xx, 3xx, 4xx, 5xx, other, none
	Count           uint64
	DurationSum     float64
	DurationBuckets [11]uint64 // cumulative, seconds
}

var durationBounds = [11]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type statistics struct {
	allocations [3]uint64
	mu          sync.Mutex
	priorities  []PriorityStatistics
	indices     map[Priority]int
	resolutions map[Priority]map[string]uint64
	adjustments [2][3]uint64
}

func newStatistics() *statistics {
	return newTierStatistics([]priority.Tier{{Name: "critical"}, {Name: "background"}})
}
func newTierStatistics(tiers []priority.Tier) *statistics {
	s := &statistics{priorities: make([]PriorityStatistics, len(tiers)), indices: map[Priority]int{}, resolutions: map[Priority]map[string]uint64{}}
	for i, tier := range tiers {
		p := Priority(tier.Name)
		s.indices[p] = i
		s.resolutions[p] = map[string]uint64{}
		s.priorities[i].Priority = p
		for j, o := range []string{"completed", "canceled", "panicked"} {
			s.priorities[i].Outcomes[j].Outcome = o
		}
	}
	return s
}

func (s *statistics) admission(p Priority, decision admission.Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := &s.priorities[s.indices[p]]
	switch decision {
	case admission.TierCapacityReserved:
		v.RejectedTier++
	case admission.CohortLimited:
		v.RejectedCohort++
	case admission.PriorityInvalid:
		v.RejectedPriorityInvalid++
	case admission.PriorityUntrusted:
		v.RejectedPriorityUntrusted++
	case admission.Admitted:
		v.Admitted++
	case admission.TenantShareReserved:
		v.RejectedTenantShare++
	case admission.TenantConcurrencyLimit:
		v.RejectedTenant++
	case admission.TenantRegistryFull:
		v.RejectedTenantRegistry++
	case admission.TenantMissing:
		v.RejectedTenantMissing++
	case admission.TenantInvalid:
		v.RejectedTenantInvalid++
	case admission.TenantUntrusted:
		v.RejectedTenantUntrusted++
	case admission.ConcurrencyLimit:
		v.RejectedConcurrency++
	case admission.CriticalCapacityReserved:
		v.RejectedReservation++
	}
}

func (s *statistics) complete(p Priority, code int, completed, canceled bool, elapsed time.Duration) {
	outcome := 0
	if canceled {
		outcome = 1
	} else if !completed {
		outcome = 2
	}
	class := 6
	if outcome == 0 {
		class = 5
		if code >= 100 && code < 600 {
			class = code/100 - 1
		}
	}
	seconds := elapsed.Seconds()
	s.mu.Lock()
	defer s.mu.Unlock()
	o := &s.priorities[s.indices[p]].Outcomes[outcome]
	o.Count++
	o.StatusClasses[class]++
	o.DurationSum += seconds
	for i, bound := range durationBounds {
		if seconds <= bound {
			o.DurationBuckets[i]++
		}
	}
}

func (s *statistics) adjustment(previous, current int, reason string) {
	direction := 0
	if current > previous {
		direction = 1
	}
	index := 0
	switch reason {
	case "server_errors":
		index = 1
	case "healthy_saturation":
		index = 2
	}
	s.mu.Lock()
	s.adjustments[direction][index]++
	s.mu.Unlock()
}

// Statistics returns coherent histogram buckets/counts/sums and a live capacity
// snapshot. Scraping this method does not count as service traffic.
func (s *Service) Statistics() Statistics {
	state := s.Snapshot()
	controller := s.ControllerSnapshot()
	tenants := s.TenantSnapshot()
	s.statistics.mu.Lock()
	defer s.statistics.mu.Unlock()
	var legacy [2]PriorityStatistics
	if s.priorities == nil {
		copy(legacy[:], s.statistics.priorities)
	}
	resolutions := []ResolutionStatistics{}
	for _, p := range s.statistics.priorities {
		for _, source := range []string{"route_default", "context", "trusted_http", "invalid"} {
			resolutions = append(resolutions, ResolutionStatistics{p.Priority, source, s.statistics.resolutions[p.Priority][source]})
		}
	}
	active := make(map[Priority]int)
	for p, n := range s.limiter.TierCounts() {
		active[Priority(p)] = n
	}
	return Statistics{TenantAllocations: s.statistics.allocations, Tiers: append([]PriorityStatistics(nil), s.statistics.priorities...), Resolutions: resolutions, TierActive: active, PriorityEnabled: s.priorities != nil, Name: s.name, State: state, Controller: controller, Tenants: tenants, Priorities: legacy, DurationBounds: durationBounds, Adjustments: s.statistics.adjustments}
}

func (s *statistics) resolution(p Priority, source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolutions[p][source]++
}

func (s *statistics) tenantAllocation(kind string) {
	i := 0
	if kind == "borrowed" {
		i = 1
	} else if kind == "fallback" {
		i = 2
	}
	s.mu.Lock()
	s.allocations[i]++
	s.mu.Unlock()
}
