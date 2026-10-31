// Package middleware adapts the shared operation engine to HTTP.
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/operation"
	prioritypkg "github.com/HN026/gatewise/priority"
	"github.com/felixge/httpsnoop"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type Priority = operation.Priority

const (
	Critical   = operation.Critical
	Background = operation.Background
)

type ServiceConfig struct {
	Name                            string
	MaxConcurrent, ReservedCritical int
	Logger                          *slog.Logger
	Adaptive                        *AdaptiveConfig
	Tenants                         *TenantConfig
	Priorities                      *prioritypkg.Config
	Decisions                       *decision.Engine
	CostResolver                    CostResolver
}
type Service struct {
	*operation.Engine
	costResolver   CostResolver
	tenantResolver TenantResolver
	policy         *prioritypkg.Policy
	logger         *slog.Logger
	name           string
}

func NewService(c ServiceConfig) (*Service, error) {
	if c.Decisions != nil && c.Decisions.HasBudgets() && c.CostResolver == nil {
		return nil, fmt.Errorf("gatewise: resource budgets require a trusted CostResolver")
	}
	if c.CostResolver != nil && c.Decisions == nil {
		return nil, fmt.Errorf("gatewise: cost resolver requires a decision engine")
	}
	var tenants *operation.TenantConfig
	if c.Tenants != nil {
		if c.Tenants.Resolver == nil {
			return nil, fmt.Errorf("gatewise: tenant Resolver is required")
		}
		tenants = &operation.TenantConfig{Sharing: c.Tenants.Sharing, MaxConcurrent: c.Tenants.MaxConcurrent, Limits: c.Tenants.Limits, MaxTracked: c.Tenants.MaxTracked, IdleTTL: c.Tenants.IdleTTL, MissingPolicy: c.Tenants.MissingPolicy, OverflowPolicy: c.Tenants.OverflowPolicy}
	}
	if c.Decisions != nil && c.Decisions.HasActorRules() && tenants == nil {
		return nil, fmt.Errorf("gatewise: actor rules require a trusted tenant resolver")
	}
	engine, err := operation.New(operation.Config{Name: c.Name, MaxConcurrent: c.MaxConcurrent, ReservedCritical: c.ReservedCritical, Logger: c.Logger, Adaptive: c.Adaptive, Tenants: tenants, Priorities: c.Priorities, Decisions: c.Decisions})
	if err != nil {
		return nil, err
	}
	s := &Service{Engine: engine, costResolver: c.CostResolver, logger: c.Logger, name: c.Name}
	if s.name == "" {
		s.name = "service"
	}
	if c.Tenants != nil {
		s.tenantResolver = c.Tenants.Resolver
	}
	if c.Priorities != nil {
		s.policy, err = prioritypkg.New(*c.Priorities, c.MaxConcurrent)
	}
	return s, err
}
func (s *Service) Middleware(priority Priority, next http.Handler, options ...HandlerOption) http.Handler {
	if s.policy == nil && priority != Critical && priority != Background || s.policy != nil && !s.policy.Valid(string(priority)) {
		panic("gatewise: invalid route priority")
	}
	if next == nil {
		panic("gatewise: nil handler")
	}
	o := handlerOptions{}
	for _, option := range options {
		if option == nil {
			panic("gatewise: nil handler option")
		}
		option(&o)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		resolvedPriority := priority
		identityFail := func(reason string, status int) {
			trace := s.Engine.Reject(resolvedPriority, reason, "identity")
			s.respond(w, resolvedPriority, reason, status, trace)
		}
		if s.policy != nil {
			resolved, m, err := s.policy.Resolve(r, string(priority), time.Now())
			resolvedPriority = Priority(s.policy.Map(string(priority)))
			if err != nil {
				reason, status := "priority_metadata_invalid", 400
				if errors.Is(err, prioritypkg.ErrUntrusted) {
					reason, status = "priority_metadata_untrusted", 403
				}
				identityFail(reason, status)
				return
			}
			r = resolved
			resolvedPriority = Priority(m.Tier)
			w.Header().Set("X-Gatewise-Resolved-Priority", m.Tier)
			w.Header().Set("X-Gatewise-Priority-Source", m.Source)
		}
		id := ""
		if s.tenantResolver != nil {
			var err error
			id, err = s.tenantResolver(r)
			if r.Context().Err() != nil {
				return
			}
			if err != nil {
				reason, status := "tenant_identity_invalid", 400
				if errors.Is(err, ErrUntrustedTenantSource) {
					reason, status = "tenant_identity_untrusted", 403
				}
				identityFail(reason, status)
				return
			}
		}
		var resolveCost func(context.Context) (decision.Cost, error)
		if s.costResolver != nil && s.Engine.NeedsCost(o.resource) {
			resolveCost = func(ctx context.Context) (decision.Cost, error) { return s.costResolver(r.WithContext(ctx)) }
		}
		permit, err := s.Engine.Begin(r.Context(), operation.Request{Kind: "http", Priority: resolvedPriority, Tenant: id, Resource: o.resource, ResolveCost: resolveCost, LatencyTarget: o.targetLatency})
		if err != nil {
			var rejection *operation.Rejection
			if errors.As(err, &rejection) {
				status := 503
				if rejection.Input {
					status = 400
				}
				if rejection.Reason == "resource_metadata_untrusted" {
					status = 403
				}
				s.respond(w, resolvedPriority, rejection.Reason, status, rejection.Decision)
			} else if errors.Is(err, operation.ErrNested) {
				http.Error(w, "gatewise: same-engine nested admission", http.StatusInternalServerError)
			}
			return
		}
		trace := permit.Decision()
		s.signalHeaders(w, trace)
		s.setHeaders(w)
		kind, share, ceiling := permit.TenantAllocation()
		if s.TenantSnapshot().SharingEnabled && kind != "" {
			w.Header().Set("X-Gatewise-Tenant-Admission", kind)
			w.Header().Set("X-Gatewise-Tenant-Share", strconv.Itoa(share))
			w.Header().Set("X-Gatewise-Tenant-Ceiling", strconv.Itoa(ceiling))
		}
		completed := false
		var result httpsnoop.Metrics
		defer func() { permit.Finish(operation.Outcome{StatusCode: result.Code, Panicked: !completed}) }()
		workContext := permit.Context()
		if resolveCost != nil {
			workContext = resolvedCost(workContext, permit.Cost())
		}
		r = r.WithContext(workContext)
		if r.Context().Err() != nil {
			completed = true
			return
		}
		result = httpsnoop.CaptureMetrics(next, w, r)
		completed = true
	})
}
func (s *Service) setHeaders(w http.ResponseWriter) {
	state := s.Snapshot()
	w.Header().Set("X-Gatewise-Limit", strconv.Itoa(state.Limit))
	w.Header().Set("X-Gatewise-Mode", state.Mode)
	if controller := s.ControllerSnapshot(); controller.Enabled {
		w.Header().Set("X-Gatewise-Controller", controller.Profile)
	}
}
func (s *Service) signalHeaders(w http.ResponseWriter, trace decision.Result) {
	e := trace.Evaluation
	if e.Mode == "" {
		return
	}
	w.Header().Set("X-Gatewise-Decision-Mode", string(e.Mode))
	w.Header().Set("X-Gatewise-Signal-Proposal", e.Action)
	w.Header().Set("X-Gatewise-Proposed-Limit", strconv.Itoa(e.ProposedLimit))
	w.Header().Set("X-Gatewise-Effective-Limit", strconv.Itoa(e.EffectiveLimit))
}
func (s *Service) respond(w http.ResponseWriter, p Priority, reason string, status int, trace decision.Result) {
	s.setHeaders(w)
	s.signalHeaders(w, trace)
	if s.logger != nil {
		s.logger.Warn("request rejected", "service", s.name, "priority", p, "reason", reason)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	code := "service_overloaded"
	if status != 503 {
		code = reason
	}
	body := map[string]any{"error": code, "service": s.name, "priority": string(p), "reason": reason}
	if s.DecisionSnapshot().Enabled {
		body["decision"] = trace
	}
	_ = json.NewEncoder(w).Encode(body)
}
