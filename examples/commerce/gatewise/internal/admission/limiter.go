// Package admission provides process-local concurrency admission without a waiting queue.
package admission

import "sync"

type Priority string

const (
	Critical   Priority = "critical"
	Background Priority = "background"
)

type Decision string

const (
	Admitted                    Decision = ""
	WriteBytesLimit             Decision = "write_bytes_limit"
	HotKeyLimit                 Decision = "hot_key_limit"
	ResourceKeyRegistryFull     Decision = "resource_key_registry_full"
	ResourceCostMissing         Decision = "resource_cost_missing"
	ResourceKeyMissing          Decision = "resource_key_missing"
	ResourceMetadataInvalid     Decision = "resource_metadata_invalid"
	ResourceMetadataUntrusted   Decision = "resource_metadata_untrusted"
	ResourceAccountingExhausted Decision = "resource_accounting_exhausted"
	SignalPressure              Decision = "signal_pressure"
	SignalUnavailable           Decision = "signal_unavailable"
	SignalConcurrencyLimit      Decision = "signal_concurrency_limit"
	TenantShareReserved         Decision = "tenant_share_reserved"
	TenantConcurrencyLimit      Decision = "tenant_concurrency_limit"
	TenantRegistryFull          Decision = "tenant_registry_full"
	TenantMissing               Decision = "tenant_identity_missing"
	TenantInvalid               Decision = "tenant_identity_invalid"
	TenantUntrusted             Decision = "tenant_identity_untrusted"
	ConcurrencyLimit            Decision = "concurrency_limit"
	CriticalCapacityReserved    Decision = "critical_capacity_reserved"
	InvalidPriority             Decision = "invalid_priority"
	TierCapacityReserved        Decision = "tier_capacity_reserved"
	CohortLimited               Decision = "cohort_limited"
	PriorityInvalid             Decision = "priority_metadata_invalid"
	PriorityUntrusted           Decision = "priority_metadata_untrusted"
)

// Limiter makes total and background accounting one atomic decision under a short
// lock. Background work cannot occupy the critical reservation, even when idle.
// Critical work can use all available capacity. There is no preemption.
type Limiter struct {
	mu                                  sync.Mutex
	limit, reserved, active, background int
	tiers                               []Tier
	counts                              []int
	legacy                              bool
}

func New(limit, reserved int) *Limiter {
	if limit <= 0 || reserved < 0 || reserved > limit {
		panic("admission: invalid limit or reservation")
	}
	return &Limiter{limit: limit, reserved: reserved, tiers: []Tier{{Critical, reserved}, {Background, 0}}, counts: make([]int, 2), legacy: true}
}

// TryAcquire never waits for capacity. Admitted work requires one matching Release.
type Tier struct {
	Name     Priority
	Reserved int
}

// NewTiers accepts validated, highest-first tiers. Prefix reservations protect
// higher tiers from the combined occupancy of every lower tier.
func NewTiers(limit int, tiers []Tier) *Limiter {
	reserved := 0
	for _, t := range tiers {
		reserved += t.Reserved
	}
	return &Limiter{limit: limit, reserved: reserved, tiers: append([]Tier(nil), tiers...), counts: make([]int, len(tiers))}
}
func (l *Limiter) index(p Priority) int {
	for i, t := range l.tiers {
		if t.Name == p {
			return i
		}
	}
	return -1
}
func (l *Limiter) TryAcquire(p Priority) Decision { return l.TryAcquireCohort(p, -1, 0) }
func (l *Limiter) TryAcquireCohort(p Priority, bucket, buckets int) Decision {
	return l.TryAcquireBudget(p, bucket, buckets, 0)
}

// TryAcquireBudget checks an optional signal ceiling under the same lock as all
// other capacity rules. It never changes the adaptive controller baseline.
func (l *Limiter) TryAcquireBudget(p Priority, bucket, buckets, ceiling int) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	index := l.index(p)
	if index < 0 {
		return InvalidPriority
	}
	if l.active >= l.limit {
		return ConcurrencyLimit
	}
	effective := l.limit
	if ceiling > 0 && ceiling < effective {
		effective = ceiling
	}
	if l.active >= effective {
		return SignalConcurrencyLimit
	}
	headroom, budget := effective-l.active, effective
	reserved := 0
	for boundary := 1; boundary <= index; boundary++ {
		reserved += l.tiers[boundary-1].Reserved
		occupied := 0
		for _, n := range l.counts[boundary:] {
			occupied += n
		}
		cap := l.limit - reserved
		if occupied >= cap {
			if l.legacy {
				return CriticalCapacityReserved
			}
			return TierCapacityReserved
		}
		cap = effective - reserved
		if occupied >= cap {
			return SignalConcurrencyLimit
		}
		if float64(cap-occupied)/float64(cap) < float64(headroom)/float64(budget) {
			headroom = cap - occupied
			budget = cap
		}
	}
	// Gate stable cohorts by current headroom; no rank overrides hard capacity.
	// Empty eligible capacity admits all buckets. Queuing/fair-share borrowing is separate.
	if buckets > 0 && bucket >= 0 && float64(bucket)/float64(buckets) >= float64(headroom)/float64(budget) {
		return CohortLimited
	}
	l.active++
	l.counts[index]++
	if index == len(l.tiers)-1 {
		l.background++
	}
	return Admitted
}

func (l *Limiter) Release(priority Priority) {
	l.mu.Lock()
	defer l.mu.Unlock()
	index := l.index(priority)
	if index < 0 || l.counts[index] == 0 {
		panic("admission: unmatched release")
	}
	l.active--
	l.counts[index]--
	if index == len(l.tiers)-1 {
		l.background--
	}
}

type State struct{ Limit, ReservedCritical, Active, Background int }

func (l *Limiter) Snapshot() State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return State{Limit: l.limit, ReservedCritical: l.reserved, Active: l.active, Background: l.background}
}

// SetLimit affects future admissions only. Existing work drains normally.
func (l *Limiter) SetLimit(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit < l.reserved {
		panic("admission: limit below reservation")
	}
	l.limit = limit
}

// TierCounts returns a detached snapshot in configured order.
func (l *Limiter) TierCounts() map[Priority]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := make(map[Priority]int, len(l.tiers))
	for i, t := range l.tiers {
		m[t.Name] = l.counts[i]
	}
	return m
}
