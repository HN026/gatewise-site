export const navigation = [
  { id: 'overview', label: 'Home', number: '00', description: 'HTTP admission control: proxy and Go middleware.' },
  { id: 'problem', label: 'Problem & design', number: '01', description: 'HTTP basics, overload, priority reservation and adaptive concurrency.' },
  { id: 'installation', label: 'Installation & deployment', number: '02', description: 'Build the proxy or integrate Go middleware.' },
  { id: 'examples', label: 'Examples', number: '03', description: 'Run the multi-service demo and API checks.' },
  { id: 'benchmarks', label: 'Benchmarks', number: '04', description: 'Measured latency, throughput and resource costs.' },
  { id: 'metrics', label: 'Metrics', number: '05', description: 'Prometheus integration and Grafana dashboard.' },
  { id: 'reference', label: 'Reference', number: '06', description: 'Configuration and public Go API.' },
  { id: 'versions', label: 'Versions', number: '07', description: 'Release notes, version requirements, downloads and upgrade guidance.' },
] as const;
export type PageId = typeof navigation[number]['id'];

export const sources = [
  { title: 'From static rate-limiting to intelligent load management', organization: 'Uber Engineering', year: '2026', url: 'https://www.uber.com/us/en/blog/from-static-rate-limiting-to-intelligent-load-management/', description: 'Database overload signals, priority-aware shedding and tenant fairness.' },
  { title: 'Introducing QALM', organization: 'Uber Engineering', year: '2018', url: 'https://www.uber.com/us/en/blog/qalm-qos-load-management-framework/', description: 'A reference for request criticality and protecting important traffic during overload.' },
  { title: 'Cinnamon: a mean load shedder', organization: 'Uber Engineering', year: '2023', url: 'https://www.uber.com/us/en/blog/cinnamon-using-century-old-tech-to-build-a-mean-load-shedder/', description: 'A reference for adaptive capacity and graceful degradation. Gatewise uses its own budget-based controller.' },
];

export const middlewareExample = `package main

import (
    "log"
    "net/http"
    "time"

    "github.com/HN026/gatewise/middleware"
)

func main() {
    guard, err := middleware.NewService(middleware.ServiceConfig{
        // Example values: tune these limits for your service.
        Name: "orders", MaxConcurrent: 20, ReservedCritical: 5,
    })
    if err != nil { log.Fatal(err) }

    mux := http.NewServeMux()
    mux.Handle("POST /checkout", guard.Middleware(
        middleware.Critical, http.HandlerFunc(checkout),
    ))
    mux.Handle("POST /reindex", guard.Middleware(
        middleware.Background, http.HandlerFunc(reindex),
    ))

    server := &http.Server{
        Addr: "127.0.0.1:8080", Handler: mux,
        ReadHeaderTimeout: 5 * time.Second,
    }
    log.Fatal(server.ListenAndServe())
}

func checkout(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
}
func reindex(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
}`;

export const proxyExample = `{
  "listen_addr": "127.0.0.1:8080",
  "services": {
    "orders": {
      "upstream_url": "http://127.0.0.1:9090",
      "max_concurrent": 20,
      "reserved_critical": 5
    }
  },
  "routes": [
    { "path_prefix": "/orders", "service": "orders",
      "strip_prefix": true, "priority": "critical" },
    { "path_prefix": "/orders/background", "service": "orders",
      "strip_prefix": true, "priority": "background" }
  ]
}`;

export const adaptiveExample = `guard, err := middleware.NewService(middleware.ServiceConfig{
    Name: "orders", MaxConcurrent: 20, ReservedCritical: 5,
    Adaptive: &middleware.AdaptiveConfig{
        MinConcurrent: 6, InitialConcurrent: 12,
        Interval: time.Second, MinSamples: 10,
        TargetLatency: 100 * time.Millisecond,
    },
})
if err != nil { log.Fatal(err) }

// Different synchronous handlers can have different budgets.
reports := guard.Middleware(middleware.Critical, handler,
    middleware.WithLatencyTarget(time.Second))`;

