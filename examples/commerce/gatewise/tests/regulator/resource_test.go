package regulator_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/resource"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/regulator"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func engine(t *testing.T, mode decision.Mode, budget decision.Budget) *decision.Engine {
	t.Helper()
	e, err := decision.New(decision.Config{Mode: mode, Budgets: []decision.Budget{budget}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestResourceAtomicityAndLifetime(t *testing.T) {
	e := engine(t, decision.Enforce, decision.Budget{Name: "writes", Resource: "write", MaxWriteBytes: 100, Keys: &decision.KeyBudget{MaxConcurrent: 1, MaxTracked: 4, IdleTTL: time.Millisecond}})
	var peak atomic.Int64
	held := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	g, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 16, Decisions: e, CostResolver: func(r *http.Request) (decision.Cost, error) {
		return decision.Cost{WriteBytes: 60, Key: r.URL.Query().Get("key")}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { peak.Add(1); defer peak.Add(-1); close(held); <-release }), middleware.WithResource("write"))
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/?key=private-key", nil))
	}()
	<-held
	for _, key := range []string{"private-key", "different-key"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/?key="+key, nil))
		if w.Code != 503 || !strings.Contains(w.Body.String(), "write_bytes_limit") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Work on a separate server-configured resource remains independent.
	w := httptest.NewRecorder()
	g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, httptest.NewRequest("GET", "/read", nil))
	if w.Code != 204 {
		t.Fatal("unrelated work blocked")
	}
	state := e.BudgetSnapshot()[0]
	if state.ActiveWriteBytes != 60 || state.Tracked != 1 || state.Admitted != 1 {
		t.Fatal(state)
	}
	raw, _ := json.Marshal(g.DecisionSnapshot())
	if strings.Contains(string(raw), "private-key") {
		t.Fatal("key leaked")
	}
	close(release)
	wg.Wait()
	if e.BudgetSnapshot()[0].ActiveWriteBytes != 0 || peak.Load() != 0 {
		t.Fatal("resource leak")
	}
	// A canceled handler retains both concurrency and resources until it returns.
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan struct{})
	canceled := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-finish }), middleware.WithResource("write"))
	go func() {
		defer close(done)
		canceled.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/?key=canceled", nil).WithContext(ctx))
	}()
	<-entered
	cancel()
	if e.BudgetSnapshot()[0].ActiveWriteBytes != 60 {
		t.Fatal("released on cancellation before return")
	}
	close(finish)
	<-done
	panics := g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("application") }), middleware.WithResource("write"))
	func() {
		defer func() { _ = recover() }()
		panics.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/?key=panic", nil))
	}()
	if e.BudgetSnapshot()[0].Active != 0 {
		t.Fatal("panic leaked reservation")
	}
}
func TestKeyBoundsObserveAndRollback(t *testing.T) {
	for _, mode := range []decision.Mode{decision.Enforce, decision.Observe} {
		e := engine(t, mode, decision.Budget{Name: "keys", Keys: &decision.KeyBudget{MaxConcurrent: 1, MaxTracked: 1, IdleTTL: time.Millisecond, Overflow: "shared", Missing: "shared"}})
		first, _, reason := e.AcquireResources(decision.Target{}, decision.Cost{Key: "hot"}, time.Now())
		if reason != "" {
			t.Fatal(reason)
		}
		next, assessments, reason := e.AcquireResources(decision.Target{}, decision.Cost{Key: "hot"}, time.Now())
		if mode == decision.Enforce && reason != "hot_key_limit" || mode == decision.Observe && (reason != "" || assessments[0].Reason != "hot_key_limit") {
			t.Fatal(reason, assessments)
		}
		if next != nil {
			next.Release()
		}
		overflow, _, reason := e.AcquireResources(decision.Target{}, decision.Cost{Key: "cold"}, time.Now())
		if reason != "" {
			t.Fatal(reason)
		}
		for i := 0; i < 100; i++ {
			l, _, reason := e.AcquireResources(decision.Target{}, decision.Cost{Key: "churn"}, time.Now())
			if mode == decision.Enforce && reason != "hot_key_limit" {
				t.Fatal(reason)
			}
			if l != nil {
				l.Release()
			}
		}
		if e.BudgetSnapshot()[0].Tracked != 1 {
			t.Fatal("unbounded registry")
		}
		overflow.Release()
		first.Release()
		first.Release()
		first.Retain()
		if e.BudgetSnapshot()[0].Active != 0 || e.BudgetSnapshot()[0].Admitted != 0 {
			t.Fatal("release/retain lifecycle")
		}
		deadline := time.Now().Add(time.Second)
		for e.BudgetSnapshot()[0].Tracked > 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if e.BudgetSnapshot()[0].Tracked != 0 {
			t.Fatal("no expiry")
		}
	}
	// All-or-none across different resource budgets.
	e, err := decision.New(decision.Config{Budgets: []decision.Budget{{Name: "bytes", MaxWriteBytes: 100}, {Name: "keys", Keys: &decision.KeyBudget{MaxConcurrent: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := e.AcquireResources(decision.Target{}, decision.Cost{Key: "key", WriteBytes: 10}, time.Now())
	defer first.Release()
	_, _, reason := e.AcquireResources(decision.Target{}, decision.Cost{Key: "key", WriteBytes: 20}, time.Now())
	if reason != "hot_key_limit" || e.BudgetSnapshot()[0].ActiveWriteBytes != 10 {
		t.Fatal("partial reservation retained")
	}
}
func TestTrustedOperationMetadata(t *testing.T) {
	resolve, err := middleware.TrustedCostHeader([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "192.0.2.1:123"
	req.Header.Set(middleware.CostHeader, `{"write_bytes":10,"key":"private"}`)
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	if _, err = resolve(req); !errors.Is(err, middleware.ErrUntrustedCostSource) {
		t.Fatal("forged trust", err)
	}
	req.RemoteAddr = "127.0.0.1:123"
	c, err := resolve(req)
	if err != nil || c.WriteBytes != 10 {
		t.Fatal(err, c)
	}
	for _, value := range []string{`null`, `{"write_bytes":10,"unknown":1}`, `{} {}`, strings.Repeat("x", 513)} {
		req.Header.Set(middleware.CostHeader, value)
		if _, err := resolve(req); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	// Unresolved client metadata must never propagate on the proxy hop.
	middleware.InjectCost(req)
	if req.Header.Get(middleware.CostHeader) != "" {
		t.Fatal("unresolved cost forwarded")
	}
}
func TestCgroupHierarchy(t *testing.T) {
	base := fstest.MapFS{"proc/self/cgroup": {Data: []byte("0::/parent/child\n")}, "proc/self/mountinfo": {Data: []byte("1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n")}, "sys/fs/cgroup/memory.max": {Data: []byte("max")}, "sys/fs/cgroup/parent/memory.max": {Data: []byte("1000")}, "sys/fs/cgroup/parent/memory.current": {Data: []byte("900")}, "sys/fs/cgroup/parent/child/memory.max": {Data: []byte("500")}, "sys/fs/cgroup/parent/child/memory.current": {Data: []byte("100")}}
	v, found, err := resource.CgroupMemory(base)
	if err != nil || !found || v.Limit != 1000 || v.Used != 900 {
		t.Fatal(v, found, err)
	}
	base["sys/fs/cgroup/parent/memory.current"] = &fstest.MapFile{Data: []byte("broken")}
	if _, _, err := resource.CgroupMemory(base); err == nil {
		t.Fatal("bad container sample treated healthy")
	}
	_, found, err = resource.CgroupMemory(fstest.MapFS{})
	if err != nil || found {
		t.Fatal(found, err)
	}
	// Mount root may hide an ancestor; map process membership to its visible root.
	scoped := fstest.MapFS{"proc/self/cgroup": {Data: []byte("0::/\n")}, "proc/self/mountinfo": {Data: []byte("1 0 0:1 /private/container /sys/fs/cgroup rw - cgroup2 cgroup rw\n")}, "sys/fs/cgroup/memory.max": {Data: []byte("100")}, "sys/fs/cgroup/memory.current": {Data: []byte("80")}}
	v, found, err = resource.CgroupMemory(scoped)
	if err != nil || !found || v.Used != 80 {
		t.Fatal(v, found, err)
	}
	// v1 unlimited sentinel is not treated as a useful memory budget.
	v1 := fstest.MapFS{"proc/self/cgroup": {Data: []byte("2:memory:/\n")}, "proc/self/mountinfo": {Data: []byte("1 0 0:1 / /sys/fs/cgroup/memory rw - cgroup memory rw,memory\n")}, "sys/fs/cgroup/memory/memory.limit_in_bytes": {Data: []byte("9223372036854771712")}}
	_, found, err = resource.CgroupMemory(v1)
	if err != nil || found {
		t.Fatal(found, err)
	}
}
func TestRuntimeMeasurements(t *testing.T) {
	memory, err := regulator.NewMemory(regulator.MemoryConfig{LimitBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	o, err := memory.Sample(context.Background())
	if err != nil || o.State != "measured" || o.Value <= 0 || memory.Snapshot().LimitBytes == 0 {
		t.Fatal(o, err)
	}
	goroutines := regulator.Goroutines{}
	before, _ := goroutines.Sample(context.Background())
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-release }()
	}
	during, _ := goroutines.Sample(context.Background())
	close(release)
	wg.Wait()
	if during.Value < before.Value+20 {
		t.Fatal("goroutines not measured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := memory.Sample(ctx); err == nil {
		t.Fatal("canceled sample accepted")
	}
}

func TestResourcesRollBackOnServiceRejection(t *testing.T) {
	e := engine(t, decision.Enforce, decision.Budget{Name: "bytes", Resource: "write", MaxWriteBytes: 100})
	g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 1, Decisions: e, CostResolver: func(*http.Request) (decision.Cost, error) { return decision.Cost{WriteBytes: 60}, nil }})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ordinary := g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(entered); <-release }))
	go func() {
		defer close(done)
		ordinary.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	<-entered
	w := httptest.NewRecorder()
	g.Middleware(middleware.Critical, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("over-capacity handler called") }), middleware.WithResource("write")).ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 503 || e.BudgetSnapshot()[0].ActiveWriteBytes != 0 || e.BudgetSnapshot()[0].Admitted != 0 {
		t.Fatal("resource reservation survived later service rejection")
	}
	close(release)
	<-done
}

func BenchmarkResourceMiddleware(b *testing.B) {
	for _, mode := range []decision.Mode{decision.Enforce, decision.Observe} {
		b.Run(string(mode), func(b *testing.B) {
			e, _ := decision.New(decision.Config{Mode: mode, Budgets: []decision.Budget{{Name: "writes", Resource: "write", MaxWriteBytes: 1 << 20}, {Name: "keys", Resource: "write", Keys: &decision.KeyBudget{MaxConcurrent: 4, MaxTracked: 32}}}})
			g, _ := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: 20, Decisions: e, CostResolver: func(*http.Request) (decision.Cost, error) {
				return decision.Cost{WriteBytes: 1024, Key: "trusted-partition"}, nil
			}})
			h := g.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), middleware.WithResource("write"))
			req := httptest.NewRequest("POST", "/writes", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
