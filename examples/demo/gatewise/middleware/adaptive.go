package middleware

import (
	"fmt"
	"time"
)

// AdaptiveConfig enables request-driven adjustment when attached to ServiceConfig.
// Zero fields select conservative defaults. MaxConcurrent remains the ceiling.
type AdaptiveConfig struct {
	MinConcurrent     int
	InitialConcurrent int
	Interval          time.Duration
	MinSamples        int
	TargetLatency     time.Duration
}

func normalizeAdaptive(c ServiceConfig) (AdaptiveConfig, error) {
	a := *c.Adaptive
	if c.ReservedCritical >= c.MaxConcurrent {
		return a, fmt.Errorf("gatewise: adaptive mode needs nonreserved capacity")
	}
	if a.MinConcurrent == 0 {
		a.MinConcurrent = c.ReservedCritical + 1
	}
	if a.InitialConcurrent == 0 {
		a.InitialConcurrent = a.MinConcurrent
	}
	if a.Interval == 0 {
		a.Interval = time.Second
	}
	if a.MinSamples == 0 {
		a.MinSamples = 10
	}
	if a.TargetLatency == 0 {
		a.TargetLatency = 100 * time.Millisecond
	}
	if a.MinConcurrent <= c.ReservedCritical || a.MinConcurrent > c.MaxConcurrent {
		return a, fmt.Errorf("gatewise: adaptive minimum must exceed ReservedCritical and not exceed MaxConcurrent")
	}
	if a.InitialConcurrent < a.MinConcurrent || a.InitialConcurrent > c.MaxConcurrent {
		return a, fmt.Errorf("gatewise: adaptive initial limit must be within minimum and maximum")
	}
	if a.Interval <= 0 || a.MinSamples < 1 || a.TargetLatency <= 0 {
		return a, fmt.Errorf("gatewise: adaptive interval, minimum samples and target latency must be positive")
	}
	return a, nil
}

type handlerOptions struct{ targetLatency time.Duration }
type HandlerOption func(*handlerOptions)

// WithLatencyTarget gives this handler its own budget so naturally slower work
// is not compared directly to fast handlers sharing the same service capacity.
func WithLatencyTarget(target time.Duration) HandlerOption {
	if target <= 0 {
		panic("gatewise: latency target must be positive")
	}
	return func(o *handlerOptions) { o.targetLatency = target }
}

// ServiceState is a read-only snapshot. Active may temporarily exceed Limit after
// a reduction while previously admitted requests drain; they are not interrupted.
type ServiceState struct {
	Mode                                                                      string
	Limit, Active, Background, ReservedCritical, MinConcurrent, MaxConcurrent int
}

func (s *Service) Snapshot() ServiceState {
	state := s.limiter.Snapshot()
	mode := "fixed"
	min := s.maximum
	if s.controller != nil {
		mode = "adaptive"
		min = s.adaptive.MinConcurrent
	}
	return ServiceState{Mode: mode, Limit: state.Limit, Active: state.Active, Background: state.Background, ReservedCritical: state.ReservedCritical, MinConcurrent: min, MaxConcurrent: s.maximum}
}
