package operation

import (
	"context"
	"errors"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/tenancy"
	"time"
)

type prepared struct {
	ctx             context.Context
	request         Request
	bucket, buckets int
}

func (s *Engine) prepare(ctx context.Context, request Request) (*prepared, error) {
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
	fail := func(reason admission.Decision, input bool) (*prepared, error) {
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

	trace.Mark("identity", "")
	stage = "signals"
	if s.decisions != nil {
		trace.Evaluation = s.decisions.Evaluate(decision.Target{Actor: request.Tenant, Resource: request.Resource}, s.limiter.Snapshot().Limit, time.Now())
		if trace.Evaluation.Reject {
			return fail(admission.Decision(trace.Evaluation.Reason), false)
		}
	}
	trace.Mark("signals", "")
	stage = "resources"
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
	}
	return &prepared{ctx: ctx, request: request, bucket: bucket, buckets: buckets}, nil
}

// Begin waits only when Queue is configured. No execution/resource/tenant permit
// is retained while waiting. A successful Permit must still be explicitly finished.
func (s *Engine) Begin(ctx context.Context, request Request) (*Permit, error) {
	p, err := s.prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	if s.queue == nil {
		return s.admit(p, true)
	}
	return s.wait(p)
}

func (s *Engine) recordAdmissionFailure(request Request, trace decision.Result, reason admission.Decision) {
	s.statistics.admission(request.Priority, reason)
	s.statistics.workAdmission(request.Kind, false)
	if s.decisions != nil {
		s.recordDecision(trace)
	}
}

func (s *Engine) admit(p *prepared, record bool) (*Permit, error) { return s.attempt(p, record, true) }

func (s *Engine) attempt(p *prepared, record, capacityAllowed bool) (*Permit, error) {
	ctx, request, bucket, buckets := p.ctx, p.request, p.bucket, p.buckets
	trace := decision.NewResult(string(request.Priority), request.Resource)
	trace.Kind = request.Kind
	stage := "identity"
	fail := func(reason admission.Decision, input bool) (*Permit, error) {
		trace.Priority = string(request.Priority)
		trace.Reason = string(reason)
		trace.Stage = stage
		trace.Mark(stage, string(reason))
		if record {
			s.recordAdmissionFailure(request, trace, reason)
		}
		return nil, &Rejection{Reason: string(reason), Input: input, Decision: trace.Clone()}
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
	if !capacityAllowed {
		return fail(admission.ConcurrencyLimit, false)
	}
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