export const metricsExample = `registry := prometheus.NewRegistry()
if err := metrics.Register(registry, guard); err != nil {
    log.Fatal(err)
}

// Keep monitoring outside guarded business routes.
mux.Handle("GET /metrics", promhttp.HandlerFor(registry,
    promhttp.HandlerOpts{}))`;

export const configuration = [
 ['decisions.budgets[]','Disabled','At most 32 named resource budgets; positive max_write_bytes and/or keys.'],
 ['decisions.budgets[].resource','All handlers','Server-owned resource tag; empty matches all requests sharing this engine.'],
 ['decisions.budgets[].keys','Disabled','Per-key concurrency, bounded tracking/idle TTL and explicit missing/overflow policies.'],
 ['decisions.cost_metadata.trusted_peers','Required for proxy budgets','Trusted immediate ingress CIDRs for reserved X-Gatewise-Cost JSON; forwarded headers do not establish trust.'],
  ['services.<name>.decisions', 'Disabled', 'Optional shared signal engine; omit to retain existing integration behaviour.'],
  ['decisions.mode', 'enforce', 'observe records signal proposals; enforce applies them. Existing hard controls always run.'],
  ['decisions.signals[]', 'Required when enabled', '1–32 uniquely named rules: scope, threshold, max_age, fallback, pressure effect and HTTP source.'],
  ['decisions.signals[].scope', 'service', 'service, resource with a server-owned key, or actor with a trusted tenant key.'],
  ['decisions.signals[].max_age', 'Required', 'Positive freshness bound ≤24h and ≥source interval + timeout.'],
  ['decisions.signals[].on_unknown', 'allow', 'allow, limit with unknown_limit, or reject for missing/unknown/stale observations.'],
  ['decisions.signals[].pressure', 'Required', 'Exactly one positive max_concurrent or reject: true. Strictest matching effect wins.'],
  ['decisions.signals[].source', 'Required in proxy', 'Operator URL, interval 10ms–1h and positive timeout ≤interval. Validation remains offline.'],
  ['routes[].resource', 'None', 'Server-owned signal resource scope; cannot be selected by caller metadata.'],
  ['services.<name>.upstream_url', 'Required', 'Absolute HTTP(S) upstream. Supports ${ENV_VAR} substitution.'],
  ['services.<name>.max_concurrent', '100', 'Positive ceiling on active synchronous requests.'],
  ['services.<name>.reserved_critical', '0', 'Capacity unavailable to background work; between 0 and the ceiling.'],
  ['services.<name>.mode', 'fixed', 'Choose fixed or adaptive. Fixed keeps a constant limit.'],
  ['adaptive.min_concurrent', 'reservation + 1', 'Adaptive floor; must exceed the critical reservation.'],
  ['adaptive.initial_concurrent', 'minimum', 'Starting admission limit, within the configured floor and ceiling.'],
  ['adaptive.interval', '1s', 'Minimum elapsed interval before evaluating feedback.'],
  ['adaptive.min_samples', '10', 'Minimum completed samples required for feedback.'],
  ['adaptive.target_latency', '100ms', 'Default synchronous handler duration budget.'],
  ['routes[].path_prefix', 'Required', 'Longest segment-prefix match selects a named service.'],
  ['routes[].priority', 'critical', 'Route-owned criticality: critical or background.'],
  ['routes[].strip_prefix', 'false', 'Remove the matched prefix before forwarding.'],
  ['routes[].latency_target', 'Service budget', 'Override the latency budget for this route.'],
  ['dial_timeout', '2s', 'Upstream connection and TLS handshake timeout.'],
  ['response_header_timeout', '2s', 'Wait for upstream headers after writing the request.'],
  ['shutdown_timeout', '5s', 'Graceful drain timeout before closing connections.'],
] as const;

