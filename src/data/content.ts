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
  ['gatewise_limit_adjustments_total', 'Counter', 'Adaptive increases/decreases and their reasons.'],
] as const;

export const pageSections: Record<PageId, readonly (readonly [string, string])[]> = {
  overview: [],
  problem: [['basics', 'Request basics'], ['overload', 'Overload problem'], ['alternatives', 'Alternative approaches'], ['uber-design', 'Uber’s progression'], ['design-decisions', 'Gatewise decisions'], ['evolution', 'Implementation layers'], ['architecture', 'Architecture'], ['admission', 'Admission rules'], ['lifecycle', 'Request lifetime'], ['adaptive', 'Adaptive controller'], ['operations', 'Deployment constraints'], ['references', 'References']],
  installation: [['source', 'Download source'], ['configure', 'Configure'], ['run', 'Run'], ['verify', 'Verify'], ['deployment', 'Deployment'], ['library-deployment', 'Go application'], ['proxy-deployment', 'Proxy service']],
  examples: [['bundle-contents', 'Bundle contents'], ['run-the-demo', 'Run the demo'], ['postman-scenarios', 'Postman scenarios'], ['reproduce-benchmarks', 'Reproduce benchmarks']],
  benchmarks: [['recorded-benchmark-figures', 'Benchmark images'], ['results', 'Results'], ['methodology', 'Methodology'], ['overhead-and-resource-accounting', 'Overhead and resources'], ['reproduction', 'Reproduction']],
  metrics: [['benchmark-metrics', 'Benchmark metrics'], ['prometheus-integration', 'Prometheus integration'], ['grafana-dashboard', 'Grafana dashboard'], ['metric-families', 'Metric families']],
  versions: [['latest', 'Latest release'], ['install-version', 'Install a version'], ['release-history', 'Release history'], ['upgrading', 'Upgrading']],
  reference: [['proxy-configuration', 'Proxy configuration'], ['environment-overrides', 'Environment overrides'], ['public-go-api', 'Public Go API'], ['http-responses', 'HTTP responses'], ['scope', 'Scope']],
};
