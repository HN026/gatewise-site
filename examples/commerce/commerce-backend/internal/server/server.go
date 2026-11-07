package server

import (
	"context"
	"errors"
	"github.com/HN026/gatewise-examples/commerce/internal/config"
	"github.com/HN026/gatewise-examples/commerce/internal/handler"
	"github.com/HN026/gatewise-examples/commerce/internal/store"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net"
	"net/http"
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
	var g *middleware.Service
	if c.Mode != "direct" && c.Mode != "semaphore" {
		cfg := middleware.ServiceConfig{Name: c.Role, MaxConcurrent: c.MaxConcurrent, ReservedCritical: c.Reserved}
		if c.TenantLimit > 0 {
			resolver, err := middleware.TrustedTenantHeader("X-Demo-Tenant", []string{"127.0.0.1/32", "::1/128"})
			if err != nil {
				return err
			}
			cfg.Tenants = &middleware.TenantConfig{MaxConcurrent: c.TenantLimit, MaxTracked: c.TenantMaxTracked, IdleTTL: c.TenantIdleTTL, Resolver: resolver}
		}
		if c.Mode == "adaptive" {
			cfg.Adaptive = &middleware.AdaptiveConfig{MinConcurrent: c.Minimum, InitialConcurrent: c.Initial, Interval: c.Interval, MinSamples: c.MinSamples, TargetLatency: c.Target}
		}
		g, e = middleware.NewService(cfg)
		if e != nil {
			return e
		}
	}
	a := handler.New(s, g, c.Mode)
	mux := http.NewServeMux()
	referenceSlots := make(chan struct{}, c.MaxConcurrent)
	protect := func(p middleware.Priority, h http.HandlerFunc) http.Handler {
		if c.Mode == "semaphore" {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Context().Err() != nil {
					return
				}
				select {
				case referenceSlots <- struct{}{}:
					defer func() { <-referenceSlots }()
					h.ServeHTTP(w, r)
				default:
					handler.JSON(w, 503, map[string]string{"error": "service_overloaded", "reason": "concurrency_limit"})
				}
			})
		}
		if g != nil {
			return g.Middleware(p, h)
		}
		return h
	}
	switch c.Role {
	case "orders":
		mux.Handle("POST /orders/checkout", protect(middleware.Critical, a.Checkout))
		mux.Handle("GET /orders/{id}", protect(middleware.Critical, a.Get))
	case "catalog":
		mux.Handle("GET /catalog/products", protect(middleware.Critical, a.Products))
	case "inventory":
		mux.Handle("POST /inventory/reservations", protect(middleware.Critical, a.Reserve))
		mux.Handle("GET /inventory/stock/{id}", protect(middleware.Critical, a.Stock))
	}
	mux.Handle("POST /"+c.Role+"/reports/rebuild", protect(middleware.Background, a.Rebuild))
	mux.HandleFunc("GET /healthz", a.Health)
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector())
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
	admin.HandleFunc("GET /state", a.State)
	admin.HandleFunc("PUT /control", a.Update)
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
