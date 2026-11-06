// Package middleware provides HTTP admission control for Go services. A shared
// Service applies one total limit and critical reservation across its handlers.
// It does not start servers, read environment files, or install global handlers.
package middleware

import (
	"encoding/json"
	"fmt"
	"github.com/felixge/httpsnoop"
	"github.com/HN026/gatewise/internal/adaptive"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/HN026/gatewise/internal/admission"
)

type Priority string

const (
	Critical   Priority = "critical"
	Background Priority = "background"
)

type ServiceConfig struct {
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
}

// Service must be constructed with NewService; its zero value is not usable.
// It is safe for concurrent use. Reuse one instance for handlers sharing
// capacity; construct separate instances for independent service budgets.
type Service struct {
	name       string
	limiter    *admission.Limiter
	logger     *slog.Logger
	maximum    int
	adaptive   AdaptiveConfig
	controller *adaptive.Controller
	statistics *statistics
}

func NewService(c ServiceConfig) (*Service, error) {
	if c.MaxConcurrent <= 0 {
		return nil, fmt.Errorf("gatewise: MaxConcurrent must be positive")
	}
	if c.ReservedCritical < 0 || c.ReservedCritical > c.MaxConcurrent {
		return nil, fmt.Errorf("gatewise: ReservedCritical must be between zero and MaxConcurrent")
	}
	if c.Name == "" {
		c.Name = "service"
	}
	s := &Service{name: c.Name, maximum: c.MaxConcurrent, logger: c.Logger, statistics: newStatistics()}
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
	if c.Adaptive != nil {
		s.controller = adaptive.New(s.limiter, adaptive.Config{Min: s.adaptive.MinConcurrent, Max: c.MaxConcurrent, Interval: s.adaptive.Interval, MinSamples: s.adaptive.MinSamples})
	}
	return s, nil
}

// Middleware wraps next with a route-configured priority. Invalid priorities or
// nil handlers panic at registration time. Caller headers cannot change priority.
// The slot is held until next returns (or panics); work outliving the handler is
// outside that lifetime. Cancellation is forwarded through the request context.
// There is no waiting queue, preemption, automatic retry, or process-wide budget.
func (s *Service) Middleware(priority Priority, next http.Handler, options ...HandlerOption) http.Handler {
	if priority != Critical && priority != Background {
		panic("gatewise: priority must be Critical or Background")
	}
	if next == nil {
		panic("gatewise: handler must not be nil")
	}
	o := handlerOptions{targetLatency: s.adaptive.TargetLatency}
	for _, option := range options {
		if option == nil {
			panic("gatewise: nil handler option")
		}
		option(&o)
	}
	p := admission.Priority(priority)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		start := time.Now()
		var decision admission.Decision
		var generation uint64
		if s.controller != nil {
			decision, generation = s.controller.Begin(p, start)
		} else {
			decision = s.limiter.TryAcquire(p)
		}
		s.statistics.admission(priority, decision)
		state := s.Snapshot()
		w.Header().Set("X-Gatewise-Limit", strconv.Itoa(state.Limit))
		w.Header().Set("X-Gatewise-Mode", state.Mode)
		if decision != admission.Admitted {
			if s.logger != nil {
				s.logger.Warn("request rejected", "service", s.name, "priority", priority, "reason", decision)
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "service_overloaded", "service": s.name, "priority": string(priority), "reason": string(decision)})
			return
		}

		completed := false
		var metrics httpsnoop.Metrics
		defer func() {
			elapsed := time.Since(start)
			canceled := r.Context().Err() != nil
			if s.controller != nil {
				change := s.controller.End(p, generation, time.Now(), elapsed, o.targetLatency, !completed || metrics.Code >= 500, canceled)
				if change != nil {
					s.statistics.adjustment(change.Previous, change.Limit, change.Reason)
					if s.logger != nil {
						s.logger.Info("concurrency limit adjusted", "service", s.name, "previous", change.Previous, "limit", change.Limit, "reason", change.Reason, "p90_ratio", change.P90Ratio, "samples", change.Samples)
					}
				}
			} else {
				s.limiter.Release(p)
			}
			s.statistics.complete(priority, metrics.Code, completed, canceled, elapsed)
		}()
		if r.Context().Err() != nil {
			return
		}
		metrics = httpsnoop.CaptureMetrics(next, w, r)
		completed = true
	})
}
