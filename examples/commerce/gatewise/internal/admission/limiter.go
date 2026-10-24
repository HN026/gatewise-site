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
	Admitted                 Decision = ""
	TenantConcurrencyLimit   Decision = "tenant_concurrency_limit"
	TenantRegistryFull       Decision = "tenant_registry_full"
	TenantMissing            Decision = "tenant_identity_missing"
	TenantInvalid            Decision = "tenant_identity_invalid"
	TenantUntrusted          Decision = "tenant_identity_untrusted"
	ConcurrencyLimit         Decision = "concurrency_limit"
	CriticalCapacityReserved Decision = "critical_capacity_reserved"
	InvalidPriority          Decision = "invalid_priority"
)

// Limiter makes total and background accounting one atomic decision under a short
// lock. Background work cannot occupy the critical reservation, even when idle.
// Critical work can use all available capacity. There is no preemption.
type Limiter struct {
	mu                                  sync.Mutex
	limit, reserved, active, background int
}

func New(limit, reserved int) *Limiter {
	if limit <= 0 || reserved < 0 || reserved > limit {
		panic("admission: invalid limit or reservation")
	}
	return &Limiter{limit: limit, reserved: reserved}
}

// TryAcquire never waits for capacity. Admitted work requires one matching Release.
func (l *Limiter) TryAcquire(priority Priority) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	if priority != Critical && priority != Background {
		return InvalidPriority
	}
	if l.active >= l.limit {
		return ConcurrencyLimit
	}
	if priority == Background && l.background >= l.limit-l.reserved {
		return CriticalCapacityReserved
	}
	l.active++
	if priority == Background {
		l.background++
	}
	return Admitted
}

func (l *Limiter) Release(priority Priority) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	if priority == Background {
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
