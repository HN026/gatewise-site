package middleware

import (
	"errors"
	"fmt"
	"github.com/HN026/gatewise/internal/tenancy"
	"github.com/HN026/gatewise/operation"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type TenantPolicy = operation.TenantPolicy

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

// TenantSharingConfig enables lending beyond configured tenant shares. The
// existing MaxConcurrent/Limits become target shares only when Sharing is set.
type TenantSharingConfig = operation.TenantSharingConfig
type TenantConfig struct {
	Sharing                       *TenantSharingConfig
	MaxConcurrent                 int
	Limits                        map[string]int // Optional overrides; copied and hashed at construction.
	MaxTracked                    int            // Default 1024 identified tenants, plus two bounded fallback buckets.
	IdleTTL                       time.Duration  // Default 5m; only inactive entries expire.
	MissingPolicy, OverflowPolicy TenantPolicy   // Default reject; shared uses a bounded bucket.
	Resolver                      TenantResolver
}
type TenantState = operation.TenantState

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
