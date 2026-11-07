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
	Limit, MaxTracked             int
	IdleTTL                       time.Duration
	MissingShared, OverflowShared bool
	Limits                        map[string]int
}
type entry struct {
	key           [32]byte
	active, limit int
	idleSince     time.Time
	idle          *list.Element
	retained      bool
}
type Limiter struct {
	mu                sync.Mutex
	config            Config
	entries           map[[32]byte]*entry
	overrides         map[[32]byte]int
	idle              list.List
	missing, overflow entry
	expired           uint64
	identifiedActive  int
}
type Lease struct {
	owner      *Limiter
	entry      *entry
	released   bool
	identified bool
}
type State struct {
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
	l.config.Limits = nil
	l.missing.limit = c.Limit
	l.overflow.limit = c.Limit
	return l
}
func (l *Limiter) expire(now time.Time) {
	for budget, front := 64, l.idle.Front(); front != nil && budget > 0; budget, front = budget-1, l.idle.Front() {
		e := front.Value.(*entry)
		if now.Sub(e.idleSince) < l.config.IdleTTL {
			return
		}
		delete(l.entries, e.key)
		l.idle.Remove(front)
		e.idle = nil
		l.expired++
	}
}
func (l *Limiter) Acquire(id string, now time.Time) (*Lease, admission.Decision) {
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
		e = &entry{key: key, limit: limit}
		l.entries[key] = e
		identified = true
	} else {
		if !l.config.OverflowShared {
			return nil, admission.TenantRegistryFull
		}
		e = &l.overflow
	}
	if e.active >= e.limit {
		return nil, admission.TenantConcurrencyLimit
	}
	if e.idle != nil {
		l.idle.Remove(e.idle)
		e.idle = nil
	}
	e.active++
	if identified {
		l.identifiedActive++
	}
	return &Lease{owner: l, entry: e, identified: identified}, admission.Admitted
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
	if t.identified {
		l.identifiedActive--
		if t.entry.active == 0 && !t.entry.retained {
			delete(l.entries, t.entry.key)
		} else if t.entry.active == 0 {
			t.entry.idleSince = now
			t.entry.idle = l.idle.PushBack(t.entry)
		}
	}
}
func (l *Limiter) Snapshot(now time.Time) State {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	return State{l.config.Limit, l.config.MaxTracked, len(l.entries), l.identifiedActive, l.missing.active, l.overflow.active, l.expired}
}
