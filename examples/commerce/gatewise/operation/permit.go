package operation

import (
	"context"
	"errors"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/tenancy"
	"sync"
	"time"
)

// Request metadata must come from a validated operation plan/authenticated
// context. Priority defaults to Critical. Resource names are configured by the
// server, not selected by an untrusted caller.
type Request struct {
	// NoWait opts this operation out of waiting while preserving queue ordering.
	NoWait           bool
	Kind             string // operation (default) or http; bounded diagnostic classification.
	ResolveCost      func(context.Context) (decision.Cost, error)
	Priority         Priority
	Tenant, Resource string
	Cost             decision.Cost
	LatencyTarget    time.Duration
}
type Outcome struct {
	Err                error
	StatusCode         int
	Canceled, Panicked bool
}
type Rejection struct {
	Reason   string
	Input    bool
	Decision decision.Result
}

func (r *Rejection) Error() string { return "gatewise: " + r.Reason }

var ErrUntrustedCost = errors.New("gatewise: untrusted operation metadata")

var ErrNested = errors.New("gatewise: nested admission on the same engine; use a separate budget")

type activeKey struct{}
type active struct {
	engine *Engine
	parent *active
	live   bool
	mu     sync.Mutex
}
type Permit struct {
	engine     *Engine
	ctx        context.Context
	request    Request
	result     decision.Result
	generation uint64
	started    time.Time
	tenant     *tenancy.Lease
	queueWait  time.Duration
	resources  *decision.Lease
	active     *active
	once       sync.Once
}

// QueueWait is admission waiting only; processing feedback excludes this time.
func (p *Permit) QueueWait() time.Duration  { return p.queueWait }
func (p *Permit) Cost() decision.Cost       { return p.request.Cost }
func (p *Permit) Context() context.Context  { return p.ctx }
func (p *Permit) Decision() decision.Result { return p.result.Clone() }
func (p *Permit) TenantAllocation() (kind string, share, ceiling int) {
	if p.tenant == nil {
		return "", 0, 0
	}
	return p.tenant.Allocation(), p.tenant.Share(), p.tenant.Ceiling()
}
func (p *Permit) Finish(outcome Outcome) {
	p.once.Do(func() {
		s := p.engine
		elapsed := time.Since(p.started)
		canceled := outcome.Canceled || p.ctx.Err() != nil
		priority := admission.Priority(p.request.Priority)
		if s.controller != nil {
			change := s.controller.End(priority, p.generation, time.Now(), elapsed, p.request.LatencyTarget, outcome.Err != nil || outcome.Panicked || outcome.StatusCode >= 500, canceled)
			if change != nil {
				s.statistics.adjustment(change.Previous, change.Limit, change.Reason)
				if s.logger != nil {
					s.logger.Info("concurrency limit adjusted", "service", s.name, "previous", change.Previous, "limit", change.Limit, "reason", change.Reason, "p90_ratio", change.P90Ratio, "control_ratio", change.ControlRatio, "control_priority", change.ControlPriority, "control_failure_fraction", change.ControlFailureFraction, "profile", change.Profile, "samples", change.Samples)
				}
			}
		} else {
			s.limiter.Release(priority)
		}
		if p.tenant != nil {
			p.tenant.Release(time.Now())
		}
		if p.resources != nil {
			p.resources.Release()
		}
		p.active.mu.Lock()
		p.active.live = false
		p.active.mu.Unlock()
		s.statistics.complete(p.request.Priority, outcome.StatusCode, !outcome.Panicked, canceled, elapsed)
		s.statistics.workComplete(p.request.Kind, outcome, canceled, elapsed)
		if s.queue != nil {
			s.queue.wake()
		}
	})
}

// Do reports failures/panics and releases after fn actually returns, even if fn
// ignores cancellation. The panic propagates after accounting is released.
func (s *Engine) Do(ctx context.Context, request Request, fn func(context.Context) error) (err error) {
	if fn == nil {
		return fmt.Errorf("gatewise: nil operation")
	}
	permit, err := s.Begin(ctx, request)
	if err != nil {
		return err
	}
	completed := false
	defer func() { permit.Finish(Outcome{Err: err, Panicked: !completed}) }()
	err = fn(permit.Context())
	completed = true
	return err
}

// Reject records an adapter identity failure before Begin; later stages remain unvisited.
func (s *Engine) Reject(priority Priority, reason string, stage string) decision.Result {
	if s.priorities != nil {
		priority = Priority(s.priorities.Map(string(priority)))
	}
	if s.priorities != nil && (reason == "priority_metadata_invalid" || reason == "priority_metadata_untrusted") {
		s.statistics.resolution(priority, "invalid")
	}
	trace := decision.NewResult(string(priority), "")
	trace.Kind = "http"
	s.statistics.workAdmission("http", false)
	trace.Stage = stage
	trace.Reason = reason
	trace.Mark(stage, reason)
	s.statistics.admission(priority, admission.Decision(reason))
	if s.decisions != nil {
		s.recordDecision(trace)
	}
	return trace
}

// NeedsCost reports whether a server-owned resource matches accounting rules.
func (s *Engine) NeedsCost(resource string) bool {
	return s.decisions != nil && s.decisions.NeedsCost(resource)
}
