package decision

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"math"
	"sync"
	"time"
)

// Cost is server-resolved operation metadata. WriteBytes describes planned
// logical writes, not HTTP body length, WAL bytes or an inferred database cost.
// Key is never retained in plaintext, returned in diagnostics or used as a label.
type Cost struct {
	WriteBytes int64  `json:"write_bytes"`
	Key        string `json:"key,omitempty"`
}
type KeyBudget struct {
	MaxConcurrent, MaxTracked int
	IdleTTL                   time.Duration
	Missing, Overflow         string // reject (default), or shared bounded fallback.
}
type Budget struct {
	Name, Resource string
	MaxWriteBytes  int64
	Keys           *KeyBudget
}
type BudgetAssessment struct {
	Name, Action, Reason                        string
	WriteBytes, ActiveWriteBytes, MaxWriteBytes int64
	KeyActive, KeyLimit                         int
	KeyBucket                                   string
}
type BudgetSnapshot struct {
	Name, Resource                                                          string
	MaxWriteBytes, ActiveWriteBytes                                         int64
	Active, Tracked, TrackingLimit, KeyLimit, MissingActive, OverflowActive int
	Expired, Attempts, WouldReject, Rejected, Admitted                      uint64
}
type keyEntry struct {
	hash   [32]byte
	active int
	idleAt time.Time
	idle   *list.Element
}
type budgetState struct {
	config                                             Budget
	keys                                               map[[32]byte]*keyEntry
	idle                                               list.List
	bytes                                              int64
	active, missing, overflow                          int
	expired, attempts, wouldReject, rejected, admitted uint64
}
type reservation struct {
	index  int
	key    *keyEntry
	bucket string
	bytes  int64
}

// Lease tracks only resource reservations; it is not an operation-level service
// admission API. Middleware rolls it back on later rejection and releases it at
// handler completion, including cancellation/panic. Release is idempotent.
type Lease struct {
	engine       *Engine
	reservations []reservation
	once         sync.Once
	committed    bool
	released     bool
}

