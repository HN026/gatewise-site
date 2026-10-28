package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/priority"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func newUpstream(c config.Config, target *url.URL, logger *slog.Logger) (http.Handler, func()) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Use only the explicitly configured backend, not environment proxies.
	transport.DialContext = (&net.Dialer{Timeout: c.DialTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = c.DialTimeout
	transport.ResponseHeaderTimeout = c.ResponseHeaderTimeout
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 100
	p := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			priority.Inject(r.Out)
			middleware.InjectCost(r.Out)
			// SetXForwarded builds fresh forwarding headers; incoming values are not trusted.
			r.Out.Header.Del("Forwarded")
			r.SetXForwarded()
		},
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return
			}
			status, code := http.StatusBadGateway, "upstream_unavailable"
			var ne net.Error
			if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
				status, code = http.StatusGatewayTimeout, "upstream_timeout"
			}
			logger.Warn("upstream request failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
			writeJSON(w, status, map[string]string{"error": code})
		},
	}
	return p, transport.CloseIdleConnections
}

// New builds one transport per named service; routes to a service share its pool.
func New(c config.Config, logger *slog.Logger) (http.Handler, func()) {
	registry := prometheus.NewRegistry()
	backends := make(map[string]backend, len(c.Services))
	var cleanups []func()
	for name, service := range c.Services {
		h, closeIdle := newUpstream(c, service.Upstream, logger.With("service", name))
		guard, err := middleware.NewService(middleware.ServiceConfig{
			CostResolver: service.CostResolver, Decisions: service.Decisions, Name: name, MaxConcurrent: service.MaxConcurrent, ReservedCritical: service.ReservedCritical, Logger: logger, Adaptive: service.Adaptive, Tenants: service.Tenants, Priorities: service.Priorities,
		})
		if err != nil {
			panic("proxy: invalid validated service configuration: " + err.Error())
		}
		if err := metrics.Register(registry, guard); err != nil {
			panic("proxy: metrics registration: " + err.Error())
		}
		backends[name] = backend{handler: h, guard: guard}
		cleanups = append(cleanups, closeIdle)
		if service.Decisions != nil {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _ = service.Decisions.Run(ctx, service.Collectors) }()
			cleanups = append(cleanups, func() {
				cancel()
				<-done
				for _, c := range service.Collectors {
					if p, ok := c.Provider.(interface{ Close() }); ok {
						p.Close()
					}
				}
			})
		}
	}

	h := accessLog(logger, route(newRouter(c.Routes, backends), promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
	return h, func() {
		for _, closeIdle := range cleanups {
			closeIdle()
		}
	}
}
