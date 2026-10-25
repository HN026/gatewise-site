package middleware_test

import (
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/priority"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestTierReservationsAndCleanup(t *testing.T) {
	g, e := middleware.NewService(middleware.ServiceConfig{Name: "chain", MaxConcurrent: 4, Priorities: &priority.Config{Tiers: []priority.Tier{{Name: "urgent", Reserved: 1}, {Name: "normal", Reserved: 1}, {Name: "batch"}}}})
	if e != nil {
		t.Fatal(e)
	}
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	var wg sync.WaitGroup
	h := g.Middleware("batch", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-release; w.WriteHeader(204) }))
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
		<-started
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	normal := g.Middleware("normal", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-release; w.WriteHeader(204) }))
	wg.Add(1)
	go func() {
		defer wg.Done()
		normal.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	<-started
	w = httptest.NewRecorder()
	normal.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	urgent := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, ok := priority.FromContext(r.Context())
		if !ok || m.Tier != "urgent" {
			t.Error(m)
		}
		w.WriteHeader(204)
	}))
	w = httptest.NewRecorder()
	urgent.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	close(release)
	wg.Wait()
	if g.Snapshot().Active != 0 {
		t.Fatal("leak")
	}
	registry := prometheus.NewRegistry()
	if e = metrics.Register(registry, g); e != nil {
		t.Fatal(e)
	}
	if _, e = registry.Gather(); e != nil {
		t.Fatal(e)
	}
}
