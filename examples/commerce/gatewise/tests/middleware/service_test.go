package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HN026/gatewise/middleware"
)

func TestConfigurationAndRegistration(t *testing.T) {
	for _, c := range []middleware.ServiceConfig{{}, {MaxConcurrent: -1}, {MaxConcurrent: 1, ReservedCritical: -1}, {MaxConcurrent: 1, ReservedCritical: 2}} {
		if _, err := middleware.NewService(c); err == nil {
			t.Fatal("invalid service config accepted")
		}
	}
	guard, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		priority middleware.Priority
		handler  http.Handler
	}{{"invalid", http.NotFoundHandler()}, {middleware.Critical, nil}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid registration accepted")
				}
			}()
			guard.Middleware(tc.priority, tc.handler)
		}()
	}
}

func TestSharedBudgetAndReservation(t *testing.T) {
	guard, err := middleware.NewService(middleware.ServiceConfig{Name: "test", MaxConcurrent: 2, ReservedCritical: 1})
	if err != nil {
		t.Fatal(err)
	}
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	held := guard.Middleware(middleware.Background, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-finish; w.WriteHeader(200) }))
	go func() {
		defer close(done)
		held.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	<-entered
	background := guard.Middleware(middleware.Background, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	result := httptest.NewRecorder()
	background.ServeHTTP(result, httptest.NewRequest("GET", "/", nil))
	var rejection map[string]string
	if err := json.Unmarshal(result.Body.Bytes(), &rejection); err != nil {
		t.Error(err)
	}
	if result.Code != 503 || rejection["reason"] != "critical_capacity_reserved" {
		t.Errorf("background borrowed reservation: %d %v", result.Code, rejection)
	}
	critical := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
	result = httptest.NewRecorder()
	critical.ServeHTTP(result, httptest.NewRequest("GET", "/", nil))
	if result.Code != 202 {
		t.Errorf("critical reservation unavailable: %d", result.Code)
	}
	independent, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 1})
	if err != nil {
		t.Error(err)
	} else {
		result = httptest.NewRecorder()
		independent.Middleware(middleware.Background, http.NotFoundHandler()).ServeHTTP(result, httptest.NewRequest("GET", "/", nil))
		if result.Code != 404 {
			t.Errorf("separate service shared capacity unexpectedly: %d", result.Code)
		}
	}
	close(finish)
	<-done
	result = httptest.NewRecorder()
	background.ServeHTTP(result, httptest.NewRequest("GET", "/", nil))
	if result.Code != 201 {
		t.Fatal("background capacity leaked")
	}
}

func TestPanicAndCanceledContext(t *testing.T) {
	guard, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	panicHandler := guard.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler failed") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		panicHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	calls := 0
	healthy := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	healthy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	if calls != 0 {
		t.Fatal("canceled request reached handler")
	}
	result := httptest.NewRecorder()
	healthy.ServeHTTP(result, httptest.NewRequest("GET", "/", nil))
	if result.Code != 204 || calls != 1 {
		t.Fatal("panic/cancellation leaked capacity")
	}
}

func TestDisabledCostPolicyStripsCallerMetadata(t *testing.T) {
	guard, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/write", nil)
	r.Header.Set(middleware.CostHeader, `{"write_bytes":999}`)
	h := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.InjectCost(r)
		if value := r.Header.Get(middleware.CostHeader); value != "" {
			t.Errorf("invented resolved cost metadata: %s", value)
		}
		w.WriteHeader(204)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
}
