package decision_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/admission"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func rule(name string, cap int) decision.Rule {
	return decision.Rule{Name: name, Threshold: .8, MaxAge: time.Second, Pressure: decision.Effect{MaxConcurrent: cap}, OnUnknown: "limit", UnknownLimit: 2}
}
func engine(t *testing.T, mode decision.Mode, rules ...decision.Rule) *decision.Engine {
	t.Helper()
	e, err := decision.New(decision.Config{Mode: mode, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func publish(t *testing.T, e *decision.Engine, name string, value float64, at time.Time) {
	t.Helper()
	if err := e.Publish(name, decision.Observation{Value: value, ObservedAt: at, State: "measured"}); err != nil {
		t.Fatal(err)
	}
}
func TestFreshnessScopesAndConflict(t *testing.T) {
	a := rule("pool", 4)
	b := rule("writes", 1)
	b.Scope = decision.Scope{Kind: "resource", Key: "checkout"}
	b.Pressure = decision.Effect{Reject: true}
	b.OnUnknown = "allow"
	b.UnknownLimit = 0
	c := rule("actor", 3)
	c.Scope = decision.Scope{Kind: "actor", Key: "private_tenant"}
	e := engine(t, decision.Enforce, a, b, c)
	now := time.Now().Add(-time.Millisecond)
	if v := e.Evaluate(decision.Target{}, 12, now); v.ProposedLimit != 2 || v.Assessments[0].State != "missing" {
		t.Fatalf("missing: %+v", v)
	}
	publish(t, e, "pool", .9, now)
	publish(t, e, "writes", .1, now)
	publish(t, e, "actor", .9, now)
	v := e.Evaluate(decision.Target{Resource: "checkout", Actor: "private_tenant"}, 12, time.Now())
	if v.ProposedLimit != 3 || v.Reject {
		t.Fatalf("combine: %+v", v)
	}
	if e.Evaluate(decision.Target{}, 12, time.Now()).ProposedLimit != 4 {
		t.Fatal("scoped rules leaked")
	}
	publish(t, e, "writes", .9, time.Now())
	v = e.Evaluate(decision.Target{Resource: "checkout"}, 12, time.Now())
	if !v.Reject || v.Reason != "signal_pressure" {
		t.Fatal(v)
	}
	if err := e.Publish("pool", decision.Observation{Value: .1, State: "measured", ObservedAt: now.Add(-time.Second)}); !errors.Is(err, decision.ErrOutOfOrder) {
		t.Fatal(err)
	}
	if err := e.Publish("pool", decision.Observation{Value: .1, State: "measured", ObservedAt: time.Now().Add(time.Hour)}); !errors.Is(err, decision.ErrFutureObservation) {
		t.Fatal(err)
	}
	publish(t, e, "pool", .9, now) // exact duplicate cannot renew the measurement
	v = e.Evaluate(decision.Target{}, 12, now.Add(2*time.Second))
	if v.ProposedLimit != 2 || v.Assessments[0].State != "stale" {
		t.Fatal(v)
	}
	raw, _ := json.Marshal(e.Snapshot())
	if strings.Contains(string(raw), "private_tenant") {
		t.Fatal("actor leaked")
	}
	publish(t, e, "pool", .1, time.Now())
	if e.Evaluate(decision.Target{}, 12, time.Now()).Action != "allow" {
		t.Fatal("recovery failed")
	}
	observe := engine(t, decision.Observe, a)
	publish(t, observe, "pool", .9, time.Now())
	v = observe.Evaluate(decision.Target{}, 12, time.Now())
	if v.ProposedLimit != 4 || v.EffectiveLimit != 12 || v.Reject {
		t.Fatal(v)
	}
}
func TestCollectorTimeoutBoundsAndRestart(t *testing.T) {
	e := engine(t, decision.Enforce, rule("pool", 4))
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	provider := decision.ProviderFunc(func(context.Context) (decision.Observation, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return decision.Observation{State: "measured", Value: .1, ObservedAt: time.Now()}, nil
	})
	collectors := []decision.Collector{{Name: "pool", Interval: 50 * time.Millisecond, Timeout: 30 * time.Millisecond, Provider: provider}}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
		defer cancel()
		if err := e.Run(ctx, collectors); err != nil {
			t.Error(err)
		}
	}
	run()
	<-started
	run()
	if calls.Load() != 1 {
		t.Fatalf("hung provider spawned %d calls", calls.Load())
	}
	snap := e.Snapshot()[0]
	if snap.State != "unknown" || snap.Failures[1] != 1 || snap.Failures[3] < 1 {
		t.Fatal(snap)
	}
	close(release)
	// A released late sample must not undo the timeout fallback.
	deadline := time.Now().Add(time.Second)
	for e.Snapshot()[0].Collecting && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.Snapshot()[0].State != "unknown" {
		t.Fatal("late result applied")
	}
	// A cooperative provider can recover on the next run.
	collectors[0].Provider = decision.ProviderFunc(func(context.Context) (decision.Observation, error) {
		return decision.Observation{State: "measured", ObservedAt: time.Now(), Value: .1}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); _ = e.Run(ctx, collectors) }()
	for e.Snapshot()[0].State != "healthy" && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if e.Snapshot()[0].State != "healthy" {
		t.Fatal("restart did not recover")
	}
}
func TestHTTPProviderContract(t *testing.T) {
	for _, body := range []string{`{"state":"measured","value":0.2,"observed_at":"2026-01-01T00:00:00Z"}`, `{"state":"measured","extra":true}`, strings.Repeat(" ", 4097), `{} {}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		p, err := decision.NewHTTPProvider(s.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Sample(context.Background())
		p.Close()
		s.Close()
		if (err == nil) != (strings.Contains(body, "observed_at")) {
			t.Fatalf("document accepted incorrectly: %v", err)
		}
	}
	destCalls := atomic.Int32{}
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destCalls.Add(1) }))
	defer dest.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, 302) }))
	defer redirect.Close()
	p, _ := decision.NewHTTPProvider(redirect.URL)
	defer p.Close()
	if _, err := p.Sample(context.Background()); err == nil || destCalls.Load() != 0 {
		t.Fatal("followed redirect")
	}
}
func BenchmarkEvaluate(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			rules := make([]decision.Rule, n)
			for i := range rules {
				rules[i] = rule("signal_"+string(rune('a'+i/26))+string(rune('a'+i%26)), 4)
			}
			for i := range rules {
				rules[i].MaxAge = 24 * time.Hour
			}
			e, _ := decision.New(decision.Config{Rules: rules})
			for _, r := range rules {
				_ = e.Publish(r.Name, decision.Observation{State: "measured", ObservedAt: time.Now(), Value: .1})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.Evaluate(decision.Target{}, 12, time.Now())
			}
		})
	}
}

func TestCollectorErrorPanicAndRapidUpdates(t *testing.T) {
	for _, kind := range []string{"error", "panic"} {
		t.Run(kind, func(t *testing.T) {
			e := engine(t, decision.Enforce, rule("pool", 4))
			provider := decision.ProviderFunc(func(context.Context) (decision.Observation, error) {
				if kind == "panic" {
					panic("private provider details")
				}
				return decision.Observation{}, errors.New("private provider details")
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = e.Run(ctx, []decision.Collector{{Name: "pool", Interval: 50 * time.Millisecond, Timeout: 30 * time.Millisecond, Provider: provider}})
			}()
			for e.Snapshot()[0].State != "unknown" && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			cancel()
			<-done
			if e.Evaluate(decision.Target{}, 12, time.Now()).ProposedLimit != 2 {
				t.Fatal("failure fallback missing")
			}
			at := time.Now()
			for i := 0; i < 200; i++ {
				value := .95
				if i%2 == 1 {
					value = .1
				}
				at = at.Add(time.Nanosecond)
				for time.Now().Before(at) {
				}
				publish(t, e, "pool", value, at)
				v := e.Evaluate(decision.Target{}, 12, time.Now())
				if (v.Action == "limit") != (i%2 == 0) {
					t.Fatal("rapid change lost", i)
				}
			}
		})
	}
}

func TestSignalCeilingIndependentOfMovingBaseline(t *testing.T) {
	e := engine(t, decision.Enforce, rule("pool", 4))
	publish(t, e, "pool", .9, time.Now())
	v := e.Evaluate(decision.Target{}, 2, time.Now())
	if v.ProposedLimit != 2 || v.SignalCeiling != 4 {
		t.Fatal(v)
	}
	// The adaptive baseline can change after evaluation, before atomic admission.
	l := admission.New(2, 0)
	l.SetLimit(8)
	for i := 0; i < 4; i++ {
		if l.TryAcquireBudget(admission.Critical, -1, 0, v.SignalCeiling) != admission.Admitted {
			t.Fatal("stale baseline artificially clamped admission")
		}
	}
	if l.TryAcquireBudget(admission.Critical, -1, 0, v.SignalCeiling) != admission.SignalConcurrencyLimit {
		t.Fatal("baseline growth bypassed signal cap")
	}
}
