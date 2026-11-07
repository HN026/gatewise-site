package metrics_test

import (
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestControllerMetricsIncludeEvaluatedHold(t *testing.T) {
	g, err := middleware.NewService(middleware.ServiceConfig{Name: "orders", MaxConcurrent: 4, Adaptive: &middleware.AdaptiveConfig{MinConcurrent: 2, InitialConcurrent: 2, MinSamples: 1, Interval: time.Nanosecond, TargetLatency: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	r := prometheus.NewRegistry()
	if err = metrics.Register(r, g); err != nil {
		t.Fatal(err)
	}
	before := read(t, r)
	if before["gatewise_controller_last_evaluation_timestamp_seconds|service=orders"] != 0 {
		t.Fatal("unexpected evaluation")
	}
	h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 1; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
	got := read(t, r)
	if got["gatewise_controller_last_window_samples|service=orders"] != 1 || got["gatewise_controller_last_decision|decision=hold|reason=no_change|service=orders"] != 1 || got["gatewise_controller_last_evaluation_timestamp_seconds|service=orders"] <= 0 {
		t.Fatalf("missing evaluated hold: %v", got)
	}
	if g.ControllerSnapshot().Decision != "hold" || !g.Statistics().Controller.Enabled {
		t.Fatal("native snapshot missing")
	}
}
