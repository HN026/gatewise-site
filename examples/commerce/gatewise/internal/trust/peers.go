// Package trust validates the immediate transport peer; forwarded headers never
// establish identity. This helper is shared by metadata resolvers.
package trust

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

func Peers(cidrs []string) (func(*http.Request) bool, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		p, e := netip.ParsePrefix(cidr)
		if e != nil {
			return nil, fmt.Errorf("gatewise: invalid trusted peer CIDR")
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		prefixes = append(prefixes, p.Masked())
	}
	return func(r *http.Request) bool {
		host, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			host = r.RemoteAddr
		}
		peer, e := netip.ParseAddr(host)
		if e != nil {
			return false
		}
		peer = peer.Unmap()
		for _, p := range prefixes {
			if p.Contains(peer) {
				return true
			}
		}
		return false
	}, nil
}