func (l *Lease) Retain() {
	if l == nil {
		return
	}
	l.engine.mu.Lock()
	defer l.engine.mu.Unlock()
	if l.committed || l.released {
		return
	}
	l.committed = true
	for _, r := range l.reservations {
		l.engine.budgets[r.index].admitted++
	}
}
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		e := l.engine
		e.mu.Lock()
		defer e.mu.Unlock()
		l.released = true
		for _, r := range l.reservations {
			s := &e.budgets[r.index]
			s.bytes -= r.bytes
			s.active--
			if r.key != nil {
				r.key.active--
				if r.key.active == 0 {
					r.key.idleAt = time.Now()
					r.key.idle = s.idle.PushBack(r.key)
				}
			}
			if r.bucket == "missing" {
				s.missing--
			}
			if r.bucket == "overflow" {
				s.overflow--
			}
		}
	})
}
func validKey(key string) bool {
	if len(key) > 128 {
		return false
	}
	for _, c := range []byte(key) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func (e *Engine) initBudgets(config []Budget) error {
	if len(config) > 32 {
		return fmt.Errorf("decision: at most 32 resource budgets")
	}
	names := map[string]bool{}
	for _, b := range config {
		if !ValidName(b.Name) || names[b.Name] || b.Resource != "" && !ValidName(b.Resource) || b.MaxWriteBytes < 0 || b.MaxWriteBytes == 0 && b.Keys == nil {
			return fmt.Errorf("decision: invalid resource budget")
		}
		names[b.Name] = true
		if b.Keys != nil {
			k := *b.Keys
			if k.MaxTracked == 0 {
				k.MaxTracked = 1024
			}
			if k.IdleTTL == 0 {
				k.IdleTTL = 5 * time.Minute
			}
			if k.Missing == "" {
				k.Missing = "reject"
			}
			if k.Overflow == "" {
				k.Overflow = "reject"
			}
			if k.MaxConcurrent < 1 || k.MaxConcurrent > 1000000000 || k.MaxTracked < 1 || k.MaxTracked > 100000 || k.IdleTTL <= 0 || k.IdleTTL > 24*time.Hour || k.Missing != "reject" && k.Missing != "shared" || k.Overflow != "reject" && k.Overflow != "shared" {
				return fmt.Errorf("decision: invalid key budget/expiry/fallback")
			}
			b.Keys = &k
		}
		e.budgets = append(e.budgets, budgetState{config: b, keys: map[[32]byte]*keyEntry{}})
	}
	return nil
}
func (s *budgetState) expire(now time.Time) {
	if s.config.Keys == nil {
		return
	}
	for n := 0; n < 64; n++ {
		front := s.idle.Front()
		if front == nil {
			return
		}
		k := front.Value.(*keyEntry)
		if now.Sub(k.idleAt) < s.config.Keys.IdleTTL {
			return
		}
		s.idle.Remove(front)
		delete(s.keys, k.hash)
		s.expired++
	}
}
func (e *Engine) NeedsCost(resource string) bool {
	for _, s := range e.budgets {
		if s.config.Resource == "" || s.config.Resource == resource {
			return true
		}
	}
	return false
}

// AcquireResources combines all matching byte/key checks atomically under the
// engine lock. No partial reservation is retained when enforcement rejects.
// Observe mode tracks actual occupancy in bounded fallback buckets while recording
// proposals, including registry overflow; it does not create unbounded keys.
func (e *Engine) AcquireResources(target Target, cost Cost, now time.Time) (*Lease, []BudgetAssessment, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var assessments []BudgetAssessment
	var pending []reservation
	reason := ""
	invalid := ""
	if cost.WriteBytes < 0 || cost.WriteBytes > 1<<40 || !validKey(cost.Key) {
		return nil, nil, "resource_metadata_invalid"
	}
	hash := sha256.Sum256([]byte(cost.Key))
	for i := range e.budgets {
		s := &e.budgets[i]
		b := s.config
		if b.Resource != "" && b.Resource != target.Resource {
			continue
		}
		s.expire(now)
		s.attempts++
		a := BudgetAssessment{Name: b.Name, Action: "allow", WriteBytes: cost.WriteBytes, ActiveWriteBytes: s.bytes, MaxWriteBytes: b.MaxWriteBytes}
		r := reservation{index: i}
		if b.MaxWriteBytes > 0 {
			r.bytes = cost.WriteBytes
			if cost.WriteBytes == 0 {
				a.Reason = "resource_cost_missing"
				invalid = a.Reason
			}
			if cost.WriteBytes > b.MaxWriteBytes-s.bytes {
				a.Reason = "write_bytes_limit"
			}
			if cost.WriteBytes > math.MaxInt64-s.bytes {
				invalid = "resource_accounting_exhausted"
				a.Reason = invalid
			}
		}
		if b.Keys != nil {
			k := s.keys[hash]
			a.KeyLimit = b.Keys.MaxConcurrent
			if cost.Key == "" {
				a.KeyBucket = "missing"
				a.KeyActive = s.missing
				if b.Keys.Missing == "reject" && a.Reason == "" {
					a.Reason = "resource_key_missing"
				}
			}
			if cost.Key != "" && k == nil && len(s.keys) >= b.Keys.MaxTracked {
				a.KeyBucket = "overflow"
				a.KeyActive = s.overflow
				if b.Keys.Overflow == "reject" && a.Reason == "" {
					a.Reason = "resource_key_registry_full"
				}
			}
			if a.KeyBucket != "" {
				r.bucket = a.KeyBucket
			} else {
				a.KeyBucket = "identified"
				r.key = k
				if k != nil {
					a.KeyActive = k.active
				}
			}
			if a.KeyActive >= a.KeyLimit && a.Reason == "" {
				a.Reason = "hot_key_limit"
			}
		}
		if a.Reason != "" {
			a.Action = "reject"
			s.wouldReject++
			if reason == "" {
				reason = a.Reason
			}
		}
		assessments = append(assessments, a)
		pending = append(pending, r)
	}
	if invalid != "" {
		reason = invalid
	}
	if invalid != "" || e.mode == Enforce && reason != "" {
		for _, r := range pending {
			e.budgets[r.index].rejected++
		}
		return nil, assessments, reason
	}
	for n := range pending {
		r := &pending[n]
		s := &e.budgets[r.index]
		s.bytes += r.bytes
		s.active++
		if s.config.Keys != nil && r.bucket == "" {
			if r.key == nil {
				r.key = &keyEntry{hash: hash}
				s.keys[hash] = r.key
			}
			if r.key.idle != nil {
				s.idle.Remove(r.key.idle)
				r.key.idle = nil
			}
			r.key.active++
		}
		if r.bucket == "missing" {
			s.missing++
		}
		if r.bucket == "overflow" {
			s.overflow++
		}
	}
	return &Lease{engine: e, reservations: pending}, assessments, ""
}
func (e *Engine) BudgetSnapshot() []BudgetSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]BudgetSnapshot, 0, len(e.budgets))
	for i := range e.budgets {
		s := &e.budgets[i]
		s.expire(time.Now())
		b := s.config
		v := BudgetSnapshot{Name: b.Name, Resource: b.Resource, MaxWriteBytes: b.MaxWriteBytes, ActiveWriteBytes: s.bytes, Active: s.active, Tracked: len(s.keys), MissingActive: s.missing, OverflowActive: s.overflow, Expired: s.expired, Attempts: s.attempts, WouldReject: s.wouldReject, Rejected: s.rejected, Admitted: s.admitted}
		if b.Keys != nil {
			v.TrackingLimit = b.Keys.MaxTracked
			v.KeyLimit = b.Keys.MaxConcurrent
		}
		out = append(out, v)
	}
	return out
}

// HasBudgets reports whether HTTP integration requires trusted operation metadata.
func (e *Engine) HasBudgets() bool { return len(e.budgets) > 0 }
