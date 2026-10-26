// Package tenancy provides bounded, process-local tenant concurrency accounting.
package tenancy

import (
	"container/list"
	"crypto/sha256"
	"github.com/HN026/gatewise/internal/admission"
	"sync"
	"time"
	"unicode/utf8"
)

type Config struct {
	Sharing                       *Sharing
	Limit, MaxTracked             int
	IdleTTL                       time.Duration
	MissingShared, OverflowShared bool
	Limits                        map[string]int
}
type entry struct {
	borrowLimit   int
	demanded      bool
	demandIdle    *list.Element
	demandUntil   time.Time
	key           [32]byte
	active, limit int
	idleSince     time.Time
	idle          *list.Element
	retained      bool
}
type Limiter struct {
	demand                    list.List
	demandShares              int64
	demanding, borrowedActive int
	reclaimUntil              time.Time
	borrowOverrides           map[[32]byte]int
	mu                        sync.Mutex
	config                    Config
	entries                   map[[32]byte]*entry
	overrides                 map[[32]byte]int
	idle                      list.List
	missing, overflow         entry
	expired                   uint64
	identifiedActive          int
}
type Lease struct {
	allocation     string
	share, ceiling int
	owner          *Limiter
	entry          *entry
	released       bool
	identified     bool
}
type State struct {
	SharingEnabled, BorrowingPaused                                             bool
	Demanding, BorrowedActive                                                   int
	DemandShares                                                                int64
	Limit, MaxTracked, Tracked, IdentifiedActive, MissingActive, OverflowActive int
	Expired                                                                     uint64
}

// ValidID bounds hash input and rejects whitespace, control bytes and combined headers.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) {
		return false
	}
	for _, b := range []byte(id) {
		if b <= 32 || b >= 127 || b == ',' {
			return false
		}
	}
	return true
}
func New(c Config) *Limiter {
	l := &Limiter{config: c, entries: make(map[[32]byte]*entry), overrides: make(map[[32]byte]int)}
	for id, limit := range c.Limits {
		l.overrides[sha256.Sum256([]byte(id))] = limit
	}
	if c.Sharing != nil {
		v := *c.Sharing
		l.config.Sharing = &v
		l.borrowOverrides = make(map[[32]byte]int)
		for id, n := range v.Limits {
			l.borrowOverrides[sha256.Sum256([]byte(id))] = n
		}
		l.config.Sharing.Limits = nil
	}
	l.config.Limits = nil
	l.missing.limit = c.Limit
	l.overflow.limit = c.Limit
	return l
}
func (l *Limiter) expire(now time.Time) {
	l.expireDemand(now)
	for budget, front := 64, l.idle.Front(); front != nil && budget > 0; budget, front = budget-1, l.idle.Front() {
		e := front.Value.(*entry)
		if now.Sub(e.idleSince) < l.config.IdleTTL {
			return
		}
		l.forgetDemand(e)
		delete(l.entries, e.key)
		l.idle.Remove(front)
		e.idle = nil
		l.expired++
	}
}
func (l *Limiter) Acquire(id string, now time.Time) (*Lease, admission.Decision) {
	return l.AcquireWithLimit(id, now, 0)
}
func (l *Limiter) AcquireWithLimit(id string, now time.Time, capacity int) (*Lease, admission.Decision) {
	if id != "" && !ValidID(id) {
		return nil, admission.TenantInvalid
	}
	key := sha256.Sum256([]byte(id))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	var e *entry
	identified := false
	if id == "" {
		if !l.config.MissingShared {
			return nil, admission.TenantMissing
		}
		e = &l.missing
	} else if known := l.entries[key]; known != nil {
		e = known
		identified = true
	} else if len(l.entries) < l.config.MaxTracked {
		limit := l.config.Limit
		if override, ok := l.overrides[key]; ok {
			limit = override
		}
		ceiling := limit
		if l.config.Sharing != nil {
			ceiling = l.config.Sharing.MaxConcurrent
			if n, ok := l.borrowOverrides[key]; ok {
				ceiling = n
			}
		}
		e = &entry{key: key, limit: limit, borrowLimit: ceiling}
		l.entries[key] = e
		identified = true
	} else {
		if !l.config.OverflowShared {
			return nil, admission.TenantRegistryFull
		}
		e = &l.overflow
	}
	allocation := "hard_cap"
	share, ceiling := e.limit, e.limit
	if l.config.Sharing != nil && identified {
		l.touchDemand(e, now)
		share, ceiling = l.entitlement(e, capacity, now)
		if e.active >= e.borrowLimit {
			return nil, admission.TenantConcurrencyLimit
		}
		if e.active >= ceiling {
			return nil, admission.TenantShareReserved
		}
		allocation = "share"
		if e.active >= share {
			allocation = "borrowed"
		}
	} else {
		if e.active >= e.limit {
			return nil, admission.TenantConcurrencyLimit
		}
		if l.config.Sharing != nil {
			allocation = "fallback"
		}
	}
	if e.demandIdle != nil {
		l.demand.Remove(e.demandIdle)
		e.demandIdle = nil
	}
	if allocation == "borrowed" {
		l.borrowedActive++
	}
	if e.idle != nil {
		l.idle.Remove(e.idle)
		e.idle = nil
	}
	e.active++
	if identified {
		l.identifiedActive++
	}
	return &Lease{owner: l, entry: e, identified: identified, allocation: allocation, share: share, ceiling: ceiling}, admission.Admitted
}

// Retain records successful service admission. Unused reservations are discarded.
func (t *Lease) Retain() {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	if !t.released {
		t.entry.retained = true
	}
}

// Release is idempotent. Active entries never expire or get evicted.
func (t *Lease) Release(now time.Time) {
	l := t.owner
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.released {
		return
	}
	t.released = true
	t.entry.active--
	if t.allocation == "borrowed" {
		l.borrowedActive--
	}
	if t.identified {
		l.identifiedActive--
		if t.entry.active == 0 && !t.entry.retained {
			delete(l.entries, t.entry.key)
		} else if t.entry.active == 0 {
			t.entry.idleSince = now
			t.entry.idle = l.idle.PushBack(t.entry)
			if l.config.Sharing != nil {
				t.entry.demandUntil = now.Add(l.config.Sharing.DemandTTL)
				t.entry.demandIdle = l.demand.PushBack(t.entry)
			}
		}
	}
}
func (l *Limiter) Snapshot(now time.Time) State {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	return State{Limit: l.config.Limit, MaxTracked: l.config.MaxTracked, Tracked: len(l.entries), IdentifiedActive: l.identifiedActive, MissingActive: l.missing.active, OverflowActive: l.overflow.active, Expired: l.expired, SharingEnabled: l.config.Sharing != nil, BorrowingPaused: now.Before(l.reclaimUntil), Demanding: l.demanding, DemandShares: l.demandShares, BorrowedActive: l.borrowedActive}
}