export const metricFamilies = [
 ['gatewise_resource_write_bytes_in_flight','Gauge','Reserved planned logical write bytes by configured budget.'],
 ['gatewise_resource_reservations_in_flight','Gauge','Resource reservations including short pre-admission leases.'],
 ['gatewise_resource_keys_tracked','Gauge','Bounded retained hashed-key registry entries, never per-key labels.'],
 ['gatewise_resource_rejection_proposals_total','Counter','Resource-policy rejection proposals; observe retains actual work accounting.'],
  ['gatewise_decision_engine_info','Gauge','Optional signal engine mode: enforce or observe.'],
  ['gatewise_signal_proposals_total','Counter','allow/limit/reject proposals; limit is not a counterfactual rejection count.'],
  ['gatewise_signal_state','Gauge','Cached signal state: missing, healthy, pressure, unknown or stale.'],
  ['gatewise_signal_value / gatewise_signal_threshold','Gauge','Last measurement and configured threshold; inspect freshness/state too.'],
  ['gatewise_signal_observed_timestamp_seconds','Gauge','Measurement time, zero before first observation.'],
  ['gatewise_signal_observations_total','Counter','Accepted cache updates for a configured signal.'],
  ['gatewise_signal_observations_dropped_total','Counter','Discarded invalid, out-of-order or future samples.'],
  ['gatewise_signal_collection_failures_total','Counter','Collection error, timeout, panic or busy worker, with bounded labels.'],
  ['gatewise_concurrency_limit', 'Gauge', 'Current admission limit for each service.'],
  ['gatewise_concurrency_min_limit', 'Gauge', 'Configured lower admission bound.'],
  ['gatewise_concurrency_max_limit', 'Gauge', 'Configured admission ceiling.'],
  ['gatewise_concurrency_reserved_critical', 'Gauge', 'Capacity unavailable to background work.'],
  ['gatewise_service_info', 'Gauge', 'Fixed or adaptive mode for the service.'],
  ['gatewise_requests_in_flight', 'Gauge', 'Admitted work currently running, by priority.'],
  ['gatewise_requests_admitted_total', 'Counter', 'Accepted work, split into critical and background.'],
  ['gatewise_requests_rejected_total', 'Counter', 'Rejected arrivals, with bounded reason labels.'],
  ['gatewise_requests_completed_total', 'Counter', 'Completed, canceled or panicked work and status class.'],
  ['gatewise_request_duration_seconds', 'Histogram', 'Admitted work duration. Rejections are excluded.'],
  ['gatewise_controller_last_window_samples', 'Gauge', 'Completed sample count in the last evaluated adaptive window.'],
  ['gatewise_controller_last_p90_ratio', 'Gauge', 'Bucket-estimated p90 duration / route latency target.'],
  ['gatewise_controller_last_failure_fraction', 'Gauge', 'Failed sample fraction in the last evaluated window.'],
  ['gatewise_controller_last_evaluation_timestamp_seconds', 'Gauge', 'Unix time of the last evaluation; zero before one completes.'],
  ['gatewise_controller_last_decision', 'Gauge', 'Last adaptive decision with bounded decision and reason labels.'],
  ['gatewise_controller_last_window_saturated', 'Gauge', 'Whether saturation was observed in the last evaluated window.'],
  ['gatewise_limit_adjustments_total', 'Counter', 'Adaptive increases/decreases and their reasons.'],
] as const;

