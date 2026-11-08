package operation_test

import (
	"context"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/operation"
	"testing"
)

func TestDecisionViewsRemainDetached(t *testing.T) {
	e, err := operation.New(operation.Config{Name: "ownership", MaxConcurrent: 2})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Begin(context.Background(), operation.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Finish(operation.Outcome{})
	a, ok := decision.FromContext(p.Context())
	if !ok || !a.Allowed {
		t.Fatal(a)
	}
	a.Stages[0].State = "mutated"
	b := p.Decision()
	if b.Stages[0].State != "passed" {
		t.Fatal("context view mutated permit")
	}
	b.Stages[0].State = "mutated again"
	c, _ := decision.FromContext(p.Context())
	if c.Stages[0].State != "passed" {
		t.Fatal("permit view mutated context")
	}
	initial := decision.NewResult("critical", "")
	ctx := decision.WithResult(context.Background(), initial)
	initial.Stages[0].State = "caller mutation"
	detached, _ := decision.FromContext(ctx)
	if detached.Stages[0].State != "not_evaluated" {
		t.Fatal("public WithResult retained caller slices")
	}
}
