// Package middleware provides HTTP admission control for Go services. A shared
// Service applies one total limit and critical reservation across its handlers.
// It does not start servers, read environment files, or install global handlers.
package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	decisionpkg "github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/adaptive"
	"github.com/HN026/gatewise/internal/tenancy"
	prioritypkg "github.com/HN026/gatewise/priority"
	"github.com/felixge/httpsnoop"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/HN026/gatewise/internal/admission"
)

type Priority string

const (
	Critical   Priority = "critical"
	Background Priority = "background"
)

type ServiceConfig struct {
	// Decisions is optional. The caller owns observation collection and lifecycle.
	Decisions *decisionpkg.Engine
	// Name identifies this service in rejection responses and logs; default "service".
	Name string
	// MaxConcurrent is required and must be positive.
	MaxConcurrent int
	// ReservedCritical ranges from zero through MaxConcurrent, inclusive.
	ReservedCritical int
	// Logger optionally records rejections. Nil disables library logging.
	Logger *slog.Logger
	// Adaptive nil retains fixed mode.
	Adaptive *AdaptiveConfig
	// Tenants nil preserves service-only admission.
	Tenants    *TenantConfig
	Priorities *prioritypkg.Config
}

// Service must be constructed with NewService; its zero value is not usable.
// It is safe for concurrent use. Reuse one instance for handlers sharing
// capacity; construct separate instances for independent service budgets.
type Service struct {
	decisions      *decisionpkg.Engine
	name           string
	limiter        *admission.Limiter
	logger         *slog.Logger
	maximum        int
	adaptive       AdaptiveConfig
	controller     *adaptive.Controller
	statistics     *statistics
	tenants        *tenancy.Limiter
	tenantResolver TenantResolver
	priorities     *prioritypkg.Policy
}

func NewService(c ServiceConfig) (*Service, error) {
	if c.MaxConcurrent <= 0 {
		return nil, fmt.Errorf("gatewise: MaxConcurrent must be positive")
	}
	if c.ReservedCritical < 0 || c.ReservedCritical > c.MaxConcurrent {
		return nil, fmt.Errorf("gatewise: ReservedCritical must be between zero and MaxConcurrent")
	}
	if c.Name == "" {
		c.Name = "service"
	}
	s := &Service{name: c.Name, maximum: c.MaxConcurrent, logger: c.Logger, statistics: newStatistics()}
	var err error
	s.tenants, s.tenantResolver, err = newTenants(c.Tenants, c.MaxConcurrent)
	if err != nil {
		return nil, err
	}
	s.decisions = c.Decisions
	if s.decisions != nil && s.decisions.HasActorRules() && s.tenantResolver == nil {
		return nil, fmt.Errorf("gatewise: actor rules require a trusted tenant resolver")
	}
	if c.Priorities != nil {
		if c.ReservedCritical != 0 {
			return nil, fmt.Errorf("gatewise: use tier reservations instead of ReservedCritical with Priorities")
		}
		s.priorities, err = prioritypkg.New(*c.Priorities, c.MaxConcurrent)
		if err != nil {
			return nil, err
		}
		for _, tier := range s.priorities.Tiers() {
			c.ReservedCritical += tier.Reserved
		}
		s.statistics = newTierStatistics(s.priorities.Tiers())
	}
	initial := c.MaxConcurrent
	if c.Adaptive != nil {
		a, err := normalizeAdaptive(c)
		if err != nil {
			return nil, err
		}
		s.adaptive = a
		initial = a.InitialConcurrent
	}
	s.limiter = admission.New(initial, c.ReservedCritical)
	if s.priorities != nil {
		tiers := []admission.Tier{}
		for _, t := range s.priorities.Tiers() {
			tiers = append(tiers, admission.Tier{Name: admission.Priority(t.Name), Reserved: t.Reserved})
		}
		s.limiter = admission.NewTiers(initial, tiers)
	}
	if c.Adaptive != nil {
		s.controller = adaptive.New(s.limiter, adaptive.Config{Min: s.adaptive.MinConcurrent, Max: c.MaxConcurrent, Interval: s.adaptive.Interval, MinSamples: s.adaptive.MinSamples})
	}
	return s, nil
}

