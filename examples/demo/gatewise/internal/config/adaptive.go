package config

import (
	"fmt"
	"github.com/HN026/gatewise/middleware"
	"strconv"
	"strings"
	"time"
)

type rawAdaptive struct {
	MinConcurrent     int    `json:"min_concurrent"`
	InitialConcurrent int    `json:"initial_concurrent"`
	Interval          string `json:"interval"`
	MinSamples        int    `json:"min_samples"`
	TargetLatency     string `json:"target_latency"`
}

func parseAdaptive(name, mode string, raw *rawAdaptive, lookup func(string) (string, bool), maximum, reserved int) (*middleware.AdaptiveConfig, error) {
	prefix := "GATEWISE_SERVICE_" + strings.ToUpper(name) + "_"
	if override, ok := lookup(prefix + "MODE"); ok {
		mode = override
		if mode == "" {
			return nil, fmt.Errorf("service %s mode must be fixed or adaptive", name)
		}
	}
	if mode == "" {
		mode = "fixed"
	}
	if mode == "fixed" {
		return nil, nil
	}
	if mode != "adaptive" {
		return nil, fmt.Errorf("service %s mode must be fixed or adaptive", name)
	}
	if raw == nil {
		raw = &rawAdaptive{}
	}
	a := &middleware.AdaptiveConfig{MinConcurrent: raw.MinConcurrent, InitialConcurrent: raw.InitialConcurrent, MinSamples: raw.MinSamples}
	for _, f := range []struct {
		key string
		dst *int
	}{
		{"MIN_CONCURRENT", &a.MinConcurrent}, {"INITIAL_CONCURRENT", &a.InitialConcurrent}, {"MIN_SAMPLES", &a.MinSamples},
	} {
		if value, ok := lookup(prefix + "ADAPTIVE_" + f.key); ok {
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("service %s adaptive %s must be positive", name, f.key)
			}
			*f.dst = n
		}
	}
	for _, f := range []struct {
		key, value string
		dst        *time.Duration
	}{
		{"INTERVAL", raw.Interval, &a.Interval}, {"TARGET_LATENCY", raw.TargetLatency, &a.TargetLatency},
	} {
		value := f.value
		override, ok := lookup(prefix + "ADAPTIVE_" + f.key)
		if ok {
			value = override
		}
		if value == "" && !ok {
			continue
		}
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("service %s adaptive %s must be a positive duration", name, f.key)
		}
		*f.dst = d
	}
	// The public constructor is side-effect-free: reuse its validation so both
	// deployment modes accept exactly the same adaptive bounds and defaults.
	if _, err := middleware.NewService(middleware.ServiceConfig{MaxConcurrent: maximum, ReservedCritical: reserved, Adaptive: a}); err != nil {
		return nil, fmt.Errorf("service %s: %w", name, err)
	}
	return a, nil
}
