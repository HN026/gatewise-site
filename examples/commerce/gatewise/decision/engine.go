// Package decision combines cached overload observations into bounded admission
// proposals. It never samples a provider or makes a network call during Evaluate.
package decision

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sync"
	"time"
)

type Mode string

const (
	Enforce Mode = "enforce"
	Observe Mode = "observe"
)

type Scope struct {
	Kind string `json:"kind"`
	Key  string `json:"key,omitempty"`
}
type Effect struct {
	MaxConcurrent int  `json:"max_concurrent,omitempty"`
	Reject        bool `json:"reject,omitempty"`
}
type Rule struct {
	Name         string
	Scope        Scope
	Threshold    float64
	MaxAge       time.Duration
	OnUnknown    string // allow (default), reject, or limit.
	UnknownLimit int
	Pressure     Effect
}
type Config struct {
	Mode    Mode
	Rules   []Rule
	Budgets []Budget
}

// Observation timestamps describe measurement time, not delivery time. Unknown
// explicitly invalidates an earlier value. Duplicate timestamps never renew age.
type Observation struct {
	Value      float64   `json:"value"`
	ObservedAt time.Time `json:"observed_at"`
	State      string    `json:"state"`
}
type Target struct{ Resource, Actor string }
type Assessment struct {
	Name             string
	Scope            Scope
	State            string // missing, healthy, pressure, unknown, stale
	Value, Threshold float64
	ObservedAt       time.Time
	MaxAge           time.Duration
	Action           string // allow, limit, reject
	Limit            int
	Cause            string // pressure, unavailable, or empty
	Binding          bool
}
type Evaluation struct {
	Mode                                         Mode
	BaselineLimit, ProposedLimit, EffectiveLimit int
	SignalCeiling                                int // Smallest matching raw cap; independent of a moving adaptive baseline.
	Action, Reason                               string
	Reject                                       bool // Enforced explicit rejection. A cap is checked atomically later.
	Assessments                                  []Assessment
}
type Stage struct{ Name, State, Reason string }
type Result struct {
	Allowed                           bool
	Priority, Resource, Reason, Stage string
	Evaluation                        Evaluation
	Stages                            []Stage
	Budgets                           []BudgetAssessment
}

func (r Result) Clone() Result {
	r.Budgets = append([]BudgetAssessment(nil), r.Budgets...)
	r.Stages = append([]Stage(nil), r.Stages...)
	r.Evaluation.Assessments = append([]Assessment(nil), r.Evaluation.Assessments...)
	return r
}

type resultKey struct{}

func WithResult(ctx context.Context, r Result) context.Context {
	return context.WithValue(ctx, resultKey{}, r.Clone())
}
func FromContext(ctx context.Context) (Result, bool) {
	r, ok := ctx.Value(resultKey{}).(Result)
	return r.Clone(), ok
}
func NewResult(priority, resource string) Result {
	return Result{Priority: priority, Resource: resource, Stages: []Stage{{Name: "identity", State: "not_evaluated"}, {Name: "signals", State: "not_evaluated"}, {Name: "resources", State: "not_evaluated"}, {Name: "tenant", State: "not_evaluated"}, {Name: "capacity", State: "not_evaluated"}}}
}
func (r *Result) Mark(stage, reason string) {
	for i := range r.Stages {
		if r.Stages[i].Name == stage {
			r.Stages[i].State = "passed"
			r.Stages[i].Reason = reason
			if reason != "" {
				r.Stages[i].State = "rejected"
			}
			return
		}
	}
}

var token = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func ValidName(s string) bool { return token.MatchString(s) }

var ErrOutOfOrder = errors.New("decision: out-of-order observation")
var ErrInvalidObservation = errors.New("decision: invalid observation")
var ErrFutureObservation = errors.New("decision: future observation")

