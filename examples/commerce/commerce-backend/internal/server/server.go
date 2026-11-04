package server

import (
	"context"
	"errors"
	"github.com/HN026/gatewise-examples/commerce/internal/baseline"
	"github.com/HN026/gatewise-examples/commerce/internal/config"
	"github.com/HN026/gatewise-examples/commerce/internal/handler"
	"github.com/HN026/gatewise-examples/commerce/internal/operations"
	"github.com/HN026/gatewise-examples/commerce/internal/store"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/priority"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net"
	"net/http"
	"strings"
	"time"
)

func Run(ctx context.Context, c config.Config) error {
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s, e := store.Open(startup, c.DatabaseURL, c.PoolSize, c.QueryTimeout, c.Role)
	if e != nil {
		return e
	}
	defer s.DB.Close()
	var pc *priority.Config
	if c.Priorities {
		pc = &priority.Config{Tiers: []priority.Tier{{Name: "urgent", Reserved: 2}, {Name: "normal", Reserved: 1}, {Name: "batch"}}, TrustedProxies: nil, Cohorts: &priority.Cohorts{Buckets: 128, Rotation: 2 * time.Second, Seed: "demo-only-cohort-seed"}}
	}
	if pc != nil && c.PriorityTrusted != "" {
		pc.TrustedProxies = strings.Split(c.PriorityTrusted, ",")
	}
	var work *resourceWork
	if c.ResourceMode != "" {
		work = newResources()
	}
	var g *middleware.Service
	var engine *decision.Engine
	var collectors []decision.Collector
	if c.Mode != "direct" && c.Mode != "semaphore" && c.Mode != "rate" {
		cfg := middleware.ServiceConfig{Name: c.Role, MaxConcurrent: c.MaxConcurrent, ReservedCritical: c.Reserved}
		if c.QueueWaiting > 0 {
			reserved := middleware.Critical
			if pc != nil {
				reserved = "urgent"
			}
			cfg.Queue = &middleware.QueueConfig{MaxWaiting: c.QueueWaiting, MaxWait: c.QueueWait, AgingAfter: c.QueueAging, Reserved: map[middleware.Priority]int{reserved: c.QueueReserved}}
		}
		if c.DecisionMode != "" {
			engine, collectors, e = signalEngine(c.DecisionMode, c.AdminListen)
			if e != nil {
				return e
			}
			cfg.Decisions = engine
		}
		if work != nil && c.ResourceMode != "disabled" {
			engine, collectors, e = resourceEngine(c.ResourceMode, work)
			if e != nil {
				return e
			}
			cfg.Decisions = engine
			cfg.CostResolver = plannedCost
		}
		if pc != nil {
			cfg.ReservedCritical = 0
			cfg.Priorities = pc
		}
		if c.TenantLimit > 0 {
			resolver, err := middleware.TrustedTenantHeader("X-Demo-Tenant", []string{"127.0.0.1/32", "::1/128"})
			if err != nil {
				return err
			}
			cfg.Tenants = &middleware.TenantConfig{MaxConcurrent: c.TenantLimit, MaxTracked: c.TenantMaxTracked, IdleTTL: c.TenantIdleTTL, Resolver: resolver}
			if c.SharingCeiling > 0 {
				cfg.Tenants.Sharing = &middleware.TenantSharingConfig{MaxConcurrent: c.SharingCeiling, DemandTTL: 500 * time.Millisecond}
			}
		}
		if c.Mode == "adaptive" {
			cfg.Adaptive = &middleware.AdaptiveConfig{Profile: c.AdaptiveProfile, MinConcurrent: c.Minimum, InitialConcurrent: c.Initial, Interval: c.Interval, MinSamples: c.MinSamples, TargetLatency: c.Target}
		}
		g, e = middleware.NewService(cfg)
		if e != nil {
			return e
		}
	}
	a := handler.New(s, g, c.Mode)
	mux := http.NewServeMux()
	reference := baseline.New(c.Mode, c.MaxConcurrent, c.RatePerSecond, c.RateBurst)
	protect := func(p middleware.Priority, h http.HandlerFunc) http.Handler {
		observed := reference.Wrap(h)
		h = observed.ServeHTTP
		if c.Mode == "semaphore" || c.Mode == "rate" {
			return h
		}
		if g != nil {
			if c.RouteBudgets && p == middleware.Background {
				return g.Middleware(p, h, middleware.WithLatencyTarget(500*time.Millisecond))
			}
			return g.Middleware(p, h)
		}
		if pc != nil {
			policy, err := priority.New(*pc, c.MaxConcurrent)
			if err != nil {
				panic(err)
			}
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resolved, m, err := policy.Resolve(r, string(p), time.Now())
				if err != nil {
					status := 400
					if errors.Is(err, priority.ErrUntrusted) {
						status = 403
					}
					handler.JSON(w, status, map[string]string{"reason": err.Error()})
					return
				}
				w.Header().Set("X-Gatewise-Resolved-Priority", m.Tier)
				w.Header().Set("X-Gatewise-Priority-Source", m.Source)
				h.ServeHTTP(w, resolved)
			})
		}
		return h
	}
	if work != nil {
		for _, kind := range []string{"allocation", "fanout", "write", "key_write"} {
			var h http.Handler = work.operation(kind, s)
			if g != nil {
				h = g.Middleware(middleware.Critical, h, middleware.WithResource(kind))
			}
			mux.Handle("POST /"+c.Role+"/resource/"+kind, work.parse(h))
		}
	}
	var ops *operations.Manager
	if c.OperationsMode != "" {
		ops, e = operations.New(ctx, s.DB, c.DatabaseURL, c.ReplicaURL, c.OperationsMode)
		if e != nil {
			return e
		}
		defer ops.Close()
		mux.Handle("POST /orders/operations/transaction", protect(middleware.Critical, ops.Transaction))
		mux.Handle("GET /orders/operations/replica", protect(middleware.Critical, ops.ReadReplica))
	}
	switch c.Role {
	case "orders":
		if c.Priorities && c.CatalogURL != "" {
			for _, tier := range []string{"urgent", "normal", "batch"} {
				mux.Handle("GET /orders/flows/"+tier, protect(middleware.Priority(tier), a.Flow(c.CatalogURL)))
			}
		}
		mux.Handle("POST /orders/checkout", protect(middleware.Critical, a.Checkout))
		mux.Handle("GET /orders/{id}", protect(middleware.Critical, a.Get))
	case "catalog":
		catalogPriority := middleware.Critical
		if c.Priorities {
			catalogPriority = "batch"
		}
		mux.Handle("GET /catalog/products", protect(catalogPriority, a.Products))
	case "inventory":
		mux.Handle("POST /inventory/reservations", protect(middleware.Critical, a.Reserve))
		mux.Handle("GET /inventory/stock/{id}", protect(middleware.Critical, a.Stock))
	}
	mux.Handle("POST /"+c.Role+"/reports/rebuild", protect(middleware.Background, a.Rebuild))
	mux.HandleFunc("GET /healthz", a.Health)
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector())
	if ops != nil {
		if e = ops.Register(registry); e != nil {
			return e
		}
	}
	if g != nil {
		if e = metrics.Register(registry, g); e != nil {
			return e
		}
	}
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "commerce_db_pool_in_use", ConstLabels: prometheus.Labels{"service": c.Role}, Help: "SQL connections in use."}, func() float64 { return float64(s.DB.Stats().InUse) }))
	registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "commerce_db_pool_wait_count_total", ConstLabels: prometheus.Labels{"service": c.Role}, Help: "SQL pool waits."}, func() float64 { return float64(s.DB.Stats().WaitCount) }))
	registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "commerce_db_pool_wait_seconds_total", ConstLabels: prometheus.Labels{"service": c.Role}, Help: "Accumulated SQL pool wait seconds."}, func() float64 { return s.DB.Stats().WaitDuration.Seconds() }))
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	admin := http.NewServeMux()
	admin.HandleFunc("GET /reference/state", func(w http.ResponseWriter, r *http.Request) { handler.JSON(w, 200, reference.Snapshot()) })
	admin.HandleFunc("GET /state", a.State)
	admin.HandleFunc("PUT /control", a.Update)
	if ops != nil {
		admin.HandleFunc("GET /operations/state", ops.State)
		admin.HandleFunc("PUT /operations/control", ops.Control)
		admin.HandleFunc("POST /operations/stream", ops.Stream)
		admin.HandleFunc("DELETE /operations/stream", ops.Stream)
		admin.HandleFunc("POST /operations/pool", ops.Pool)
		admin.HandleFunc("DELETE /operations/pool", ops.Pool)
	}
	if work != nil {
		admin.HandleFunc("GET /resource/state", work.state)
		admin.HandleFunc("GET /resource/persisted", work.persisted(s))
		admin.HandleFunc("PUT /resource/control", work.control)
		admin.HandleFunc("GET /resource/signals/{name}", work.observation)
	}
	if c.DecisionMode != "" {
		controls := newSignals()
		admin.Handle("GET /signals/{name}", controls)
		admin.Handle("PUT /signals/{name}", controls)
	}
	l, e := net.Listen("tcp", c.Listen)
	if e != nil {
		return e
	}
	defer l.Close()
	al, e := net.Listen("tcp", c.AdminListen)
	if e != nil {
		return e
	}
	defer al.Close()
	servers := []*http.Server{{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}, {Handler: admin, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}}
	errs := make(chan error, 2)
	go func() { errs <- servers[0].Serve(l) }()
	go func() { errs <- servers[1].Serve(al) }()
	collectorCtx, stopCollectors := context.WithCancel(ctx)
	defer stopCollectors()
	if engine != nil {
		done := make(chan struct{})
		go func() { defer close(done); _ = engine.Run(collectorCtx, collectors) }()
		defer func() {
			stopCollectors()
			<-done
			for _, c := range collectors {
				if closer, ok := c.Provider.(interface{ Close() }); ok {
					closer.Close()
				}
			}
		}()
	}
	select {
	case <-ctx.Done():
	case e = <-errs:
	}
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdown); err != nil {
			srv.Close()
		}
	}
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return nil
}
