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
	resources  *decision.Lease
	active     *active
	once       sync.Once
}

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
	})
}

// Begin never queues. Nested admission through an active Permit.Context on the
// same engine is rejected; independent HTTP/database budgets use distinct engines.
func (s *Engine) Begin(ctx context.Context, request Request) (*Permit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for a, _ := ctx.Value(activeKey{}).(*active); a != nil; a = a.parent {
		a.mu.Lock()
		live := a.live
		a.mu.Unlock()
		if live && a.engine == s {
			return nil, ErrNested
		}
	}
	if request.Kind == "" {
		request.Kind = "operation"
	}
	if request.Kind != "operation" && request.Kind != "http" {
		return nil, fmt.Errorf("gatewise: invalid work kind")
	}
	if request.Priority == "" {
		request.Priority = Critical
	}
	if request.LatencyTarget < 0 {
		return nil, fmt.Errorf("gatewise: negative operation latency target")
	}
	if request.LatencyTarget == 0 {
		request.LatencyTarget = s.adaptive.TargetLatency
	}
	if request.Resource != "" && !decision.ValidName(request.Resource) {
		return nil, fmt.Errorf("gatewise: invalid operation resource")
	}
	trace := decision.NewResult(string(request.Priority), request.Resource)
	trace.Kind = request.Kind
	stage := "identity"
	bucket, buckets := -1, 0
	fail := func(reason admission.Decision, input bool) (*Permit, error) {
		trace.Priority = string(request.Priority)
		trace.Reason = string(reason)
		trace.Stage = stage
		trace.Mark(stage, string(reason))
		s.statistics.admission(request.Priority, reason)
		s.statistics.workAdmission(request.Kind, false)
		if s.decisions != nil {
			s.recordDecision(trace)
		}
		return nil, &Rejection{Reason: string(reason), Input: input, Decision: trace.Clone()}
	}
	if s.priorities != nil {
		resolved, m, err := s.priorities.ResolveContext(ctx, string(request.Priority), time.Now())
		request.Priority = Priority(s.priorities.Map(string(request.Priority)))
		if err != nil {
			s.statistics.resolution(request.Priority, "invalid")
			return fail(admission.PriorityInvalid, true)
		}
		ctx = resolved
		request.Priority = Priority(m.Tier)
		bucket = m.Bucket
		buckets = s.priorities.Buckets()
		s.statistics.resolution(request.Priority, m.Source)
	} else if request.Priority != Critical && request.Priority != Background {
		return nil, fmt.Errorf("gatewise: invalid operation priority")
	}
	if request.Tenant != "" && !tenancy.ValidID(request.Tenant) {
		return fail(admission.TenantInvalid, true)
	}
	trace.Priority = string(request.Priority)
	trace.Mark("identity", "")
	stage = "signals"
	ceiling := 0
	if s.decisions != nil {
		trace.Evaluation = s.decisions.Evaluate(decision.Target{Actor: request.Tenant, Resource: request.Resource}, s.limiter.Snapshot().Limit, time.Now())
		if trace.Evaluation.Mode == decision.Enforce {
			ceiling = trace.Evaluation.SignalCeiling
		}
		if trace.Evaluation.Reject {
			return fail(admission.Decision(trace.Evaluation.Reason), false)
		}
	}
	trace.Mark("signals", "")
	stage = "resources"
	var resources *decision.Lease
	if s.decisions != nil && s.decisions.NeedsCost(request.Resource) {
		if request.ResolveCost != nil {
			var err error
			request.Cost, err = request.ResolveCost(ctx)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				reason := admission.ResourceMetadataInvalid
				if errors.Is(err, ErrUntrustedCost) {
					reason = admission.ResourceMetadataUntrusted
				}
				return fail(reason, true)
			}
		}
		var reason string
		resources, trace.Budgets, reason = s.decisions.AcquireResources(decision.Target{Actor: request.Tenant, Resource: request.Resource}, request.Cost, time.Now())
		if reason != "" {
			return fail(admission.Decision(reason), reason == "resource_metadata_invalid" || reason == "resource_cost_missing" || reason == "resource_key_missing")
		}
	}
	rollback := true
	var tenant *tenancy.Lease
	defer func() {
		if rollback {
			if resources != nil {
				resources.Release()
			}
			if tenant != nil {
				tenant.Release(time.Now())
			}
		}
	}()
	trace.Mark("resources", "")
	stage = "tenant"
	if s.tenants != nil {
		capacity := 0
		if s.tenants.SharingEnabled() {
			capacity = s.limiter.Snapshot().Limit
			if ceiling > 0 && ceiling < capacity {
				capacity = ceiling
			}
		}
		var reason admission.Decision
		tenant, reason = s.tenants.AcquireWithLimit(request.Tenant, time.Now(), capacity)
		if reason != admission.Admitted {
			return fail(reason, reason == admission.TenantMissing || reason == admission.TenantInvalid)
		}
	}
	trace.Mark("tenant", "")
	stage = "capacity"
	started := time.Now()
	var reason admission.Decision
	var generation uint64
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.controller != nil {
		reason, generation = s.controller.BeginBudget(admission.Priority(request.Priority), started, bucket, buckets, ceiling)
	} else {
		reason = s.limiter.TryAcquireBudget(admission.Priority(request.Priority), bucket, buckets, ceiling)
	}
	if reason != admission.Admitted {
		if tenant != nil {
			tenant.Rejected(reason, time.Now())
		}
		return fail(reason, false)
	}
	if tenant != nil {
		tenant.Retain()
		if s.tenants.SharingEnabled() {
			s.statistics.tenantAllocation(tenant.Allocation())
		}
	}
	if resources != nil {
		resources.Retain()
	}
	trace.Mark("capacity", "")
	trace.Allowed = true
	trace.Stage = "admitted"
	if s.decisions != nil {
		s.recordDecision(trace)
	}
	s.statistics.admission(request.Priority, admission.Admitted)
	s.statistics.workAdmission(request.Kind, true)
	parent, _ := ctx.Value(activeKey{}).(*active)
	marker := &active{engine: s, parent: parent, live: true}
	ctx = context.WithValue(ctx, activeKey{}, marker)
	ctx = decision.WithResult(ctx, trace)
	rollback = false
	return &Permit{engine: s, ctx: ctx, request: request, result: trace, generation: generation, started: started, tenant: tenant, resources: resources, active: marker}, nil
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
