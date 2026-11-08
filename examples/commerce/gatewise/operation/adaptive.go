package operation

import (
	"fmt"

	"time"
)

// AdaptiveConfig enables request-driven adjustment when attached to Config.
// Zero fields select conservative defaults. MaxConcurrent remains the ceiling.
type AdaptiveConfig struct {
	MinConcurrent     int
	InitialConcurrent int
	Interval          time.Duration
	MinSamples        int
	TargetLatency     time.Duration
}

func normalizeAdaptive(c Config) (AdaptiveConfig, error) {
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

// ServiceState is a read-only snapshot. Active may temporarily exceed Limit after
// a reduction while previously admitted requests drain; they are not interrupted.
type ServiceState struct {
	Mode                                                                      string
	Limit, Active, Background, ReservedCritical, MinConcurrent, MaxConcurrent int
}

func (s *Engine) Snapshot() ServiceState {
	state := s.limiter.Snapshot()
	mode := "fixed"
	min := s.maximum
	if s.controller != nil {
		mode = "adaptive"
		min = s.adaptive.MinConcurrent
	}
	return ServiceState{Mode: mode, Limit: state.Limit, Active: state.Active, Background: state.Background, ReservedCritical: state.ReservedCritical, MinConcurrent: min, MaxConcurrent: s.maximum}
}

// ControllerState contains the last evaluated adaptive window. EvaluatedAt is
// zero until both interval and sample requirements are met. Decision is one of
// not_evaluated, hold, increase or decrease. These are observations, not controls.
type ControllerState struct {
	Enabled                   bool
	EvaluatedAt               time.Time
	Samples                   int
	P90Ratio, FailureFraction float64
	PreviousLimit, Limit      int
	Saturated                 bool
	Decision, Reason          string
}

func (s *Engine) ControllerSnapshot() ControllerState {
	if s.controller == nil {
		return ControllerState{Decision: "not_evaluated", Reason: "fixed_mode"}
	}
	e := s.controller.Snapshot()
	state := ControllerState{Enabled: true, EvaluatedAt: e.EvaluatedAt, Samples: e.Samples,
		P90Ratio: e.P90Ratio, FailureFraction: e.FailureFraction,
		PreviousLimit: e.PreviousLimit, Limit: e.Limit, Saturated: e.Saturated,
		Decision: e.Decision, Reason: e.Reason}
	if e.EvaluatedAt.IsZero() {
		state.Decision = "not_evaluated"
		state.Reason = "insufficient_samples_or_interval"
	}
	return state
}