type slot struct {
	rule                Rule
	actor               [32]byte
	observation         Observation
	present, collecting bool
	accepted            uint64
	dropped             [3]uint64 // invalid, out_of_order, future
	failures            [4]uint64 // error, timeout, panic, busy
}
type SignalSnapshot struct {
	Assessment
	Accepted   uint64
	Dropped    [3]uint64
	Failures   [4]uint64
	Collecting bool
}
type Engine struct {
	mu      sync.Mutex
	mode    Mode
	budgets []budgetState
	slots   []slot
	indices map[string]int
	running bool
}

func New(c Config) (*Engine, error) {
	if c.Mode == "" {
		c.Mode = Enforce
	}
	if c.Mode != Enforce && c.Mode != Observe {
		return nil, fmt.Errorf("decision: mode must be enforce or observe")
	}
	if len(c.Rules) == 0 && len(c.Budgets) == 0 || len(c.Rules) > 32 {
		return nil, fmt.Errorf("decision: configure signals or resource budgets; at most 32 signals")
	}
	e := &Engine{mode: c.Mode, indices: make(map[string]int, len(c.Rules))}
	if err := e.initBudgets(c.Budgets); err != nil {
		return nil, err
	}
	for _, r := range c.Rules {
		if !ValidName(r.Name) {
			return nil, fmt.Errorf("decision: invalid signal name")
		}
		if _, ok := e.indices[r.Name]; ok {
			return nil, fmt.Errorf("decision: duplicate signal name")
		}
		if math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0) || r.Threshold < 0 || r.MaxAge <= 0 || r.MaxAge > 24*time.Hour {
			return nil, fmt.Errorf("decision: threshold must be finite/nonnegative; max age must be positive and at most 24h")
		}
		if r.Scope.Kind == "" {
			r.Scope.Kind = "service"
		}
		if r.Scope.Kind == "service" && r.Scope.Key != "" || r.Scope.Kind == "resource" && !ValidName(r.Scope.Key) || r.Scope.Kind == "actor" && (len(r.Scope.Key) < 1 || len(r.Scope.Key) > 128) || r.Scope.Kind != "service" && r.Scope.Kind != "resource" && r.Scope.Kind != "actor" {
			return nil, fmt.Errorf("decision: invalid signal scope")
		}
		if r.OnUnknown == "" {
			r.OnUnknown = "allow"
		}
		if r.OnUnknown != "allow" && r.OnUnknown != "reject" && r.OnUnknown != "limit" || r.OnUnknown == "limit" && r.UnknownLimit <= 0 || r.OnUnknown != "limit" && r.UnknownLimit != 0 {
			return nil, fmt.Errorf("decision: invalid unknown policy/limit")
		}
		if r.Pressure.MaxConcurrent < 0 || r.Pressure.Reject == (r.Pressure.MaxConcurrent > 0) {
			return nil, fmt.Errorf("decision: pressure needs exactly one positive cap or reject")
		}
		s := slot{rule: r}
		if r.Scope.Kind == "actor" {
			s.actor = sha256.Sum256([]byte(r.Scope.Key))
			s.rule.Scope.Key = ""
		}
		e.indices[r.Name] = len(e.slots)
		e.slots = append(e.slots, s)
	}
	return e, nil
}
func (e *Engine) Mode() Mode { return e.mode }
func (e *Engine) HasActorRules() bool {
	for _, s := range e.slots {
		if s.rule.Scope.Kind == "actor" {
			return true
		}
	}
	return false
}
func (e *Engine) Publish(name string, o Observation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	i, ok := e.indices[name]
	if !ok {
		return fmt.Errorf("decision: unknown signal")
	}
	s := &e.slots[i]
	if o.State != "measured" && o.State != "unknown" || o.ObservedAt.IsZero() || math.IsNaN(o.Value) || math.IsInf(o.Value, 0) || o.Value < 0 {
		s.dropped[0]++
		return ErrInvalidObservation
	}
	if o.ObservedAt.After(time.Now()) {
		s.dropped[2]++
		return ErrFutureObservation
	}
	if s.present && !o.ObservedAt.After(s.observation.ObservedAt) {
		if o == s.observation {
			return nil
		}
		s.dropped[1]++
		return ErrOutOfOrder
	}
	s.observation = o
	s.present = true
	s.accepted++
	return nil
}

