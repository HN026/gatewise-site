// Package priority resolves trusted request importance and propagates it through
// explicitly selected HTTP destinations. It performs no authentication or I/O.
package priority

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HN026/gatewise/internal/trust"
)

const Header = "X-Gatewise-Priority"

var ErrInvalid = errors.New("priority_metadata_invalid")
var ErrUntrusted = errors.New("priority_metadata_untrusted")

type Tier struct {
	Name     string `json:"name"`
	Reserved int    `json:"reserved"`
}
type Cohorts struct {
	Buckets  int           `json:"buckets"`
	Rotation time.Duration `json:"-"`
	Seed     string        `json:"-"`
}
type Config struct {
	Tiers                        []Tier
	CriticalTier, BackgroundTier string
	TrustedProxies               []string
	Cohorts                      *Cohorts
}
type Metadata struct {
	Tier             string
	Source           string // route_default, context, trusted_http
	Bucket           int
	Epoch            int64
	cohort, identity string
	resolved         bool
}
type contextKey struct{}

// With supplies server-authenticated importance and a stable authenticated actor.
// Call this only after authentication; identity is never placed in the wire header.
func With(ctx context.Context, tier, identity string) context.Context {
	return context.WithValue(ctx, contextKey{}, Metadata{Tier: tier, identity: identity, Source: "context"})
}
func FromContext(ctx context.Context) (Metadata, bool) {
	m, ok := ctx.Value(contextKey{}).(Metadata)
	return m, ok
}

type Policy struct {
	config  Config
	known   map[string]bool
	trusted func(*http.Request) bool
}

func New(c Config, maximum int) (*Policy, error) {
	if maximum < 1 {
		return nil, fmt.Errorf("priority: service maximum must be positive")
	}
	if len(c.Tiers) < 2 || len(c.Tiers) > 8 {
		return nil, fmt.Errorf("priority: configure 2..8 ordered tiers")
	}
	c.Tiers = append([]Tier(nil), c.Tiers...)
	known := map[string]bool{}
	reserved := 0
	for i, t := range c.Tiers {
		if !validName(t.Name) || known[t.Name] || t.Reserved < 0 || t.Reserved > maximum || (i == len(c.Tiers)-1 && t.Reserved != 0) {
			return nil, fmt.Errorf("priority: invalid/duplicate tier or reservation; lowest tier must reserve zero")
		}
		known[t.Name] = true
		if t.Reserved > maximum-reserved {
			return nil, fmt.Errorf("priority: total reservations exceed service limit")
		}
		reserved += t.Reserved
	}
	if reserved > maximum {
		return nil, fmt.Errorf("priority: total reservations exceed service limit")
	}
	if c.CriticalTier == "" {
		c.CriticalTier = c.Tiers[0].Name
	}
	if c.BackgroundTier == "" {
		c.BackgroundTier = c.Tiers[len(c.Tiers)-1].Name
	}
	if !known[c.CriticalTier] || !known[c.BackgroundTier] || c.CriticalTier == c.BackgroundTier {
		return nil, fmt.Errorf("priority: compatibility mappings must name distinct tiers")
	}
	criticalIndex, backgroundIndex := -1, -1
	for i, t := range c.Tiers {
		if t.Name == c.CriticalTier {
			criticalIndex = i
		}
		if t.Name == c.BackgroundTier {
			backgroundIndex = i
		}
	}
	if criticalIndex >= backgroundIndex || known["critical"] && c.CriticalTier != "critical" || known["background"] && c.BackgroundTier != "background" {
		return nil, fmt.Errorf("priority: Critical mapping must precede Background; legacy tier names must map to themselves")
	}
	if c.Cohorts != nil {
		v := *c.Cohorts
		c.Cohorts = &v
		if v.Buckets < 2 || v.Buckets > 1024 || v.Rotation < time.Second || v.Rotation > 24*time.Hour || len(v.Seed) < 16 || len(v.Seed) > 256 {
			return nil, fmt.Errorf("priority: cohorts need 2..1024 buckets, 1s..24h rotation and a 16..256 byte seed")
		}
	}
	trusted, e := trust.Peers(c.TrustedProxies)
	if e != nil {
		return nil, e
	}
	return &Policy{config: c, known: known, trusted: trusted}, nil
}
func validName(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for i, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || i > 0 && (c >= '0' && c <= '9' || c == '_')) {
			return false
		}
	}
	return true
}
func (p *Policy) Map(tier string) string {
	if tier == "critical" {
		return p.config.CriticalTier
	}
	if tier == "background" {
		return p.config.BackgroundTier
	}
	return tier
}
func (p *Policy) Valid(tier string) bool { return p.known[p.Map(tier)] }
func (p *Policy) Tiers() []Tier          { return append([]Tier(nil), p.config.Tiers...) }
func (p *Policy) Buckets() int {
	if p.config.Cohorts == nil {
		return 0
	}
	return p.config.Cohorts.Buckets
}
func (p *Policy) Resolve(r *http.Request, fallback string, now time.Time) (*http.Request, Metadata, error) {
	m := Metadata{Tier: p.Map(fallback), Source: "route_default", Bucket: -1}
	values := r.Header.Values(Header)
	if len(values) > 0 {
		if !p.trusted(r) {
			return r, m, ErrUntrusted
		}
		if len(values) != 1 || len(values[0]) > 128 {
			return r, m, ErrInvalid
		}
		v := strings.Split(values[0], ";")
		if len(v) != 3 || v[0] != "v1" || !p.known[v[1]] {
			return r, m, ErrInvalid
		}
		if v[2] != "-" {
			b, e := hex.DecodeString(v[2])
			if e != nil || len(b) != 32 || v[2] != strings.ToLower(v[2]) {
				return r, m, ErrInvalid
			}
			m.cohort = v[2]
		}
		m.Tier = v[1]
		m.Source = "trusted_http"
	}
	if local, ok := FromContext(r.Context()); ok {
		m = local
		m.Tier = p.Map(local.Tier)
		m.Source = "context"
		m.Bucket = -1
	}
	ctx, m, err := p.finalize(r.Context(), m, now)
	return r.WithContext(ctx), m, err
}