export const pageSections: Record<PageId, readonly (readonly [string, string])[]> = {
  overview: [],
  problem: [['basics', 'Request basics'], ['overload', 'Overload problem'], ['alternatives', 'Alternative approaches'], ['design-decisions', 'Gatewise decisions'], ['evolution', 'Implementation layers'], ['architecture', 'Architecture'], ['admission', 'Admission rules'], ['lifecycle', 'Request lifetime'], ['operation-lifetimes','Jobs and SQL lifetimes'], ['adaptive', 'Adaptive controller'], ['operations', 'Deployment constraints'], ['combined-signals', 'Combining signals'], ['references', 'References']],
  installation: [['operation-setup','Jobs and SQL'], ['source', 'Download source'], ['configure', 'Configure'], ['run', 'Run'], ['verify', 'Verify'], ['signal-setup', 'Overload signals'], ['resource-setup', 'Resource regulators'], ['adaptive-setup', 'Enable adaptive mode'], ['tenant-isolation', 'Tenant isolation'], ['priority-propagation', 'Priority propagation'], ['tenant-sharing', 'Tenant sharing'], ['validate-config', 'Validate configuration'], ['tuning', 'Choose limits'], ['deployment', 'Deployment'], ['library-deployment', 'Go application'], ['proxy-deployment', 'Proxy service']],
  examples: [['operation-example','Operation example'], ['resource-regulators','Resource regulators'], ['controlled-signals', 'Signal validation'], ['application-model', 'Application model'], ['run-the-demo', 'Run the demo'], ['postman-scenarios', 'Postman scenarios'], ['tenant-isolation', 'Tenant isolation'], ['priority-chain', 'Priority service chain'], ['tenant-sharing', 'Tenant sharing'], ['reproduce-benchmarks', 'Reproduce benchmarks']],
  benchmarks: [['operation-benchmarks','Jobs and SQL · v0.8.0'], ['resource-regulators','Real resource pressure · v0.7.0'], ['signal-engine','Combining signals · v0.6.0'], ['tenant-sharing', 'Sharing spare capacity · v0.5.0'], ['priority-chain', 'Priority chain · v0.4.0'], ['tenant-isolation', 'Tenant isolation · v0.3.0'], ['recorded-results', 'Service overload · v0.2.0'], ['service-results', 'Per-service results'], ['controller-validation', 'Controller validation'], ['methodology', 'Methodology'], ['resources-and-limitations', 'Resources and limitations'], ['reproduction', 'Reproduction']],
  metrics: [['operation-feedback','Operation metrics'], ['resource-feedback','Resource accounting'], ['signal-feedback','Signal diagnostics'], ['benchmark-metrics', 'Benchmark metrics'], ['prometheus-integration', 'Prometheus integration'], ['grafana-dashboard', 'Grafana dashboard'], ['controller-feedback', 'Controller feedback'], ['tenant-feedback', 'Tenant diagnostics'], ['priority-diagnostics', 'Priority diagnostics'], ['sharing-diagnostics', 'Sharing diagnostics'], ['metric-families', 'Metric families']],
  versions: [['latest', 'Latest release'], ['install-version', 'Install a version'], ['release-history', 'Release history'], ['upgrading', 'Upgrading']],
  reference: [['operation-reference','Operation and SQL APIs'], ['resource-reference','Resource APIs'], ['decision-reference','Decision engine'], ['sharing-reference', 'Sharing policy'], ['priority-reference', 'Priority API and wire format'], ['proxy-configuration', 'Proxy configuration'], ['environment-overrides', 'Environment overrides'], ['public-go-api', 'Public Go API'], ['http-responses', 'HTTP responses'], ['scope', 'Scope']],
};

export const adaptiveInstallationExample = `guard, err := middleware.NewService(middleware.ServiceConfig{
    Name: "orders",
    MaxConcurrent: 20,       // Example ceiling, not requests per second.
    ReservedCritical: 5,
    Adaptive: &middleware.AdaptiveConfig{
        MinConcurrent: 6, InitialConcurrent: 12,
        Interval: time.Second, MinSamples: 10,
        TargetLatency: 100 * time.Millisecond,
    },
})
if err != nil { log.Fatal(err) }`;
export const adaptiveProxyInstallationExample = JSON.stringify({
  ...JSON.parse(proxyExample),
  services: { orders: {
    upstream_url: 'http://127.0.0.1:9090', max_concurrent: 20,
    reserved_critical: 5, mode: 'adaptive', adaptive: {
      min_concurrent: 6, initial_concurrent: 12, interval: '1s',
      min_samples: 10, target_latency: '100ms',
    },
  } },
}, null, 2);

