package middleware_test

import (
	"github.com/HN026/gatewise/middleware"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdaptiveConfigurationAndWriterCompatibility(t *testing.T) {
	for _, a := range []middleware.AdaptiveConfig{{MinConcurrent: 1}, {MinConcurrent: 5}, {MinConcurrent: 2, InitialConcurrent: 1}, {Interval: -time.Second}, {MinSamples: -1}, {TargetLatency: -time.Second}} {
		if _, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, ReservedCritical: 1, Adaptive: &a}); err == nil {
			t.Fatal("invalid adaptive config accepted")
		}
	}
	guard, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, ReservedCritical: 1, Adaptive: &middleware.AdaptiveConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	state := guard.Snapshot()
	if state.Mode != "adaptive" || state.Limit != 2 || state.MinConcurrent != 2 || state.MaxConcurrent != 4 {
		t.Fatalf("bad conservative defaults: %+v", state)
	}
	h := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("adaptive instrumentation hid Flusher")
			return
		}
		w.WriteHeader(201)
		f.Flush()
	}), middleware.WithLatencyTarget(time.Second))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 201 || w.Header().Get("X-Gatewise-Mode") != "adaptive" || w.Header().Get("X-Gatewise-Limit") != "2" || guard.Snapshot().Active != 0 {
		t.Fatalf("writer/accounting changed: %d %+v", w.Code, guard.Snapshot())
	}
	fixed, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, ReservedCritical: 1})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Snapshot().Mode != "fixed" || fixed.Snapshot().Limit != 4 {
		t.Fatal("fixed mode changed")
	}
}
