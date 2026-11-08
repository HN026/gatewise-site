package tenancy

import (
	"github.com/HN026/gatewise/internal/admission"
	"time"
)

// Sharing is validated by the public middleware constructor. Shares use the
// existing default Limit and per-identity overrides. Only identified tenants borrow.
type Sharing struct {
	Capacity, MaxConcurrent int
	Limits                  map[string]int
	DemandTTL               time.Duration
}

func (l *Limiter) SharingEnabled() bool { return l.config.Sharing != nil }
func (l *Limiter) forgetDemand(e *entry) {
	if e.demandIdle != nil {
		l.demand.Remove(e.demandIdle)
		e.demandIdle = nil
	}
	if e.demanded {
		l.demandShares -= int64(e.limit)
		l.demanding--
		e.demanded = false
	}
}
func (l *Limiter) expireDemand(now time.Time) {
	if l.config.Sharing == nil {
		return
	}
	for budget := 64; budget > 0; budget-- {
		front := l.demand.Front()
		if front == nil {
			return
		}
		e := front.Value.(*entry)
		if now.Before(e.demandUntil) {
			return
		}
		l.forgetDemand(e)
	}
}
func (l *Limiter) touchDemand(e *entry, now time.Time) {
	if !e.demanded {
		e.demanded = true
		l.demandShares += int64(e.limit)
		l.demanding++
	}
	// Rejected attempts also express demand. Retain their bounded registry entry.
	e.retained = true
	if e.demandIdle != nil {
		l.demand.Remove(e.demandIdle)
		e.demandIdle = nil
	}
	if e.active == 0 {
		e.demandUntil = now.Add(l.config.Sharing.DemandTTL)
		e.demandIdle = l.demand.PushBack(e)
	}
}
func (l *Limiter) entitlement(e *entry, capacity int, now time.Time) (share, ceiling int) {
	if capacity <= 0 {
		capacity = l.config.Sharing.Capacity
	}
	capacity -= l.missing.active + l.overflow.active
	if capacity < 1 {
		capacity = 1
	}
	share = e.limit
	if l.demandShares > int64(capacity) {
		// Proportional integer targets; minimum one is a target, not a reserved slot.
		share = int(int64(capacity) * int64(e.limit) / l.demandShares)
		if share < 1 {
			share = 1
		}
		ceiling = share // No lending while configured shares exceed current capacity.
	} else {
		ceiling = capacity - int(l.demandShares-int64(e.limit))
	}
	if ceiling > e.borrowLimit {
		ceiling = e.borrowLimit
	}
	if now.Before(l.reclaimUntil) && ceiling > share {
		ceiling = share
	}
	return
}
func (t *Lease) Allocation() string { return t.allocation }
func (t *Lease) Share() int         { return t.share }
func (t *Lease) Ceiling() int       { return t.ceiling }

// Rejected pauses lending after a below-share attempt hits an execution bound.
// Cohort/identity rejections do not create reclaim demand. No executing work is revoked.
func (t *Lease) Rejected(decision admission.Decision, now time.Time) {
	l := t.owner
	if l.config.Sharing == nil || !t.identified || t.allocation != "share" {
		return
	}
	if decision != admission.ConcurrencyLimit && decision != admission.CriticalCapacityReserved && decision != admission.TierCapacityReserved {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !t.released {
		l.reclaimUntil = now.Add(l.config.Sharing.DemandTTL)
	}
}
