// Package metrics exports Gatewise service statistics to a caller-owned
// Prometheus registry. It never registers with global/default registries.
package metrics

import (
	"fmt"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

type collector struct {
	tenantLimit, tenantTracked, tenantTrackingLimit, tenantActive, tenantExpired                                    *prometheus.Desc
	controllerSamples, controllerRatio, controllerFailures, controllerTime, controllerDecision, controllerSaturated *prometheus.Desc
	service                                                                                                         *middleware.Service
	limit, minimum, maximum, reserved, active, info, admitted, rejected, completed, duration, adjustments           *prometheus.Desc
}

// Register adds one service to registry. Service names must be unique within that
// registry. The caller owns the HTTP handler and can use promhttp.HandlerFor.
func Register(registry prometheus.Registerer, service *middleware.Service) error {
	if registry == nil || service == nil {
		return fmt.Errorf("gatewise metrics: registry and service are required")
	}
	serviceName := service.Statistics().Name
	desc := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc("gatewise_"+name, help, labels, prometheus.Labels{"service": serviceName})
	}
	c := &collector{service: service,
		tenantLimit:         desc("tenant_concurrency_limit", "Default per-tenant concurrency limit."),
		tenantTracked:       desc("tenants_tracked", "Identified tenants currently retained in the bounded registry."),
		tenantTrackingLimit: desc("tenants_tracking_limit", "Maximum identified tenant entries."),
		tenantActive:        desc("tenant_requests_in_flight", "Tenant leases including brief service-admission reservations; never labeled by identity.", "bucket"),
		tenantExpired:       desc("tenant_entries_expired_total", "Inactive identified tenant entries expired."),
		controllerSamples:   desc("controller_last_window_samples", "Completed samples in the last evaluated adaptive window."),
		controllerRatio:     desc("controller_last_p90_ratio", "Bucket-estimated p90 handler duration divided by latency target in the last evaluated window."),
		controllerFailures:  desc("controller_last_failure_fraction", "Failed sample fraction in the last evaluated adaptive window."),
		controllerTime:      desc("controller_last_evaluation_timestamp_seconds", "Unix timestamp of the last adaptive evaluation; zero before evaluation."),
		controllerDecision:  desc("controller_last_decision", "Last adaptive decision, including hold and not_evaluated; bounded labels.", "decision", "reason"),
		controllerSaturated: desc("controller_last_window_saturated", "Whether saturation was observed in the last evaluated adaptive window."),
		limit:               desc("concurrency_limit", "Current admission limit."),
		minimum:             desc("concurrency_min_limit", "Configured lower admission bound."),
		maximum:             desc("concurrency_max_limit", "Configured admission ceiling."),
		reserved:            desc("concurrency_reserved_critical", "Capacity unavailable to background work."),
		active:              desc("requests_in_flight", "Currently admitted work; may exceed a reduced limit while draining.", "priority"),
		info:                desc("service_info", "Service configuration information.", "mode"),
		admitted:            desc("requests_admitted_total", "Requests admitted to handlers.", "priority"),
		rejected:            desc("requests_rejected_total", "Requests rejected before reaching handlers.", "priority", "reason"),
		completed:           desc("requests_completed_total", "Admitted requests that ended.", "priority", "outcome", "status_class"),
		duration:            desc("request_duration_seconds", "Duration of admitted handler work, excluding admission rejections.", "priority", "outcome"),
		adjustments:         desc("limit_adjustments_total", "Adaptive limit changes.", "direction", "reason"),
	}
	return registry.Register(c)
}

func (c *collector) Describe(out chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.limit, c.minimum, c.maximum, c.reserved, c.active, c.info, c.admitted, c.rejected, c.completed, c.duration, c.adjustments, c.controllerSamples, c.controllerRatio, c.controllerFailures, c.controllerTime, c.controllerDecision, c.controllerSaturated, c.tenantLimit, c.tenantTracked, c.tenantTrackingLimit, c.tenantActive, c.tenantExpired} {
		out <- desc
	}
}