// Unavailable publishes a current unknown state, never the provider's error text.
func (e *Engine) Unavailable(name string) error {
	return e.Publish(name, Observation{ObservedAt: time.Now(), State: "unknown"})
}
func assess(s *slot, now time.Time) Assessment {
	a := Assessment{Name: s.rule.Name, Scope: s.rule.Scope, Threshold: s.rule.Threshold, MaxAge: s.rule.MaxAge, Action: "allow", State: "missing"}
	if s.present {
		a.ObservedAt = s.observation.ObservedAt
		a.Value = s.observation.Value
		a.State = "healthy"
		if s.observation.State == "unknown" {
			a.State = "unknown"
		} else if now.Before(a.ObservedAt) || now.Sub(a.ObservedAt) > s.rule.MaxAge {
			a.State = "stale"
		} else if a.Value >= s.rule.Threshold {
			a.State = "pressure"
		}
	}
	if a.State == "pressure" {
		a.Cause = "pressure"
		if s.rule.Pressure.Reject {
			a.Action = "reject"
		} else {
			a.Action = "limit"
			a.Limit = s.rule.Pressure.MaxConcurrent
		}
	}
	if a.State == "missing" || a.State == "unknown" || a.State == "stale" {
		a.Cause = "unavailable"
		a.Action = s.rule.OnUnknown
		if a.Action == "limit" {
			a.Limit = s.rule.UnknownLimit
		}
	}
	return a
}
func (e *Engine) Evaluate(target Target, baseline int, now time.Time) Evaluation {
	result := Evaluation{Mode: e.mode, BaselineLimit: baseline, ProposedLimit: baseline, EffectiveLimit: baseline, Action: "allow", Assessments: make([]Assessment, 0, len(e.slots))}
	var actor [32]byte
	if len(target.Actor) > 0 && len(target.Actor) <= 128 {
		actor = sha256.Sum256([]byte(target.Actor))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.slots {
		s := &e.slots[i]
		if s.rule.Scope.Kind == "resource" && s.rule.Scope.Key != target.Resource || s.rule.Scope.Kind == "actor" && (len(target.Actor) == 0 || len(target.Actor) > 128 || s.actor != actor) {
			continue
		}
		a := assess(s, now)
		result.Assessments = append(result.Assessments, a)
		if a.Action == "reject" && result.Action != "reject" {
			result.Action = "reject"
			result.Reason = "signal_pressure"
			if a.Cause == "unavailable" {
				result.Reason = "signal_unavailable"
			}
		}
		if a.Action == "limit" && (result.SignalCeiling == 0 || a.Limit < result.SignalCeiling) {
			result.SignalCeiling = a.Limit
		}
		if a.Action == "limit" && a.Limit < result.ProposedLimit {
			result.ProposedLimit = a.Limit
		}
	}
	if result.Action != "reject" && result.ProposedLimit < baseline {
		result.Action = "limit"
		result.Reason = "signal_concurrency_limit"
	}
	for i := range result.Assessments {
		a := &result.Assessments[i]
		a.Binding = a.Action == "reject" && result.Action == "reject" || result.Action == "limit" && a.Action == "limit" && a.Limit == result.ProposedLimit
	}
	if e.mode == Enforce {
		result.EffectiveLimit = result.ProposedLimit
		result.Reject = result.Action == "reject"
	}
	return result
}
func (e *Engine) Snapshot() []SignalSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]SignalSnapshot, 0, len(e.slots))
	for i := range e.slots {
		s := &e.slots[i]
		out = append(out, SignalSnapshot{Assessment: assess(s, time.Now()), Accepted: s.accepted, Dropped: s.dropped, Failures: s.failures, Collecting: s.collecting})
	}
	return out
}
