package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/trust"
	"io"
	"net/http"
	"strings"
)

const CostHeader = "X-Gatewise-Cost"

var ErrUntrustedCostSource = errors.New("gatewise: untrusted operation metadata")
var ErrInvalidCost = errors.New("gatewise: invalid operation metadata")

// CostResolver must derive logical write cost and keys from authenticated,
// validated operation plans. It must be local/fast and must not infer database
// cost from Content-Length. Use request context to avoid parsing bodies twice.
type CostResolver func(*http.Request) (decision.Cost, error)
type costKey struct{}

func resolvedCost(ctx context.Context, c decision.Cost) context.Context {
	return context.WithValue(ctx, costKey{}, c)
}

// InjectCost removes caller values and propagates only server-resolved metadata.
// The receiving ingress must trust this immediate peer and authenticate callers.
func InjectCost(r *http.Request) {
	r.Header.Del(CostHeader)
	if c, ok := r.Context().Value(costKey{}).(decision.Cost); ok {
		data, _ := json.Marshal(c)
		r.Header.Set(CostHeader, string(data))
	}
}

// TrustedCostHeader accepts only the fixed reserved wire header from explicitly
// trusted immediate peers. Ingress must authenticate and overwrite caller values.
func TrustedCostHeader(cidrs []string) (CostResolver, error) {
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("gatewise: cost trusted peers required")
	}
	trusted, err := trust.Peers(cidrs)
	if err != nil {
		return nil, err
	}
	return func(r *http.Request) (decision.Cost, error) {
		values := r.Header.Values(CostHeader)
		if len(values) == 0 {
			return decision.Cost{}, nil
		}
		if !trusted(r) {
			return decision.Cost{}, ErrUntrustedCostSource
		}
		if len(values) != 1 || len(values[0]) > 512 || !strings.HasPrefix(strings.TrimSpace(values[0]), "{") {
			return decision.Cost{}, ErrInvalidCost
		}
		var cost decision.Cost
		d := json.NewDecoder(strings.NewReader(values[0]))
		d.DisallowUnknownFields()
		if d.Decode(&cost) != nil || d.Decode(new(any)) != io.EOF {
			return decision.Cost{}, ErrInvalidCost
		}
		return cost, nil
	}, nil
}