func (c *collector) Collect(out chan<- prometheus.Metric) {
	s := c.service.Statistics()
	gauge := func(desc *prometheus.Desc, value float64, labels ...string) {
		out <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
	counter := func(desc *prometheus.Desc, value uint64, labels ...string) {
		out <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(value), labels...)
	}
	if s.Tenants.Enabled {
		t := s.Tenants
		gauge(c.tenantLimit, float64(t.MaxConcurrent))
		gauge(c.tenantTracked, float64(t.Tracked))
		gauge(c.tenantTrackingLimit, float64(t.MaxTracked))
		gauge(c.tenantActive, float64(t.IdentifiedActive), "identified")
		gauge(c.tenantActive, float64(t.MissingActive), "missing")
		gauge(c.tenantActive, float64(t.OverflowActive), "overflow")
		counter(c.tenantExpired, t.EntriesExpired)
	}
	if s.Controller.Enabled {
		e := s.Controller
		gauge(c.controllerSamples, float64(e.Samples))
		gauge(c.controllerRatio, e.P90Ratio)
		gauge(c.controllerFailures, e.FailureFraction)
		timestamp := 0.0
		if !e.EvaluatedAt.IsZero() {
			timestamp = float64(e.EvaluatedAt.UnixNano()) / 1e9
		}
		gauge(c.controllerTime, timestamp)
		gauge(c.controllerDecision, 1, e.Decision, e.Reason)
		saturated := 0.0
		if e.Saturated {
			saturated = 1
		}
		gauge(c.controllerSaturated, saturated)
	}
	gauge(c.limit, float64(s.State.Limit))
	gauge(c.minimum, float64(s.State.MinConcurrent))
	gauge(c.maximum, float64(s.State.MaxConcurrent))
	gauge(c.reserved, float64(s.State.ReservedCritical))
	gauge(c.info, 1, s.State.Mode)
	gauge(c.active, float64(s.State.Active-s.State.Background), "critical")
	gauge(c.active, float64(s.State.Background), "background")
	classes := [7]string{"1xx", "2xx", "3xx", "4xx", "5xx", "other", "none"}
	for _, p := range s.Priorities {
		priority := string(p.Priority)
		counter(c.admitted, p.Admitted, priority)
		counter(c.rejected, p.RejectedConcurrency, priority, "concurrency_limit")
		counter(c.rejected, p.RejectedReservation, priority, "critical_capacity_reserved")
		if s.Tenants.Enabled {
			for _, v := range []struct {
				reason string
				n      uint64
			}{{"tenant_concurrency_limit", p.RejectedTenant}, {"tenant_registry_full", p.RejectedTenantRegistry}, {"tenant_identity_missing", p.RejectedTenantMissing}, {"tenant_identity_invalid", p.RejectedTenantInvalid}, {"tenant_identity_untrusted", p.RejectedTenantUntrusted}} {
				counter(c.rejected, v.n, priority, v.reason)
			}
		}
		for oi, o := range p.Outcomes {
			if oi == 0 {
				for i := 0; i < 6; i++ {
					counter(c.completed, o.StatusClasses[i], priority, o.Outcome, classes[i])
				}
			} else {
				counter(c.completed, o.StatusClasses[6], priority, o.Outcome, "none")
			}
			buckets := make(map[float64]uint64, len(s.DurationBounds))
			for i, bound := range s.DurationBounds {
				buckets[bound] = o.DurationBuckets[i]
			}
			out <- prometheus.MustNewConstHistogram(c.duration, o.Count, o.DurationSum, buckets, priority, o.Outcome)
		}
	}
	reasons := [3]string{"latency_pressure", "server_errors", "healthy_saturation"}
	for direction, counts := range s.Adjustments {
		label := "decrease"
		if direction == 1 {
			label = "increase"
		}
		for i, count := range counts {
			counter(c.adjustments, count, label, reasons[i])
		}
	}
}