export const tenantMiddlewareExample = `// The application's authentication layer sets this context value first.
type authenticatedTenantKey struct{}

guard, err := middleware.NewService(middleware.ServiceConfig{
    Name: "orders", MaxConcurrent: 20, ReservedCritical: 5,
    Tenants: &middleware.TenantConfig{
        MaxConcurrent: 4, MaxTracked: 1024, IdleTTL: 5 * time.Minute,
        Limits: map[string]int{"premium": 8}, // Optional example override.
        Resolver: func(r *http.Request) (string, error) {
            id, _ := r.Context().Value(authenticatedTenantKey{}).(string)
            return id, nil
        },
    },
})
if err != nil { log.Fatal(err) }`;
export const tenantProxyExample = JSON.stringify({
  ...JSON.parse(proxyExample),
  services: { orders: {
    upstream_url: 'http://127.0.0.1:9090', max_concurrent: 20, reserved_critical: 5,
    tenants: {max_concurrent:4, max_tracked:1024, idle_ttl:'5m', header:'X-Tenant-ID', trusted_proxies:['10.0.0.0/24'], missing_policy:'reject', overflow_policy:'reject', limits:{premium:8}},
  } },
}, null, 2);

export const priorityMiddlewareExample = `// Add github.com/HN026/gatewise/priority to your imports.
guard, err := middleware.NewService(middleware.ServiceConfig{
    Name: "orders", MaxConcurrent: 20,
    Priorities: &priority.Config{
        Tiers: []priority.Tier{
            {Name: "urgent", Reserved: 5},
            {Name: "normal", Reserved: 3},
            {Name: "batch", Reserved: 0},
        },
        CriticalTier: "urgent", BackgroundTier: "batch",
        TrustedProxies: []string{"10.0.0.0/24"}, // Replace with your ingress.
    },
})
if err != nil { log.Fatal(err) }`;
export const priorityProxyExample = JSON.stringify({listen_addr:'127.0.0.1:8080',services:{orders:{upstream_url:'http://127.0.0.1:9090',max_concurrent:20,priorities:{tiers:[{name:'urgent',reserved:5},{name:'normal',reserved:3},{name:'batch',reserved:0}],critical_tier:'urgent',background_tier:'batch',trusted_proxies:['10.0.0.0/24']}}},routes:[{path_prefix:'/checkout',service:'orders',priority:'urgent'},{path_prefix:'/reports',service:'orders',priority:'batch'}]},null,2);

export const sharingMiddlewareExample = `// Example values; choose shares and ceilings by testing your workload.
Tenants: &middleware.TenantConfig{
    MaxConcurrent: 2, // Target share when Sharing is enabled.
    Limits: map[string]int{"premium": 4}, // Optional larger target share.
    MaxTracked: 1024, IdleTTL: 5 * time.Minute,
    Resolver: authenticatedTenantResolver,
    Sharing: &middleware.TenantSharingConfig{
        MaxConcurrent: 8, // Total per-tenant ceiling, not 8 extra slots.
        Limits: map[string]int{"premium": 10},
        DemandTTL: 500 * time.Millisecond,
    },
},`;
export const sharingProxyExample = JSON.stringify({listen_addr:'127.0.0.1:8080',services:{orders:{upstream_url:'http://127.0.0.1:9090',max_concurrent:20,reserved_critical:5,tenants:{max_concurrent:2,limits:{premium:4},max_tracked:1024,idle_ttl:'5m',header:'X-Tenant-ID',trusted_proxies:['10.0.0.0/24'],sharing:{max_concurrent:8,limits:{premium:10},demand_ttl:'500ms'}}}},routes:[{path_prefix:'/checkout',service:'orders',priority:'critical'},{path_prefix:'/reports',service:'orders',priority:'background'}]},null,2);
