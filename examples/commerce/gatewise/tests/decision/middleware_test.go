package decision_test

import (
	"context"
	"encoding/json"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAtomicCapObserveAndLifetime(t *testing.T) {
	for _, mode := range []decision.Mode{decision.Enforce, decision.Observe} {
		t.Run(string(mode), func(t *testing.T) {
			e := engine(t, mode, rule("pool", 3))
			publish(t, e, "pool", .9, time.Now())
			guard, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 12, ReservedCritical: 4, Decisions: e})
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			var peak, active atomic.Int32
			h := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				v, ok := decision.FromContext(r.Context())
				if !ok || !v.Allowed || v.Stage != "admitted" {
					t.Error("no admitted context")
				}
				n := active.Add(1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				defer active.Add(-1)
				<-release
				w.WriteHeader(204)
			}))
			const total = 40
			var wg sync.WaitGroup
			codes := make(chan int, total)
			for i := 0; i < total; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
					codes <- w.Code
				}()
			}
			expected := int32(3)
			if mode == decision.Observe {
				expected = 12
			}
			deadline := time.Now().Add(time.Second)
			for peak.Load() != expected && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			close(release)
			wg.Wait()
			close(codes)
			if peak.Load() != expected || guard.Snapshot().Active != 0 {
				t.Fatalf("peak=%d state=%+v", peak.Load(), guard.Snapshot())
			}
			snapshot := guard.DecisionSnapshot()
			if snapshot.Proposals[1] != total || snapshot.Last.Evaluation.ProposedLimit != 3 {
				t.Fatal(snapshot)
			}
			registry := prometheus.NewRegistry()
			if err := metrics.Register(registry, guard); err != nil {
				t.Fatal(err)
			}
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range families {
				if f.GetName() == "gatewise_signal_state" {
					found = true
				}
			}
			if !found {
				t.Fatal("missing decision metrics")
			}
		})
	}
}
func TestScopedSignalsIdentityAndHardGuards(t *testing.T) {
	r := rule("actor", 1)
	r.Scope = decision.Scope{Kind: "actor", Key: "authenticated"}
	r.Pressure = decision.Effect{Reject: true}
	e := engine(t, decision.Enforce, r)
	publish(t, e, "actor", .9, time.Now())
	if _, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, Decisions: e}); err == nil {
		t.Fatal("actor scope without resolver")
	}
	guard, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, Decisions: e, Tenants: &middleware.TenantConfig{MaxConcurrent: 2, Resolver: func(r *http.Request) (string, error) { return "authenticated", nil }}})
	called := false
	h := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Gatewise-Actor", "other")
	h.ServeHTTP(w, req)
	var body struct{ Decision decision.Result }
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 503 || called || body.Decision.Stage != "signals" || guard.TenantSnapshot().Tracked != 0 {
		t.Fatalf("rejected late: %d %+v", w.Code, body)
	}
	if body.Decision.Stages[2].State != "not_evaluated" {
		t.Fatal("unvisited tenant stage claimed passed")
	}
	observe := engine(t, decision.Observe, rule("pool", 1))
	publish(t, observe, "pool", .9, time.Now())
	g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 1, Decisions: observe})
	panicHandler := g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler") }))
	func() { defer func() { _ = recover() }(); panicHandler.ServeHTTP(httptest.NewRecorder(), req) }()
	if g.Snapshot().Active != 0 {
		t.Fatal("panic leaked capacity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("canceled called") })).ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	if g.DecisionSnapshot().Proposals[0] != 1 {
		t.Fatal("canceled counted")
	}
}

func TestObserveRetainsHardCapAndScopedReservation(t *testing.T) {
	for _, mode := range []decision.Mode{decision.Observe, decision.Enforce} {
		e := engine(t, mode, rule("pool", 2))
		publish(t, e, "pool", .9, time.Now())
		g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 4, ReservedCritical: 2, Decisions: e})
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		h := g.Middleware(middleware.Background, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(entered); <-release }))
		if mode == decision.Enforce {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 503 {
				t.Fatal("signal cap ignored protected reservation")
			}
			continue
		}
		go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
		<-entered
		second := g.Middleware(middleware.Background, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		// One occupied background slot still leaves the second hard-cap slot available.
		w := httptest.NewRecorder()
		second.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 204 {
			t.Fatal("observe enforced signal cap")
		}
		close(release)
		<-done
	}
}

func BenchmarkMiddleware(b *testing.B) {
	for _, mode := range []string{"disabled", "observe", "enforce"} {
		b.Run(mode, func(b *testing.B) {
			var e *decision.Engine
			if mode != "disabled" {
				rules := []decision.Rule{rule("primary", 4), rule("secondary", 6)}
				for i := range rules {
					rules[i].MaxAge = 24 * time.Hour
				}
				e, _ = decision.New(decision.Config{Mode: decision.Mode(mode), Rules: rules})
				for _, r := range rules {
					_ = e.Publish(r.Name, decision.Observation{State: "measured", ObservedAt: time.Now(), Value: .1})
				}
			}
			g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 20, ReservedCritical: 5, Decisions: e})
			h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			req := httptest.NewRequest("GET", "/", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
