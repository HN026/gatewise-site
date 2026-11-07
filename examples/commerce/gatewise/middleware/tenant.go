package middleware

import (
	"errors"
	"fmt"
	"github.com/HN026/gatewise/internal/tenancy"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type TenantPolicy string

const (
	TenantReject TenantPolicy = "reject"
	TenantShared TenantPolicy = "shared"
)

var ErrUntrustedTenantSource = errors.New("gatewise: untrusted tenant source")
var ErrInvalidTenantIdentity = errors.New("gatewise: invalid tenant identity")

// TenantResolver must use an authenticated identity or explicitly trusted metadata.
// Empty identity invokes MissingPolicy. Errors reject without exposing their text.
// Keep resolvers local and fast; Gatewise does not authenticate identities.
type TenantResolver func(*http.Request) (string, error)

type TenantConfig struct {
	MaxConcurrent                 int
	Limits                        map[string]int // Optional overrides; copied and hashed at construction.
	MaxTracked                    int            // Default 1024 identified tenants, plus two bounded fallback buckets.
	IdleTTL                       time.Duration  // Default 5m; only inactive entries expire.
	MissingPolicy, OverflowPolicy TenantPolicy   // Default reject; shared uses a bounded bucket.
	Resolver                      TenantResolver
}
type TenantState struct {
	Enabled                                                                             bool
	MaxConcurrent, MaxTracked, Tracked, IdentifiedActive, MissingActive, OverflowActive int
	EntriesExpired                                                                      uint64
}

func newTenants(c *TenantConfig, maximum int) (*tenancy.Limiter, TenantResolver, error) {
	if c == nil {
		return nil, nil, nil
	}
	copy := *c
	if copy.MaxConcurrent <= 0 || copy.MaxConcurrent > maximum {
		return nil, nil, fmt.Errorf("gatewise: tenant MaxConcurrent must be between 1 and service MaxConcurrent")
	}
	if copy.Resolver == nil {
		return nil, nil, fmt.Errorf("gatewise: tenant Resolver is required")
	}
	if copy.MaxTracked == 0 {
		copy.MaxTracked = 1024
	}
	if copy.IdleTTL == 0 {
		copy.IdleTTL = 5 * time.Minute
	}
	if copy.MaxTracked < 1 || copy.MaxTracked > 1000000 || copy.IdleTTL < 0 {
		return nil, nil, fmt.Errorf("gatewise: tenant MaxTracked must be 1..1000000 and IdleTTL positive")
	}
	for _, p := range []TenantPolicy{copy.MissingPolicy, copy.OverflowPolicy} {
		if p != "" && p != TenantReject && p != TenantShared {
			return nil, nil, fmt.Errorf("gatewise: tenant policies must be reject or shared")
		}
	}
	if len(copy.Limits) > copy.MaxTracked {
		return nil, nil, fmt.Errorf("gatewise: tenant overrides exceed MaxTracked")
	}
	for id, limit := range copy.Limits {
		if !tenancy.ValidID(id) || limit < 1 || limit > maximum {
			return nil, nil, fmt.Errorf("gatewise: invalid tenant override identity or limit")
		}
	}
	return tenancy.New(tenancy.Config{Limit: copy.MaxConcurrent, MaxTracked: copy.MaxTracked, IdleTTL: copy.IdleTTL, MissingShared: copy.MissingPolicy == TenantShared, OverflowShared: copy.OverflowPolicy == TenantShared, Limits: copy.Limits}), copy.Resolver, nil
}
func (s *Service) TenantSnapshot() TenantState {
	if s.tenants == nil {
		return TenantState{}
	}
	v := s.tenants.Snapshot(time.Now())
	return TenantState{true, v.Limit, v.MaxTracked, v.Tracked, v.IdentifiedActive, v.MissingActive, v.OverflowActive, v.Expired}
}

// TrustedTenantHeader trusts only the immediate peer's address, never Forwarded
// or X-Forwarded-For. Trusted ingress must authenticate and overwrite the header.
// Requests without the header invoke MissingPolicy; supplied untrusted headers reject.
func TrustedTenantHeader(header string, cidrs []string) (TenantResolver, error) {
	if header == "" {
		return nil, fmt.Errorf("gatewise: tenant header is required")
	}
	for _, c := range []byte(header) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return nil, fmt.Errorf("gatewise: invalid tenant header")
		}
	}
	switch strings.ToLower(header) {
	case "host", "connection", "content-length", "transfer-encoding", "forwarded", "x-forwarded-for", "authorization", "cookie", "te", "trailer", "upgrade", "keep-alive", "proxy-connection", "proxy-authorization", "proxy-authenticate":
		return nil, fmt.Errorf("gatewise: tenant header must be a dedicated identity field")
	}
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("gatewise: tenant trusted proxy CIDRs are required")
	}
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		p, e := netip.ParsePrefix(cidr)
		if e != nil {
			return nil, fmt.Errorf("gatewise: invalid tenant trusted proxy CIDR")
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		prefixes = append(prefixes, p.Masked())
	}
	header = http.CanonicalHeaderKey(header)
	return func(r *http.Request) (string, error) {
		values := r.Header.Values(header)
		if len(values) == 0 {
			return "", nil
		}
		host, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			host = r.RemoteAddr
		}
		peer, e := netip.ParseAddr(host)
		trusted := false
		if e == nil {
			peer = peer.Unmap()
			for _, p := range prefixes {
				if p.Contains(peer) {
					trusted = true
					break
				}
			}
		}
		if !trusted {
			return "", ErrUntrustedTenantSource
		}
		if len(values) != 1 {
			return "", ErrInvalidTenantIdentity
		}
		id := values[0]
		if id != "" && !tenancy.ValidID(id) {
			return "", ErrInvalidTenantIdentity
		}
		return id, nil
	}, nil
}
