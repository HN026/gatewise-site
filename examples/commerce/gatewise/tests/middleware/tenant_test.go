package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func tenantRequest(id string) *http.Request {
	r := httptest.NewRequest("GET", "/work", nil)
	r.Header.Set("X-Tenant", id)
	return r
}
func TestTenantIsolationAndLifetime(t *testing.T) {
	g, e := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, ReservedCritical: 1, Tenants: &middleware.TenantConfig{MaxConcurrent: 1, Resolver: func(r *http.Request) (string, error) { return r.Header.Get("X-Tenant"), nil }}})
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant") == "a" {
			close(entered)
			<-release
		}
		w.WriteHeader(204)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	r := tenantRequest("a").WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), r) }()
	<-entered
	cancel()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, tenantRequest("a"))
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), "tenant_concurrency_limit") {
		t.Fatal(rr.Code, rr.Body)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, tenantRequest("b"))
	if rr.Code != 204 {
		t.Fatal("unrelated tenant blocked", rr.Code)
	}
	close(release)
	<-done
	if g.Snapshot().Active != 0 || g.TenantSnapshot().IdentifiedActive != 0 {
		t.Fatal("leaked capacity")
	}
	panicHandler := g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		panicHandler.ServeHTTP(httptest.NewRecorder(), tenantRequest("a"))
	}()
	if g.TenantSnapshot().IdentifiedActive != 0 || g.Snapshot().Active != 0 {
		t.Fatal("panic leaked capacity")
	}
}
func TestTenantRejectionDoesNotFeedAdaptiveController(t *testing.T) {
	g, e := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, Adaptive: &middleware.AdaptiveConfig{MinConcurrent: 2, InitialConcurrent: 3, Interval: time.Nanosecond, MinSamples: 1, TargetLatency: time.Second}, Tenants: &middleware.TenantConfig{MaxConcurrent: 1, Resolver: func(r *http.Request) (string, error) { return "private-tenant", nil }}})
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); h.ServeHTTP(httptest.NewRecorder(), tenantRequest("a")) }()
	<-entered
	for i := 0; i < 20; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, tenantRequest("a"))
		if rr.Code != 503 {
			t.Fatal(rr.Code)
		}
	}
	if !g.ControllerSnapshot().EvaluatedAt.IsZero() || g.Snapshot().Limit != 3 {
		t.Fatal("tenant rejection affected controller")
	}
	registry := prometheus.NewRegistry()
	if e = metrics.Register(registry, g); e != nil {
		t.Fatal(e)
	}
	rr := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	data := rr.Body.String()
	if strings.Contains(data, "private-tenant") || !strings.Contains(data, "reason=\"tenant_concurrency_limit\"") {
		t.Fatal("missing aggregate diagnostics or identity leaked")
	}
	close(release)
	wg.Wait()
}
func TestGlobalRejectRollsBackTenantAndOverridesCopied(t *testing.T) {
	limits := map[string]int{"a": 1}
	g, e := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 2, Tenants: &middleware.TenantConfig{MaxConcurrent: 2, MaxTracked: 2, Limits: limits, Resolver: func(r *http.Request) (string, error) { return r.Header.Get("X-Tenant"), nil }}})
	if e != nil {
		t.Fatal(e)
	}
	limits["a"] = 2
	entered, release := make(chan string, 2), make(chan struct{})
	h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- r.Header.Get("X-Tenant"); <-release }))
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); h.ServeHTTP(httptest.NewRecorder(), tenantRequest(id)) }(id)
		<-entered
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, tenantRequest("a"))
	if !strings.Contains(rr.Body.String(), "tenant_concurrency_limit") {
		t.Fatal("override map was not copied")
	}
	// b has one available tenant slot but the service is full.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, tenantRequest("b"))
	if !strings.Contains(rr.Body.String(), "concurrency_limit") {
		t.Fatal(rr.Body)
	}
	if g.TenantSnapshot().IdentifiedActive != 2 {
		t.Fatal("global rejection leaked tenant slot")
	}
	close(release)
	wg.Wait()
}
func TestTrustedTenantIdentityAndErrorPrivacy(t *testing.T) {
	resolver, e := middleware.TrustedTenantHeader("X-Tenant", []string{"127.0.0.1/32"})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		peer, id, xff string
		duplicate     bool
		status        int
	}{{"127.0.0.1:123", "a", "", false, 204}, {"192.0.2.1:12", "a", "127.0.0.1", false, 403}, {"127.0.0.1:1", "a", "", true, 400}, {"127.0.0.1:1", "", "", false, 400}, {"127.0.0.1:1", "bad value", "", false, 400}} {
		g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 2, Tenants: &middleware.TenantConfig{MaxConcurrent: 1, Resolver: resolver}})
		r := tenantRequest(tc.id)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		if tc.duplicate {
			r.Header.Add("X-Tenant", "b")
		}
		rr := httptest.NewRecorder()
		g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(rr, r)
		if rr.Code != tc.status {
			t.Fatalf("peer %s: %d", tc.peer, rr.Code)
		}
	}
	var logs strings.Builder
	g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 2, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Tenants: &middleware.TenantConfig{MaxConcurrent: 1, Resolver: func(*http.Request) (string, error) { return "", errors.New("secret-identity") }}})
	rr := httptest.NewRecorder()
	g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid resolver invoked handler") })).ServeHTTP(rr, tenantRequest("a"))
	data, _ := json.Marshal(g.Statistics())
	if strings.Contains(rr.Body.String()+logs.String()+string(data), "secret-identity") {
		t.Fatal("resolver error leaked")
	}
}

func TestMappedProxyCIDR(t *testing.T) {
	resolver, e := middleware.TrustedTenantHeader("X-Tenant", []string{"::ffff:127.0.0.1/128"})
	if e != nil {
		t.Fatal(e)
	}
	r := tenantRequest("a")
	r.RemoteAddr = "127.0.0.1:12"
	id, e := resolver(r)
	if e != nil || id != "a" {
		t.Fatal(id, e)
	}
}
