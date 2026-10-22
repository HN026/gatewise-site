package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func read(t *testing.T, registry *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := []string{}
			for _, label := range metric.Label {
				labels = append(labels, label.GetName()+"="+label.GetValue())
			}
			sort.Strings(labels)
			key := family.GetName() + "|" + strings.Join(labels, "|")
			if metric.Counter != nil {
				values[key] = metric.Counter.GetValue()
			}
			if metric.Gauge != nil {
				values[key] = metric.Gauge.GetValue()
			}
			if metric.Histogram != nil {
				values[key] = float64(metric.Histogram.GetSampleCount())
			}
		}
	}
	return values
}

func TestMetricsAccountingAndRegistryIsolation(t *testing.T) {
	guard, err := middleware.NewService(middleware.ServiceConfig{Name: "payments", MaxConcurrent: 2, ReservedCritical: 1})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	if err := metrics.Register(registry, guard); err != nil {
		t.Fatal(err)
	}
	if err := metrics.Register(registry, guard); err == nil {
		t.Fatal("duplicate service registration accepted")
	}
	if err := metrics.Register(prometheus.NewRegistry(), guard); err != nil {
		t.Fatal("global registry coupling", err)
	}
	entered, done := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	held := guard.Middleware(middleware.Background, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	go func() {
		defer close(done)
		held.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	}()
	<-entered
	background := guard.Middleware(middleware.Background, http.NotFoundHandler())
	rejected := httptest.NewRecorder()
	background.ServeHTTP(rejected, httptest.NewRequest("GET", "/customer/123?secret=sample", nil))
	if rejected.Code != 503 {
		t.Error("expected reserved-capacity rejection")
	}
	for _, status := range []int{201, 500} {
		h := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
	panicHandler := guard.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("sample") }))
	func() {
		defer func() { _ = recover() }()
		panicHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	before := read(t, registry)
	for key, want := range map[string]float64{
		"gatewise_concurrency_limit|service=payments":                                                             2,
		"gatewise_requests_in_flight|priority=background|service=payments":                                        1,
		"gatewise_requests_admitted_total|priority=critical|service=payments":                                     3,
		"gatewise_requests_rejected_total|priority=background|reason=critical_capacity_reserved|service=payments": 1,
		"gatewise_requests_completed_total|outcome=completed|priority=critical|service=payments|status_class=2xx": 1,
		"gatewise_requests_completed_total|outcome=completed|priority=critical|service=payments|status_class=5xx": 1,
		"gatewise_requests_completed_total|outcome=panicked|priority=critical|service=payments|status_class=none": 1,
		"gatewise_request_duration_seconds|outcome=completed|priority=critical|service=payments":                  2,
	} {
		if got := before[key]; got != want {
			t.Errorf("%s got %v want %v", key, got, want)
		}
	}
	scrape := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		scrape.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "customer/123") || strings.Contains(w.Body.String(), "secret=sample") {
			t.Error("scrape failed or unbounded request data leaked")
		}
	}
	after := read(t, registry)
	for key, value := range before {
		if after[key] != value {
			t.Errorf("scraping altered %s", key)
		}
	}
	cancel()
	<-done
	final := read(t, registry)
	if final["gatewise_requests_in_flight|priority=background|service=payments"] != 0 || final["gatewise_requests_completed_total|outcome=canceled|priority=background|service=payments|status_class=none"] != 1 {
		t.Fatal("cancellation accounting failed")
	}
	stats := guard.Statistics()
	for _, p := range stats.Priorities {
		for _, outcome := range p.Outcomes {
			var previous uint64
			for _, bucket := range outcome.DurationBuckets {
				if bucket < previous || bucket > outcome.Count {
					t.Fatal("incoherent histogram")
				}
				previous = bucket
			}
		}
	}
}
