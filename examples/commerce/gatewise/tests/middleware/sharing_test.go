package middleware_test

import (
	"context"
	"github.com/HN026/gatewise/middleware"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestBorrowedLifetimeAndPriorityReservation(t *testing.T) {
	g, e := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, ReservedCritical: 1, Tenants: &middleware.TenantConfig{MaxConcurrent: 1, Sharing: &middleware.TenantSharingConfig{MaxConcurrent: 4}, Resolver: func(r *http.Request) (string, error) { return r.Header.Get("X-Tenant"), nil }}})
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var wg sync.WaitGroup
	h := g.Middleware(middleware.Background, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.ServeHTTP(httptest.NewRecorder(), tenantRequest("busy").WithContext(ctx)) }()
		<-entered
	}
	cancel()
	if v := g.TenantSnapshot(); v.BorrowedActive != 2 || v.IdentifiedActive != 3 {
		t.Fatal(v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, tenantRequest("busy"))
	if w.Code != 503 {
		t.Fatal("priority reservation bypass", w.Code)
	}
	critical := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	w = httptest.NewRecorder()
	critical.ServeHTTP(w, tenantRequest("other"))
	if w.Code != 204 || w.Header().Get("X-Gatewise-Tenant-Admission") != "share" {
		t.Fatal(w.Code, w.Header())
	}
	close(release)
	wg.Wait()
	if v := g.TenantSnapshot(); v.BorrowedActive != 0 || v.IdentifiedActive != 0 {
		t.Fatal("leaked sharing reservations", v)
	}
	panicHandler := g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		panicHandler.ServeHTTP(httptest.NewRecorder(), tenantRequest("busy"))
	}()
	if g.TenantSnapshot().IdentifiedActive != 0 {
		t.Fatal("panic leak")
	}
}
func TestInvalidSharingConfiguration(t *testing.T) {
	for _, sharing := range []*middleware.TenantSharingConfig{{MaxConcurrent: 1}, {MaxConcurrent: 9}, {DemandTTL: -time.Second}, {DemandTTL: time.Hour}, {Limits: map[string]int{"a": 1}}} {
		_, e := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 8, Tenants: &middleware.TenantConfig{MaxConcurrent: 2, IdleTTL: time.Second, Sharing: sharing, Resolver: func(*http.Request) (string, error) { return "a", nil }}})
		if e == nil {
			t.Fatal("invalid sharing accepted", sharing)
		}
	}
}
