package operation_test

import (
	"context"
	"errors"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/operation"
	"github.com/HN026/gatewise/priority"
	"sync"
	"testing"
	"time"
)

func TestLifetimeNestedAndOnce(t *testing.T) {
	e, _ := operation.New(operation.Config{MaxConcurrent: 1})
	ctx, cancel := context.WithCancel(context.Background())
	p, err := e.Begin(ctx, operation.Request{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if e.Snapshot().Active != 1 {
		t.Fatal("cancellation released live work")
	}
	if _, err := e.Begin(context.Background(), operation.Request{}); err == nil {
		t.Fatal("capacity bypass")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Finish(operation.Outcome{}) }()
	}
	wg.Wait()
	if e.Snapshot().Active != 0 || e.Statistics().Work[0].Completed[2] != 1 {
		t.Fatal("not exactly once")
	}
	parent, _ := e.Begin(context.Background(), operation.Request{})
	if _, err := e.Begin(parent.Context(), operation.Request{}); !errors.Is(err, operation.ErrNested) {
		t.Fatal(err)
	}
	other, _ := operation.New(operation.Config{MaxConcurrent: 1})
	if err := other.Do(parent.Context(), operation.Request{}, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	parent.Finish(operation.Outcome{})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		_ = e.Do(context.Background(), operation.Request{}, func(context.Context) error { panic("application") })
	}()
	if e.Snapshot().Active != 0 || e.Statistics().Work[0].Completed[3] != 1 {
		t.Fatal("panic leaked or unreported")
	}
}
func TestPriorityTenantsResourcesAndCancellation(t *testing.T) {
	dec, _ := decision.New(decision.Config{Budgets: []decision.Budget{{Name: "writes", Resource: "write", MaxWriteBytes: 100}}})
	e, err := operation.New(operation.Config{MaxConcurrent: 3, Decisions: dec, Tenants: &operation.TenantConfig{MaxConcurrent: 1}, Priorities: &priority.Config{Tiers: []priority.Tier{{Name: "urgent", Reserved: 1}, {Name: "batch"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := priority.With(context.Background(), "urgent", "authenticated")
	p, err := e.Begin(ctx, operation.Request{Priority: "batch", Tenant: "one", Resource: "write", Cost: decision.Cost{WriteBytes: 60}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Decision().Priority != "urgent" {
		t.Fatal("importance lost")
	}
	_, err = e.Begin(context.Background(), operation.Request{Tenant: "one", Resource: "write", Cost: decision.Cost{WriteBytes: 30}})
	var rejection *operation.Rejection
	if !errors.As(err, &rejection) || rejection.Reason != "tenant_concurrency_limit" {
		t.Fatal(err)
	}
	if dec.BudgetSnapshot()[0].ActiveWriteBytes != 60 {
		t.Fatal("later rejection did not roll back")
	}
	p.Finish(operation.Outcome{})
	ctx2, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Begin(ctx2, operation.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx3, cancel3 := context.WithCancel(context.Background())
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = e.Do(ctx3, operation.Request{Tenant: "two"}, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	cancel3()
	if e.Snapshot().Active != 1 {
		t.Fatal("ignored-cancel work released")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion stuck")
	}
}
