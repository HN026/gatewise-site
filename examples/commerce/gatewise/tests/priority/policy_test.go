package priority_test

import (
	"context"
	"github.com/HN026/gatewise/priority"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func config() priority.Config {
	return priority.Config{Tiers: []priority.Tier{{Name: "urgent", Reserved: 2}, {Name: "normal", Reserved: 1}, {Name: "batch"}}, TrustedProxies: []string{"127.0.0.1/32"}, Cohorts: &priority.Cohorts{Buckets: 128, Rotation: time.Second, Seed: "test-only-cohort-seed"}}
}
func TestTrustPropagationAndRotation(t *testing.T) {
	p, e := priority.New(config(), 8)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Unix(1800000000, 0)
	r := httptest.NewRequest("GET", "http://local", nil)
	r = r.WithContext(priority.With(r.Context(), "normal", "authenticated-user"))
	r, m, e := p.Resolve(r, "background", now)
	if e != nil || m.Source != "context" || m.Tier != "normal" {
		t.Fatal(m, e)
	}
	priority.Inject(r)
	if strings.Contains(r.Header.Get(priority.Header), "authenticated-user") {
		t.Fatal("identity leaked")
	}
	r.RemoteAddr = "127.0.0.1:9"
	r = r.WithContext(context.Background())
	_, down, e := p.Resolve(r, "background", now)
	if e != nil || down.Source != "trusted_http" || down.Bucket != m.Bucket || down.Epoch != m.Epoch {
		t.Fatal(down, e)
	}
	changed := false
	for i := 1; i < 8; i++ {
		_, v, _ := p.Resolve(r, "background", now.Add(time.Duration(i)*time.Second))
		changed = changed || v.Bucket != m.Bucket
	}
	if !changed {
		t.Fatal("cohort never rotates")
	}
	r.RemoteAddr = "192.0.2.1:9"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if _, _, e = p.Resolve(r, "background", now); e != priority.ErrUntrusted {
		t.Fatal(e)
	}
	r.RemoteAddr = "127.0.0.1:9"
	for _, bad := range []string{"v1;urgent;invalid", "v1;unknown;-", "urgent", "v2;urgent;-"} {
		r.Header.Set(priority.Header, bad)
		if _, _, e = p.Resolve(r, "background", now); e != priority.ErrInvalid {
			t.Fatal(bad, e)
		}
	}
	r.Header.Del(priority.Header)
	_, v, e := p.Resolve(r, "background", now)
	if e != nil || v.Tier != "batch" || v.Source != "route_default" {
		t.Fatal(v, e)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOutboundDestinationAndClone(t *testing.T) {
	p, _ := priority.New(config(), 8)
	r := httptest.NewRequest("GET", "http://trusted:9000/test", nil)
	r = r.WithContext(priority.With(r.Context(), "urgent", "actor"))
	r, _, _ = p.Resolve(r, "background", time.Now())
	r.Header.Set(priority.Header, "forged")
	calls := 0
	transport := priority.Transport{Hosts: []string{"trusted:9000"}, Base: roundTrip(func(out *http.Request) (*http.Response, error) {
		calls++
		value := out.Header.Get(priority.Header)
		if calls == 1 && !strings.HasPrefix(value, "v1;urgent;") {
			t.Fatal(value)
		}
		if calls == 2 && value != "" {
			t.Fatal("cross-host propagation", value)
		}
		return &http.Response{StatusCode: 204, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	transport.RoundTrip(r)
	if r.Header.Get(priority.Header) != "forged" {
		t.Fatal("request mutated")
	}
	r.URL.Host = "external:9000"
	transport.RoundTrip(r)
	r.Header = nil
	priority.Inject(r)
	if !strings.HasPrefix(r.Header.Get(priority.Header), "v1;urgent;") {
		t.Fatal("nil-header injection failed")
	}
}
func TestPolicyValidation(t *testing.T) {
	for _, mutate := range []func(*priority.Config){func(c *priority.Config) { c.Tiers[1].Name = "urgent" }, func(c *priority.Config) { c.Tiers[2].Reserved = 1 }, func(c *priority.Config) { c.CriticalTier = "absent" }, func(c *priority.Config) { c.TrustedProxies = []string{"bad"} }, func(c *priority.Config) { c.Cohorts.Seed = "short" }, func(c *priority.Config) { c.Cohorts.Rotation = time.Millisecond }} {
		c := config()
		mutate(&c)
		if _, e := priority.New(c, 8); e == nil {
			t.Fatal("invalid accepted", c)
		}
	}
}
