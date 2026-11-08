// Package metrics exports Gatewise service statistics to a caller-owned
// Prometheus registry. It never registers with global/default registries.
package metrics

import (
	"fmt"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

type collector struct {
	sharingDemand, sharingShares, sharingPause, sharingActive, sharingAdmissions                                    *prometheus.Desc
	resolutions                                                                                                     *prometheus.Desc
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
		sharingDemand:       desc("tenant_sharing_demanding_tenants", "Identified tenants with active or recent demand."),
		sharingShares:       desc("tenant_sharing_demand_shares", "Configured shares of tenants with active or recent demand."),
		sharingPause:        desc("tenant_sharing_borrowing_paused", "One while lending is paused after below-share capacity rejection."),
		sharingActive:       desc("tenant_sharing_requests_in_flight", "Reservations classified at acquisition, including brief uncommitted leases.", "allocation"),
		sharingAdmissions:   desc("tenant_sharing_admissions_total", "Successful service admissions by tenant allocation class.", "allocation"),
		resolutions:         desc("priority_resolutions_total", "Resolved configured priority and bounded source.", "priority", "source"),
		tenantLimit:         desc("tenant_concurrency_limit", "Default tenant hard cap, or configured target share when sharing is enabled."),
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
	for _, desc := range []*prometheus.Desc{c.sharingDemand, c.sharingShares, c.sharingPause, c.sharingActive, c.sharingAdmissions, c.resolutions, c.limit, c.minimum, c.maximum, c.reserved, c.active, c.info, c.admitted, c.rejected, c.completed, c.duration, c.adjustments, c.controllerSamples, c.controllerRatio, c.controllerFailures, c.controllerTime, c.controllerDecision, c.controllerSaturated, c.tenantLimit, c.tenantTracked, c.tenantTrackingLimit, c.tenantActive, c.tenantExpired} {
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
	if s.Tenants.SharingEnabled {
		t := s.Tenants
		gauge(c.sharingDemand, float64(t.DemandingTenants))
		gauge(c.sharingShares, float64(t.DemandShares))
		paused := 0.
		if t.BorrowingPaused {
			paused = 1
		}
		gauge(c.sharingPause, paused)
		for i, kind := range []string{"share", "borrowed", "fallback"} {
			active := t.IdentifiedActive - t.BorrowedActive
			if i == 1 {
				active = t.BorrowedActive
			}
			if i == 2 {
				active = t.MissingActive + t.OverflowActive
			}
			gauge(c.sharingActive, float64(active), kind)
			counter(c.sharingAdmissions, s.TenantAllocations[i], kind)
		}
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
	for p, n := range s.TierActive {
		gauge(c.active, float64(n), string(p))
	}
	if s.PriorityEnabled {
		for _, v := range s.Resolutions {
			counter(c.resolutions, v.Count, string(v.Priority), v.Source)
		}
	}
	classes := [7]string{"1xx", "2xx", "3xx", "4xx", "5xx", "other", "none"}
	for _, p := range s.Tiers {
		priority := string(p.Priority)
		counter(c.admitted, p.Admitted, priority)
		if s.PriorityEnabled {
			for _, v := range []struct {
				reason string
				n      uint64
			}{{"tier_capacity_reserved", p.RejectedTier}, {"cohort_limited", p.RejectedCohort}, {"priority_metadata_invalid", p.RejectedPriorityInvalid}, {"priority_metadata_untrusted", p.RejectedPriorityUntrusted}} {
				counter(c.rejected, v.n, priority, v.reason)
			}
		}
		counter(c.rejected, p.RejectedConcurrency, priority, "concurrency_limit")
		counter(c.rejected, p.RejectedReservation, priority, "critical_capacity_reserved")
		if s.Tenants.SharingEnabled {
			counter(c.rejected, p.RejectedTenantShare, priority, "tenant_share_reserved")
		}
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