// Middleware wraps next with a route-configured priority. Invalid priorities or
// nil handlers panic at registration time. Without Priorities, caller headers
// cannot change priority. Explicit tier policies resolve trusted metadata first.
// The slot is held until next returns (or panics); work outliving the handler is
// outside that lifetime. Cancellation is forwarded through the request context.
// There is no waiting queue, preemption, automatic retry, or process-wide budget.
func (s *Service) Middleware(priority Priority, next http.Handler, options ...HandlerOption) http.Handler {
	if s.priorities == nil && priority != Critical && priority != Background || s.priorities != nil && !s.priorities.Valid(string(priority)) {
		panic("gatewise: priority must be a configured tier or compatibility priority")
	}
	if next == nil {
		panic("gatewise: handler must not be nil")
	}
	o := handlerOptions{targetLatency: s.adaptive.TargetLatency}
	for _, option := range options {
		if option == nil {
			panic("gatewise: nil handler option")
		}
		option(&o)
	}
	routePriority := priority
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		priority := routePriority
		var trace decisionpkg.Result
		if s.decisions != nil {
			trace = decisionpkg.NewResult(string(priority), o.resource)
		}
		stage := "identity"
		fail := func(reason admission.Decision, status int) {
			if s.decisions != nil {
				trace.Priority = string(priority)
				trace.Reason = string(reason)
				trace.Stage = stage
				trace.Mark(stage, string(reason))
				s.recordDecision(trace)
			}
			s.reject(w, priority, reason, status, trace)
		}
		bucket, buckets := -1, 0
		if s.priorities != nil {
			resolved, m, err := s.priorities.Resolve(r, string(routePriority), time.Now())
			priority = Priority(s.priorities.Map(string(routePriority)))
			if err != nil {
				status, decision := 400, admission.PriorityInvalid
				if errors.Is(err, prioritypkg.ErrUntrusted) {
					status, decision = 403, admission.PriorityUntrusted
				}
				s.statistics.resolution(priority, "invalid")
				fail(decision, status)
				return
			}
			r = resolved
			priority = Priority(m.Tier)
			bucket = m.Bucket
			buckets = s.priorities.Buckets()
			s.statistics.resolution(priority, m.Source)
			w.Header().Set("X-Gatewise-Resolved-Priority", m.Tier)
			w.Header().Set("X-Gatewise-Priority-Source", m.Source)
		}
		p := admission.Priority(priority)
		var tenantLease *tenancy.Lease
		var id string
		if s.tenants != nil {
			var err error
			id, err = s.tenantResolver(r)
			if r.Context().Err() != nil {
				return
			}
			if err != nil {
				decision := admission.TenantInvalid
				status := 400
				if errors.Is(err, ErrUntrustedTenantSource) {
					decision = admission.TenantUntrusted
					status = 403
				}
				fail(decision, status)
				return
			}
		}
		if id != "" && !tenancy.ValidID(id) {
			fail(admission.TenantInvalid, 400)
			return
		}
		trace.Priority = string(priority)
		trace.Mark("identity", "")
		stage = "signals"
		ceiling := 0
		if s.decisions != nil {
			trace.Evaluation = s.decisions.Evaluate(decisionpkg.Target{Actor: id, Resource: o.resource}, s.limiter.Snapshot().Limit, time.Now())
			if trace.Evaluation.Mode == decisionpkg.Enforce {
				ceiling = trace.Evaluation.SignalCeiling
			}
			w.Header().Set("X-Gatewise-Decision-Mode", string(trace.Evaluation.Mode))
			w.Header().Set("X-Gatewise-Signal-Proposal", trace.Evaluation.Action)
			w.Header().Set("X-Gatewise-Proposed-Limit", strconv.Itoa(trace.Evaluation.ProposedLimit))
			w.Header().Set("X-Gatewise-Effective-Limit", strconv.Itoa(trace.Evaluation.EffectiveLimit))
			if trace.Evaluation.Reject {
				fail(admission.Decision(trace.Evaluation.Reason), 503)
				return
			}
		}
		trace.Mark("signals", "")
		stage = "tenant"
		if s.tenants != nil {
			capacity := 0
			if s.tenants.SharingEnabled() {
				capacity = s.limiter.Snapshot().Limit
				if ceiling > 0 && ceiling < capacity {
					capacity = ceiling
				}
			}
			lease, decision := s.tenants.AcquireWithLimit(id, time.Now(), capacity)
			if decision != admission.Admitted {
				status := 503
				if decision == admission.TenantMissing || decision == admission.TenantInvalid {
					status = 400
				}
				fail(decision, status)
				return
			}
			tenantLease = lease
			defer func() { lease.Release(time.Now()) }()
		}
		trace.Mark("tenant", "")
		stage = "capacity"
		start := time.Now()
		var decision admission.Decision
		var generation uint64
		if s.controller != nil {
			decision, generation = s.controller.BeginBudget(p, start, bucket, buckets, ceiling)
		} else {
			decision = s.limiter.TryAcquireBudget(p, bucket, buckets, ceiling)
		}
		if decision != admission.Admitted {
			if tenantLease != nil {
				tenantLease.Rejected(decision, time.Now())
			}
			fail(decision, 503)
			return
		}
		if tenantLease != nil {
			tenantLease.Retain()
			if s.tenants.SharingEnabled() {
				s.statistics.tenantAllocation(tenantLease.Allocation())
				w.Header().Set("X-Gatewise-Tenant-Admission", tenantLease.Allocation())
				w.Header().Set("X-Gatewise-Tenant-Share", strconv.Itoa(tenantLease.Share()))
				w.Header().Set("X-Gatewise-Tenant-Ceiling", strconv.Itoa(tenantLease.Ceiling()))
			}
		}
		trace.Mark("capacity", "")
		trace.Allowed = true
		trace.Stage = "admitted"
		if s.decisions != nil {
			s.recordDecision(trace)
			r = r.WithContext(decisionpkg.WithResult(r.Context(), trace))
		}
		s.statistics.admission(priority, decision)
		s.setHeaders(w)

		completed := false
		var metrics httpsnoop.Metrics
		defer func() {
			elapsed := time.Since(start)
			canceled := r.Context().Err() != nil
			if s.controller != nil {
				change := s.controller.End(p, generation, time.Now(), elapsed, o.targetLatency, !completed || metrics.Code >= 500, canceled)
				if change != nil {
					s.statistics.adjustment(change.Previous, change.Limit, change.Reason)
					if s.logger != nil {
						s.logger.Info("concurrency limit adjusted", "service", s.name, "previous", change.Previous, "limit", change.Limit, "reason", change.Reason, "p90_ratio", change.P90Ratio, "samples", change.Samples)
					}
				}
			} else {
				s.limiter.Release(p)
			}
			s.statistics.complete(priority, metrics.Code, completed, canceled, elapsed)
		}()
		if r.Context().Err() != nil {
			return
		}
		metrics = httpsnoop.CaptureMetrics(next, w, r)
		completed = true
	})
}

func (s *Service) setHeaders(w http.ResponseWriter) {
	state := s.Snapshot()
	w.Header().Set("X-Gatewise-Limit", strconv.Itoa(state.Limit))
	w.Header().Set("X-Gatewise-Mode", state.Mode)
}
func (s *Service) reject(w http.ResponseWriter, priority Priority, decision admission.Decision, status int, traces ...decisionpkg.Result) {
	s.statistics.admission(priority, decision)
	s.setHeaders(w)
	if s.logger != nil {
		s.logger.Warn("request rejected", "service", s.name, "priority", priority, "reason", decision)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	code := "service_overloaded"
	if status != 503 {
		code = string(decision)
	}
	body := map[string]any{"error": code, "service": s.name, "priority": string(priority), "reason": string(decision)}
	if s.decisions != nil && len(traces) > 0 {
		body["decision"] = traces[0]
	}
	_ = json.NewEncoder(w).Encode(body)
}
