package operation

import (
	"fmt"
	"github.com/HN026/gatewise/internal/tenancy"
	"time"
)

type TenantPolicy string

const (
	TenantReject TenantPolicy = "reject"
	TenantShared TenantPolicy = "shared"
)

// TenantSharingConfig enables lending beyond configured tenant shares. The
// existing MaxConcurrent/Limits become target shares only when Sharing is set.
type TenantSharingConfig struct {
	MaxConcurrent int            // Borrowing ceiling; zero selects the service maximum.
	Limits        map[string]int // Optional per-identity borrowing ceilings.
	DemandTTL     time.Duration  // Default 500ms; positive and no greater than IdleTTL.
}
type TenantConfig struct {
	Sharing                       *TenantSharingConfig
	MaxConcurrent                 int
	Limits                        map[string]int // Optional overrides; copied and hashed at construction.
	MaxTracked                    int            // Default 1024 identified tenants, plus two bounded fallback buckets.
	IdleTTL                       time.Duration  // Default 5m; only inactive entries expire.
	MissingPolicy, OverflowPolicy TenantPolicy   // Default reject; shared uses a bounded bucket.
}
type TenantState struct {
	SharingEnabled, BorrowingPaused                                                     bool
	DemandingTenants, BorrowedActive                                                    int
	DemandShares                                                                        int64
	Enabled                                                                             bool
	MaxConcurrent, MaxTracked, Tracked, IdentifiedActive, MissingActive, OverflowActive int
	EntriesExpired                                                                      uint64
}

func newTenants(c *TenantConfig, maximum int) (*tenancy.Limiter, error) {
	if c == nil {
		return nil, nil
	}
	copy := *c
	if copy.MaxConcurrent <= 0 || copy.MaxConcurrent > maximum {
		return nil, fmt.Errorf("gatewise: tenant MaxConcurrent must be between 1 and service MaxConcurrent")
	}
	if copy.MaxTracked == 0 {
		copy.MaxTracked = 1024
	}
	if copy.IdleTTL == 0 {
		copy.IdleTTL = 5 * time.Minute
	}
	if copy.MaxTracked < 1 || copy.MaxTracked > 1000000 || copy.IdleTTL < 0 {
		return nil, fmt.Errorf("gatewise: tenant MaxTracked must be 1..1000000 and IdleTTL positive")
	}
	for _, p := range []TenantPolicy{copy.MissingPolicy, copy.OverflowPolicy} {
		if p != "" && p != TenantReject && p != TenantShared {
			return nil, fmt.Errorf("gatewise: tenant policies must be reject or shared")
		}
	}
	if len(copy.Limits) > copy.MaxTracked {
		return nil, fmt.Errorf("gatewise: tenant overrides exceed MaxTracked")
	}
	for id, limit := range copy.Limits {
		if !tenancy.ValidID(id) || limit < 1 || limit > maximum {
			return nil, fmt.Errorf("gatewise: invalid tenant override identity or limit")
		}
	}
	var sharing *tenancy.Sharing
	if copy.Sharing != nil {
		v := *copy.Sharing
		if v.MaxConcurrent == 0 {
			v.MaxConcurrent = maximum
		}
		if v.DemandTTL == 0 {
			v.DemandTTL = 500 * time.Millisecond
		}
		if maximum > 1000000000 || v.MaxConcurrent < copy.MaxConcurrent || v.MaxConcurrent > maximum || v.DemandTTL <= 0 || v.DemandTTL > copy.IdleTTL || len(v.Limits) > copy.MaxTracked {
			return nil, fmt.Errorf("gatewise: invalid sharing ceiling/demand TTL/override count; service ceiling must not exceed 1e9")
		}
		for id, n := range v.Limits {
			base := copy.MaxConcurrent
			if override, ok := copy.Limits[id]; ok {
				base = override
			}
			if !tenancy.ValidID(id) || n < base || n > maximum {
				return nil, fmt.Errorf("gatewise: borrowing ceilings must be between each tenant share and the service maximum")
			}
		}
		for id, base := range copy.Limits {
			ceiling := v.MaxConcurrent
			if n, ok := v.Limits[id]; ok {
				ceiling = n
			}
			if base > ceiling {
				return nil, fmt.Errorf("gatewise: tenant share exceeds its borrowing ceiling")
			}
		}
		sharing = &tenancy.Sharing{Capacity: maximum, MaxConcurrent: v.MaxConcurrent, Limits: v.Limits, DemandTTL: v.DemandTTL}
	}
	return tenancy.New(tenancy.Config{Sharing: sharing, Limit: copy.MaxConcurrent, MaxTracked: copy.MaxTracked, IdleTTL: copy.IdleTTL, MissingShared: copy.MissingPolicy == TenantShared, OverflowShared: copy.OverflowPolicy == TenantShared, Limits: copy.Limits}), nil
}
func (s *Engine) TenantSnapshot() TenantState {
	if s.tenants == nil {
		return TenantState{}
	}
	v := s.tenants.Snapshot(time.Now())
	return TenantState{Enabled: true, MaxConcurrent: v.Limit, MaxTracked: v.MaxTracked, Tracked: v.Tracked, IdentifiedActive: v.IdentifiedActive, MissingActive: v.MissingActive, OverflowActive: v.OverflowActive, EntriesExpired: v.Expired, SharingEnabled: v.SharingEnabled, BorrowingPaused: v.BorrowingPaused, DemandingTenants: v.Demanding, DemandShares: v.DemandShares, BorrowedActive: v.BorrowedActive}
}
