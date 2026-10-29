// Package operation provides transport-neutral, process-local admission.
// Call Begin with trusted metadata, use Permit.Context for nested work, and
// Finish only when the actual operation lifetime ends. Cancellation never
// releases a permit automatically.
package operation

import (
	"fmt"
	decisionpkg "github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/adaptive"
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/tenancy"
	prioritypkg "github.com/HN026/gatewise/priority"
	"log/slog"
)

type Priority string

const (
	Critical   Priority = "critical"
	Background Priority = "background"
)

type Config struct {
	// Decisions is optional. The caller owns observation collection and lifecycle.
	Decisions *decisionpkg.Engine
	// Name identifies this service in rejection responses and logs; default "service".
	Name string
	// MaxConcurrent is required and must be positive.
	MaxConcurrent int
	// ReservedCritical ranges from zero through MaxConcurrent, inclusive.
	ReservedCritical int
	// Logger optionally records rejections. Nil disables library logging.
	Logger *slog.Logger
	// Adaptive nil retains fixed mode.
	Adaptive *AdaptiveConfig
	// Tenants nil preserves service-only admission.
	Tenants    *TenantConfig
	Priorities *prioritypkg.Config
}

// Engine must be constructed with New; its zero value is not usable.
// It is safe for concurrent use. Reuse one instance for operations sharing
// capacity; construct separate instances for independent service budgets.
type Engine struct {
	decisions  *decisionpkg.Engine
	name       string
	limiter    *admission.Limiter
	logger     *slog.Logger
	maximum    int
	adaptive   AdaptiveConfig
	controller *adaptive.Controller
	statistics *statistics
	tenants    *tenancy.Limiter
	priorities *prioritypkg.Policy
}

func New(c Config) (*Engine, error) {
	if c.MaxConcurrent <= 0 {
		return nil, fmt.Errorf("gatewise: MaxConcurrent must be positive")
	}
	if c.ReservedCritical < 0 || c.ReservedCritical > c.MaxConcurrent {
		return nil, fmt.Errorf("gatewise: ReservedCritical must be between zero and MaxConcurrent")
	}
	if c.Name == "" {
		c.Name = "service"
	}
	s := &Engine{name: c.Name, maximum: c.MaxConcurrent, logger: c.Logger, statistics: newStatistics()}
	var err error
	s.tenants, err = newTenants(c.Tenants, c.MaxConcurrent)
	if err != nil {
		return nil, err
	}
	s.decisions = c.Decisions
	if c.Priorities != nil {
		if c.ReservedCritical != 0 {
			return nil, fmt.Errorf("gatewise: use tier reservations instead of ReservedCritical with Priorities")
		}
		s.priorities, err = prioritypkg.New(*c.Priorities, c.MaxConcurrent)
		if err != nil {
			return nil, err
		}
		for _, tier := range s.priorities.Tiers() {
			c.ReservedCritical += tier.Reserved
		}
		s.statistics = newTierStatistics(s.priorities.Tiers())
	}
	initial := c.MaxConcurrent
	if c.Adaptive != nil {
		a, err := normalizeAdaptive(c)
		if err != nil {
			return nil, err
		}
		s.adaptive = a
		initial = a.InitialConcurrent
	}
	s.limiter = admission.New(initial, c.ReservedCritical)
	if s.priorities != nil {
		tiers := []admission.Tier{}
		for _, t := range s.priorities.Tiers() {
			tiers = append(tiers, admission.Tier{Name: admission.Priority(t.Name), Reserved: t.Reserved})
		}
		s.limiter = admission.NewTiers(initial, tiers)
	}
	if c.Adaptive != nil {
		s.controller = adaptive.New(s.limiter, adaptive.Config{Min: s.adaptive.MinConcurrent, Max: c.MaxConcurrent, Interval: s.adaptive.Interval, MinSamples: s.adaptive.MinSamples})
	}
	return s, nil
}