// ResolveContext accepts only application-authenticated context metadata. A
// previously HTTP-resolved context retains its bounded source and cohort key.
func (p *Policy) ResolveContext(ctx context.Context, fallback string, now time.Time) (context.Context, Metadata, error) {
	m := Metadata{Tier: p.Map(fallback), Source: "context", Bucket: -1}
	if local, ok := FromContext(ctx); ok {
		m = local
		m.Tier = p.Map(m.Tier)
		if !m.resolved {
			m.Source = "context"
		}
		m.Bucket = -1
	}
	return p.finalize(ctx, m, now)
}
func (p *Policy) finalize(ctx context.Context, m Metadata, now time.Time) (context.Context, Metadata, error) {
	if !p.known[m.Tier] || len(m.identity) > 128 {
		return ctx, m, ErrInvalid
	}
	if p.config.Cohorts != nil {
		c := p.config.Cohorts
		if m.cohort == "" {
			input := []byte(m.identity)
			if len(input) == 0 {
				input = make([]byte, 32)
				if _, e := rand.Read(input); e != nil {
					return ctx, m, ErrInvalid
				}
			}
			h := hmac.New(sha256.New, []byte(c.Seed))
			h.Write(input)
			m.cohort = hex.EncodeToString(h.Sum(nil))
		}
		m.Epoch = now.UnixNano() / int64(c.Rotation)
		h := sha256.Sum256([]byte(strconv.FormatInt(m.Epoch, 10) + ":" + m.cohort))
		n := uint64(0)
		for _, b := range h[:8] {
			n = n<<8 | uint64(b)
		}
		m.Bucket = int(n % uint64(c.Buckets))
	}
	m.identity = ""
	m.resolved = true
	return context.WithValue(ctx, contextKey{}, m), m, nil
}

// Inject removes caller metadata and writes only policy-resolved context metadata.
// The caller must select a trusted outbound destination before calling this.
func Inject(r *http.Request) {
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Del(Header)
	if m, ok := FromContext(r.Context()); ok && m.resolved {
		key := m.cohort
		if key == "" {
			key = "-"
		}
		r.Header.Set(Header, "v1;"+m.Tier+";"+key)
	}
}

// Transport propagates only to exact URL Host values (including port) in Hosts.
// Other destinations have the reserved metadata stripped. Caller requests are cloned.
// Redirects require the application's normal redirect/security policy as well.
type Transport struct {
	Base  http.RoundTripper
	Hosts []string
}

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	out.Header = r.Header.Clone()
	out.Header.Del(Header)
	for _, host := range t.Hosts {
		if strings.EqualFold(r.URL.Host, host) {
			Inject(out)
			break
		}
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(out)
}
