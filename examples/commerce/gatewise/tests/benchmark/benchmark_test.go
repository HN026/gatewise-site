package benchmark_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/HN026/gatewise/internal/benchmark"
	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/internal/proxy"
	"github.com/HN026/gatewise/middleware"
)

func TestNearestRankLatency(t *testing.T) {
	in := []float64{5, 1, 4, 2, 3}
	s := benchmark.Summarize(in)
	if s.Count != 5 || s.Mean != 3 || s.P50 != 3 || s.P95 != 5 || s.P99 != 5 || in[0] != 5 {
		t.Fatalf("incorrect distribution or mutated input: %+v %v", s, in)
	}
	if benchmark.Summarize(nil).Count != 0 {
		t.Fatal("empty distribution")
	}
}

type sink struct{ header http.Header }

func (s *sink) Header() http.Header         { return s.header }
func (s *sink) Write(p []byte) (int, error) { return len(p), nil }
func (s *sink) WriteHeader(int)             {}

// Handler benchmarks isolate instrumentation/wrapping allocations, without a
// network, serialization fixture or load generator in the timed loop.
func BenchmarkHandler(b *testing.B) {
	bare := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, mode := range []string{"bare", "fixed", "adaptive"} {
		b.Run(mode, func(b *testing.B) {
			var h http.Handler = bare
			if mode != "bare" {
				cfg := middleware.ServiceConfig{Name: "benchmark", MaxConcurrent: 6, ReservedCritical: 1}
				if mode == "adaptive" {
					cfg.Adaptive = &middleware.AdaptiveConfig{MinConcurrent: 2, InitialConcurrent: 6, Interval: time.Second, MinSamples: 10, TargetLatency: 100 * time.Millisecond}
				}
				s, err := middleware.NewService(cfg)
				if err != nil {
					b.Fatal(err)
				}
				h = s.Middleware(middleware.Critical, h)
			}
			r := httptest.NewRequest(http.MethodGet, "http://example.test/critical/work", nil)
			w := &sink{http.Header{}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(w, r)
			}
		})
	}
}

// Transport benchmarks compare the same httptest backend directly and through
// proxy.New. Logging is deliberately disabled here, unlike the process runs.
func BenchmarkHTTP(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer backend.Close()
	upstream, _ := url.Parse(backend.URL)
	for _, mode := range []string{"direct", "proxy-fixed", "proxy-adaptive"} {
		b.Run(mode, func(b *testing.B) {
			target := backend.URL
			if mode != "direct" {
				s := config.Service{Upstream: upstream, MaxConcurrent: 6, ReservedCritical: 1}
				if mode == "proxy-adaptive" {
					s.Adaptive = &middleware.AdaptiveConfig{MinConcurrent: 2, InitialConcurrent: 6, Interval: time.Second, MinSamples: 10, TargetLatency: 100 * time.Millisecond}
				}
				h, closeIdle := proxy.New(config.Config{Services: map[string]config.Service{"benchmark": s}, Routes: []config.Route{{Prefix: "/", Service: "benchmark", Priority: "critical"}}, DialTimeout: time.Second, ResponseHeaderTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				defer closeIdle()
				server := httptest.NewServer(h)
				defer server.Close()
				target = server.URL
			}
			transport := &http.Transport{MaxIdleConnsPerHost: 10}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
				resp, err := client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					b.Fatalf("status %d", resp.StatusCode)
				}
			}
		})
	}
}
