package baseline_test

import (
	"github.com/HN026/gatewise-examples/commerce/internal/baseline"
	"github.com/HN026/gatewise/middleware"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPoliciesRejectWithoutExecutingAndRelease(t *testing.T) {
	for _, mode := range []string{"semaphore", "rate"} {
		t.Run(mode, func(t *testing.T) {
			p := baseline.New(mode, 1, 0.000001, 1)
			entered := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			h := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
			go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
			<-entered
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			want := 503
			if mode == "rate" {
				want = 429
			}
			if w.Code != want {
				t.Fatalf("status=%d", w.Code)
			}
			close(release)
			<-done
			s := p.Snapshot()
			if s.Active != 0 || s.Peak != 1 || s.Admitted != 1 || s.Rejected != 1 {
				t.Fatalf("state=%+v", s)
			}
		})
	}
}
func TestSemaphoreConcurrentCeiling(t *testing.T) {
	p := baseline.New("semaphore", 4, 1, 1)
	entered := make(chan struct{}, 100)
	release := make(chan struct{})
	var wg sync.WaitGroup
	h := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }))
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	close(release)
	wg.Wait()
	s := p.Snapshot()
	if s.Peak > 4 || s.Active != 0 || s.Admitted+s.Rejected != 100 {
		t.Fatal(s)
	}
}

type sink struct{ header http.Header }

func (s *sink) Header() http.Header         { return s.header }
func (s *sink) Write(b []byte) (int, error) { return len(b), nil }
func (s *sink) WriteHeader(int)             {}

// All modes include the same demo observer and a successful no-op handler.
// Sequential execution isolates wrapping cost; it does not estimate overloaded throughput.
func BenchmarkSuccessfulHandler(b *testing.B) {
	for _, mode := range []string{"direct", "semaphore", "rate", "fixed", "adaptive", "fixed-queue", "adaptive-queue"} {
		b.Run(mode, func(b *testing.B) {
			referenceMode := mode
			if mode != "semaphore" && mode != "rate" {
				referenceMode = "direct"
			}
			p := baseline.New(referenceMode, 4, 1e15, 4)
			var h http.Handler = p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			if mode != "direct" && mode != "semaphore" && mode != "rate" {
				cfg := middleware.ServiceConfig{Name: "overhead", MaxConcurrent: 4, ReservedCritical: 1}
				if mode == "adaptive" || mode == "adaptive-queue" {
					cfg.Adaptive = &middleware.AdaptiveConfig{Profile: "stable", InitialConcurrent: 4}
				}
				if mode == "fixed-queue" || mode == "adaptive-queue" {
					cfg.Queue = &middleware.QueueConfig{MaxWaiting: 16, MaxWait: 40 * time.Millisecond}
				}
				g, err := middleware.NewService(cfg)
				if err != nil {
					b.Fatal(err)
				}
				h = g.Middleware(middleware.Critical, h)
			}
			w := &sink{http.Header{}}
			r := httptest.NewRequest("GET", "/", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(w, r)
			}
		})
	}
}
