package operation_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/operation"
	"github.com/HN026/gatewise/priority"
)

func queued(t *testing.T, e *operation.Engine, n int) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if e.QueueSnapshot().Waiting == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queue depth", e.QueueSnapshot())
}
func rejectReason(t *testing.T, err error, want string) {
	t.Helper()
	var r *operation.Rejection
	if !errors.As(err, &r) || r.Reason != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}
func TestQueuePriorityFIFOAndAging(t *testing.T) {
	for _, aged := range []bool{false, true} {
		t.Run(map[bool]string{false: "priority", true: "aged"}[aged], func(t *testing.T) {
			e, err := operation.New(operation.Config{MaxConcurrent: 1, Queue: &operation.QueueConfig{MaxWaiting: 4, MaxWait: 2 * time.Second, AgingAfter: 80 * time.Millisecond}})
			if err != nil {
				t.Fatal(err)
			}
			held, _ := e.Begin(context.Background(), operation.Request{})
			defer held.Finish(operation.Outcome{})
			type result struct {
				priority operation.Priority
				p        *operation.Permit
				err      error
			}
			done := make(chan result, 3)
			launch := func(priority operation.Priority) {
				go func() {
					p, err := e.Begin(context.Background(), operation.Request{Priority: priority})
					done <- result{priority, p, err}
				}()
			}
			launch(operation.Background)
			queued(t, e, 1)
			if aged {
				time.Sleep(100 * time.Millisecond)
			}
			launch(operation.Critical)
			queued(t, e, 2)
			held.Finish(operation.Outcome{})
			first := <-done
			if first.err != nil {
				t.Fatal(first.err)
			}
			want := operation.Critical
			if aged {
				want = operation.Background
			}
			if first.priority != want {
				t.Fatal("selection", first.priority, want)
			}
			if first.p.QueueWait() <= 0 {
				t.Fatal("missing wait")
			}
			first.p.Finish(operation.Outcome{})
			second := <-done
			if second.err != nil {
				t.Fatal(second.err)
			}
			second.p.Finish(operation.Outcome{})
			queued(t, e, 0)
			q := e.QueueSnapshot()
			if q.HighWater != 2 || q.Tiers[0].Admitted != 1 || q.Tiers[1].Admitted != 1 {
				t.Fatal(q)
			}
		})
	}
	// Same-tier FIFO is determined by arrival, not goroutine wake order.
	e, _ := operation.New(operation.Config{MaxConcurrent: 1, Queue: &operation.QueueConfig{MaxWaiting: 2, MaxWait: time.Second}})
	held, _ := e.Begin(context.Background(), operation.Request{})
	order := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			p, err := e.Begin(context.Background(), operation.Request{})
			if err != nil {
				order <- -1
				return
			}
			order <- i
			p.Finish(operation.Outcome{})
		}(i)
		queued(t, e, i+1)
	}
	held.Finish(operation.Outcome{})
	if a, b := <-order, <-order; a != 0 || b != 1 {
		t.Fatal("FIFO", a, b)
	}
}
func TestQueueBoundsReservationCancellationAndDeadline(t *testing.T) {
	e, err := operation.New(operation.Config{MaxConcurrent: 1, Queue: &operation.QueueConfig{MaxWaiting: 2, MaxWait: time.Second, Reserved: map[operation.Priority]int{operation.Critical: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	held, _ := e.Begin(context.Background(), operation.Request{})
	defer held.Finish(operation.Outcome{})
	ctx, cancel := context.WithCancel(context.Background())
	bg := make(chan error, 1)
	go func() {
		p, err := e.Begin(ctx, operation.Request{Priority: operation.Background})
		if p != nil {
			p.Finish(operation.Outcome{})
		}
		bg <- err
	}()
	queued(t, e, 1)
	_, err = e.Begin(context.Background(), operation.Request{Priority: operation.Background})
	rejectReason(t, err, "queue_reserved")
	deadline, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	urgent := make(chan error, 1)
	go func() { _, err := e.Begin(deadline, operation.Request{}); urgent <- err }()
	queued(t, e, 2)
	_, err = e.Begin(context.Background(), operation.Request{})
	rejectReason(t, err, "queue_full")
	if err = <-urgent; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	cancel()
	if err = <-bg; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	queued(t, e, 0)
	if e.Snapshot().Active != 1 {
		t.Fatal("cancellation freed executing work")
	}
	q := e.QueueSnapshot()
	if q.HighWater != 2 || q.Tiers[0].Full != 1 || q.Tiers[0].Deadline != 1 || q.Tiers[1].Reserved != 1 || q.Tiers[1].Canceled != 1 {
		t.Fatal(q)
	}
}
func TestQueueTimeoutHoldsNoLeasesAndResolvesCostOnce(t *testing.T) {
	dec, _ := decision.New(decision.Config{Budgets: []decision.Budget{{Name: "writes", Resource: "write", MaxWriteBytes: 100}}})
	e, err := operation.New(operation.Config{MaxConcurrent: 1, Decisions: dec, Tenants: &operation.TenantConfig{MaxConcurrent: 1}, Queue: &operation.QueueConfig{MaxWaiting: 2, MaxWait: 80 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	held, _ := e.Begin(context.Background(), operation.Request{Tenant: "one", Resource: "write", Cost: decision.Cost{WriteBytes: 50}})
	defer held.Finish(operation.Outcome{})
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := e.Begin(context.Background(), operation.Request{Tenant: "two", Resource: "write", ResolveCost: func(context.Context) (decision.Cost, error) { calls.Add(1); return decision.Cost{WriteBytes: 50}, nil }})
		done <- err
	}()
	queued(t, e, 1)
	if e.TenantSnapshot().IdentifiedActive != 1 || dec.BudgetSnapshot()[0].ActiveWriteBytes != 50 {
		t.Fatal("queued work retained permits")
	}
	// Hard resource refusal remains immediate even with pending work.
	_, err = e.Begin(context.Background(), operation.Request{Tenant: "three", Resource: "write", Cost: decision.Cost{WriteBytes: 60}})
	rejectReason(t, err, "write_bytes_limit")
	rejectReason(t, <-done, "queue_timeout")
	if calls.Load() != 1 || e.QueueSnapshot().Tiers[0].Timeout != 1 {
		t.Fatal("callback replay/timeout", calls.Load(), e.QueueSnapshot())
	}
	held.Finish(operation.Outcome{})
	if e.Snapshot().Active != 0 || dec.BudgetSnapshot()[0].ActiveWriteBytes != 0 || e.TenantSnapshot().IdentifiedActive != 0 {
		t.Fatal("leaked permits")
	}
}

func TestQueuedWorkRechecksSignalsAndExcludesWaitFromFeedback(t *testing.T) {
	dec, err := decision.New(decision.Config{Rules: []decision.Rule{{Name: "pressure", Threshold: 1, MaxAge: time.Second, Pressure: decision.Effect{Reject: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = dec.Publish("pressure", decision.Observation{State: "measured", Value: 0, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	e, err := operation.New(operation.Config{MaxConcurrent: 1, Decisions: dec, Queue: &operation.QueueConfig{MaxWaiting: 2, MaxWait: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	held, _ := e.Begin(context.Background(), operation.Request{})
	defer held.Finish(operation.Outcome{})
	done := make(chan error, 1)
	go func() { _, err := e.Begin(context.Background(), operation.Request{}); done <- err }()
	queued(t, e, 1)
	if err = dec.Publish("pressure", decision.Observation{State: "measured", Value: 2, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rejectReason(t, <-done, "signal_pressure")
	if e.QueueSnapshot().Tiers[0].PolicyRejected != 1 || e.Snapshot().Active != 1 {
		t.Fatal("signal retry/permit accounting")
	}
	held.Finish(operation.Outcome{})
	adaptive, err := operation.New(operation.Config{MaxConcurrent: 2, Adaptive: &operation.AdaptiveConfig{Profile: "stable", MinConcurrent: 1, InitialConcurrent: 1, Interval: time.Millisecond, MinSamples: 1}, Queue: &operation.QueueConfig{MaxWaiting: 2, MaxWait: 300 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	active, _ := adaptive.Begin(context.Background(), operation.Request{LatencyTarget: time.Second})
	finished := make(chan *operation.Permit, 1)
	go func() {
		p, err := adaptive.Begin(context.Background(), operation.Request{LatencyTarget: 20 * time.Millisecond})
		if err != nil {
			finished <- nil
			return
		}
		finished <- p
	}()
	queued(t, adaptive, 1)
	time.Sleep(40 * time.Millisecond)
	active.Finish(operation.Outcome{})
	p := <-finished
	if p == nil {
		t.Fatal("no queued permit")
	}
	time.Sleep(2 * time.Millisecond)
	p.Finish(operation.Outcome{})
	if p.QueueWait() < 30*time.Millisecond || adaptive.ControllerSnapshot().ControlRatio > 1 {
		t.Fatal("queue polluted processing feedback", p.QueueWait(), adaptive.ControllerSnapshot())
	}
}

func TestQueueCustomTiersAndCompatibilityMapping(t *testing.T) {
	e, err := operation.New(operation.Config{MaxConcurrent: 1, Priorities: &priority.Config{Tiers: []priority.Tier{{Name: "urgent"}, {Name: "normal"}, {Name: "batch"}}}, Queue: &operation.QueueConfig{MaxWaiting: 3, MaxWait: 2 * time.Second, Reserved: map[operation.Priority]int{"urgent": 1, "normal": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	held, _ := e.Begin(context.Background(), operation.Request{})
	defer held.Finish(operation.Outcome{})
	type result struct {
		tier string
		p    *operation.Permit
		err  error
	}
	done := make(chan result, 3)
	for i, tier := range []operation.Priority{"batch", "normal", operation.Critical} {
		go func(p operation.Priority) {
			permit, err := e.Begin(context.Background(), operation.Request{Priority: p})
			name := ""
			if permit != nil {
				name = permit.Decision().Priority
			}
			done <- result{name, permit, err}
		}(tier)
		queued(t, e, i+1)
		if i < 2 {
			_, err = e.Begin(context.Background(), operation.Request{Priority: tier})
			rejectReason(t, err, "queue_reserved")
		}
	}
	held.Finish(operation.Outcome{})
	for _, want := range []string{"urgent", "normal", "batch"} {
		r := <-done
		if r.err != nil || r.tier != want {
			t.Fatal("tier selection", r.tier, r.err, want)
		}
		r.p.Finish(operation.Outcome{})
	}
	queued(t, e, 0)
}
