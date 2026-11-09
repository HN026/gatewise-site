<p><img src="assets/logo.svg" alt="Gatewise logo" width="80" height="80" /></p>

# Gatewise

Process-local admission control for HTTP services, Go worker jobs and SQL operations. Gatewise bounds concurrent work, reserves capacity for critical traffic, and optionally adjusts admission using latency and error feedback.

Use it as Go middleware or as a standalone proxy in front of an HTTP service written in any language.

[Documentation](https://hn026.github.io/gatewise-site/) · [Installation](https://hn026.github.io/gatewise-site/#/installation) · [Examples](https://hn026.github.io/gatewise-site/#/examples) · [Benchmarks](https://hn026.github.io/gatewise-site/#/benchmarks)

## Go middleware

Requires Go 1.22 or newer.

```sh
go get github.com/HN026/gatewise@v1.0.0
```

```go
import "github.com/HN026/gatewise/middleware"

// Example values: choose limits by load testing your service.
guard, err := middleware.NewService(middleware.ServiceConfig{
    Name: "orders", MaxConcurrent: 20, ReservedCritical: 5,
})
if err != nil {
    log.Fatal(err)
}
mux.Handle("POST /checkout", guard.Middleware(
    middleware.Critical, http.HandlerFunc(checkout),
))
mux.Handle("POST /reindex", guard.Middleware(
    middleware.Background, http.HandlerFunc(reindex),
))
```

This is an integration fragment: `mux` is your `http.ServeMux` and `checkout`/`reindex` are application handlers. See the [complete example](https://hn026.github.io/gatewise-site/#/installation).

After adding the imports and handlers to your application, run `go mod tidy` to resolve the packages it uses, then build or start your service.

The example allows 20 requests running simultaneously across both routes, with background work limited to 15. This is concurrency, not requests per second. Reuse one guard for routes sharing capacity. Excess requests receive structured HTTP 503 responses. Limits are configurable and apply per process.

## Jobs and SQL operations

`operation.New` creates the shared admission engine without an HTTP handler. Use `Engine.Do(ctx, operation.Request{Priority: operation.Background}, fn)` around a job, or `Begin`/`Permit.Context`/`Finish` for an explicitly owned lifetime. Cancellation does not release work still running. Use separate engines for HTTP, workers and databases; nesting the same engine through its active context returns `operation.ErrNested`.

`sqlguard.New(db, engine, metadata)` wraps an existing `database/sql` pool. Statements hold admission through completion, rows through Close/terminal iteration, and transactions through Commit/Rollback. Always close rows; use transaction methods and `tx.Context()` inside `InTx`. Transaction control retains capacity through driver cleanup and may wait for configured driver/network timeouts. The application owns the driver and pool.

`regulator.SQLPool` samples finite-pool utilization. Opt-in `regulator.NewSQLPoolContention` distinguishes occupancy from new pool waits: it emits 0/1 pressure from WaitCount deltas and clears only after a configurable quiet interval with a nonfull pool. Historical waits before construction are ignored. It is not queue depth or an availability check. Existing providers and controller defaults are unchanged. `postgres.NewReplication` observes one named physical standby from a separate primary monitoring pool, distinguishing idle catch-up from real WAL byte backlog and unavailable data. These are application adapters, not PostgreSQL-engine enforcement or cluster-wide limits.

See [operation installation and lifetime rules](https://hn026.github.io/gatewise-site/#/installation/operation-setup) and the [runnable SQL/worker/standby example](https://hn026.github.io/gatewise-site/#/examples/operation-example). Register either an HTTP guard or operation engine with `metrics.Register`; work metrics distinguish `http` and `operation` without adding layered counts as end-to-end throughput.

## Compatibility and ownership

The v1 contract covers the exported `decision`, `metrics`, `middleware`, `operation`, `postgres`, `priority`, `regulator` and `sqlguard` APIs, documented proxy configuration, CLI flags and HTTP rejection fields. Use constructors and keyed configuration literals. Additive optional fields, diagnostic reasons and metrics may appear in v1.x; accept unknown response fields and do not depend on exact error/log text. `internal/` packages are not public APIs. CI checks the frozen exported API and runs race tests on Linux AMD64/ARM64 with Go 1.22 and the current stable toolchain. Go 1.22 is the source minimum; deploy with a currently supported, patched Go toolchain. Local macOS ARM64 validation uses current Go.

Reuse one engine for work sharing a budget, and do not copy a constructed engine, service, permit or SQL adapter. Applications own servers, authentication, HTTP/driver timeouts, SQL pools and collector cancellation. Always finish permits and close rows; cancellation cannot stop arbitrary running work. Collectors must honor cancellation; one uncooperative provider can retain one worker per signal. Limits and waiting queues are process-local. Adding replicas multiplies independent budgets unless you size them together.

[Compatibility details](https://hn026.github.io/gatewise-site/#/reference/compatibility) · [Deployment and rollback](https://hn026.github.io/gatewise-site/#/installation/operating)

## Standalone proxy

```sh
git clone --branch v1.0.0 https://github.com/HN026/gatewise.git
cd gatewise
go build -o bin/gatewise ./cmd/gatewise
```

Save a configuration such as `configs/orders.json`:

```json
{
  "listen_addr": "127.0.0.1:8080",
  "services": {
    "orders": {
      "upstream_url": "http://127.0.0.1:9090",
      "max_concurrent": 20,
      "reserved_critical": 5
    }
  },
  "routes": [
    {"path_prefix": "/orders", "service": "orders", "strip_prefix": true, "priority": "critical"},
    {"path_prefix": "/orders/background", "service": "orders", "strip_prefix": true, "priority": "background"}
  ]
}
```

Addresses and limits above are examples. Point `upstream_url` at your running backend, then start:

```sh
./bin/gatewise -config configs/orders.json -env-file ""
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/orders/hello
```

The second request forwards `/hello` to your backend; its response depends on that handler. Routing uses the longest matching path-segment prefix. Configuration precedence is process environment > dotenv > JSON > defaults. Configuration is resolved at startup. See the [reference](https://hn026.github.io/gatewise-site/#/reference) for adaptive settings, route budgets and environment variables.

## Adaptive profiles

Existing adaptive integrations retain the `legacy` controller. Opt in with `Adaptive: &middleware.AdaptiveConfig{Profile: "stable"}` (or the same field on `operation.AdaptiveConfig`). The proxy accepts `adaptive.profile: "stable"` and `GATEWISE_SERVICE_<NAME>_ADAPTIVE_PROFILE=stable` when adaptive mode is enabled. Explicit bounds and settings remain authoritative.

The stable profile uses real local capacity refusals as growth evidence, requires healthy latency headroom, and protects well-sampled priority-tier tails instead of relying only on aggregate feedback. It records an EWMA of its selected control ratio, uses bounded decrease/growth steps, and vetoes growth when a matching signal ceiling is binding. Conservative startup defaults remain minimum = reservation + 1, initial = minimum, interval = 1 second, minimum samples = 10. A bounded local interval offset helps diversify timing; it is not cross-instance coordination.

Sparse windows can be reported as holds with at least three completions, four nominal intervals elapsed and no refused local demand. Actual overload keeps the configured sample requirement. Idle reset clears stale feedback without freeing running work or resetting the learned limit. Controller snapshots, adjustment logs and bounded Prometheus metrics distinguish raw aggregate p90 from the selected control ratio, pending samples, sparse holds and external constraints.

[Profile setup and exact rules](https://hn026.github.io/gatewise-site/#/installation/stable-adaptive) · [Repeated controller evaluation](https://hn026.github.io/gatewise-site/#/benchmarks/adaptation)

The preset does not learn a production ceiling or relax your latency targets. Per-tier tails need at least 10 samples; histograms are bucket estimates, not exact percentiles. Operator floors, shared dependencies and workload changes can still prevent a latency goal from being met. Existing `legacy` behavior remains the rollback option.

## Tenant isolation

Optional `ServiceConfig.Tenants` adds per-tenant concurrency caps inside each service budget. Supply a resolver using your authenticated principal. The proxy accepts a dedicated identity header only from configured immediate-peer CIDRs; the ingress must authenticate and overwrite it. Forwarded headers do not establish trust.

Tracking has a configurable bound and idle expiry. Missing identities and registry exhaustion reject by default; explicit bounded shared-bucket policies are available. Active entries are never evicted. Tenant rejections do not feed adaptive latency or failure observations. Limits apply per guard and process, not across replicas.

`guard.TenantSnapshot()` and aggregate Prometheus metrics expose tracking and rejection reasons without identity labels. See the [tenant integration guide](https://hn026.github.io/gatewise-site/#/installation/tenant-isolation) and [Postman example](https://hn026.github.io/gatewise-site/#/examples/tenant-isolation).

## Metrics and operations

The proxy exposes `/healthz` and Prometheus `/metrics` outside admission. Go applications can register a guard using `metrics.Register` with their own Prometheus registry. A Grafana dashboard and optional local monitoring stack are included under `deploy/observability/`.

Adaptive consumers can inspect `guard.ControllerSnapshot()` for the last evaluated sample count, latency ratio, failure fraction and decision, including holds. The Prometheus collector and Grafana dashboard expose the same observations.

Validate proxy configuration offline before deployment:

```sh
./bin/gatewise -check-config -config configs/orders.json -env-file ""
```

This exits without opening a listener or contacting upstreams. It does not verify backend availability.

Slots are held until synchronous handlers return. Immediate rejection is the default; optional bounded waiting is available. There is no preemption. Adaptive mode uses completed-request feedback within configured bounds; it does not directly measure database capacity. Multiple replicas have independent budgets. Applications own retry policy, dependency timeouts, authentication and TLS. Long-lived streaming and WebSocket support are outside the current scope.

## Optional bounded waiting

Set `ServiceConfig.Queue` (or `operation.Config.Queue`) to opt in:

```go
Queue: &middleware.QueueConfig{
    MaxWaiting: 32, MaxWait: 40 * time.Millisecond,
    AgingAfter: 20 * time.Millisecond,
    Reserved: map[middleware.Priority]int{middleware.Critical: 8},
},
```

These are example values. Waiting slots and execution concurrency are separate budgets. FIFO within a tier, priority preference and aging choose the next eligible request; hard execution reservations still apply. Only local capacity refusals wait. Every selected request rechecks policies and holds no execution, tenant or resource permit while waiting. The earlier caller deadline wins; full/reserved/expired waits return explicit reasons. Existing work is never preempted or released just because its context cancels.

HTTP queues buffer known-length bodies up to 64 KiB before admission so HTTP/1.1 disconnects are observable while waiting. Configure `http.Server.ReadTimeout` to bound upload preparation. Large/unknown bodies keep streaming and fail fast instead of waiting. `QueueBodyMaxBytes` customizes buffering (-1 disables it; maximum 1 MiB). Proxy configuration uses `queue.max_body_bytes` and requires a positive top-level `read_timeout` while buffering is enabled. `Request.NoWait` opts a native operation out of waiting while retaining queue order.

Queue time is reported separately from processing feedback. Assess useful completions against the whole response budget, including preparation and waiting. A queue does not add capacity or guarantee lower-tier progress under unavailable execution capacity. It is in memory, process-local and non-durable. Disable it by removing Queue / the JSON queue object, or setting the service's `QUEUE_ENABLED=false` environment override and restarting.

[Setup and exact policy](https://hn026.github.io/gatewise-site/#/installation/bounded-waiting) · [Database-backed comparison](https://hn026.github.io/gatewise-site/#/benchmarks/queue)

## Demo example and measurements

The separate [documentation repository](https://github.com/HN026/gatewise-site) provides a source-only commerce example: orders, catalog and inventory services, three PostgreSQL databases, and a portable Postman collection. Both proxy and middleware modes are supported. [Download and run the demo example](https://hn026.github.io/gatewise-site/#/examples).

[Published benchmarks](https://hn026.github.io/gatewise-site/#/benchmarks) report sustained SQL traffic, overload, injected database failures and recovery. Results separate successful work within a latency budget, successful-request p95, rejection rates and SQL-pool waits. These controlled local experiments do not establish production capacity. The older synthetic runner remains available in `benchmarks/` for custom HTTP fixtures.

## Choose and tune a policy

Start with the smallest policy that meets the service's latency and failure goals. A rate limiter enforces a request quota; a classless semaphore may be sufficient for a stable concurrency ceiling. Gatewise adds priority reservations, adaptive limits and optional bounded waiting when those are useful. Rate limits can be used alongside admission control.

A proxy limit covers active proxy requests. After cancellation, a remote handler may still be cleaning up; this is not a hard bound on all remote work. Middleware holds admission until its protected handler returns. Use operation/SQL guards to protect the corresponding backend lifetime.

The [policy comparison](https://hn026.github.io/gatewise-site/#/benchmarks/hardening) runs seeded, matched traffic through unprotected services, a semaphore, three token-bucket rates and Gatewise against the same PostgreSQL application. It includes reservation sweeps, controlled cancellation and before/after wrapper overhead. It reports useful completions, refusals, failures, pool waiting, recovery and wrapper overhead. These local measurements are not production capacity or a Cinnamon performance comparison. The [tuning guide](https://hn026.github.io/gatewise-site/#/installation/choose-policy) covers concurrency sweeps, reservations, adaptive bounds, deadline-aware waiting and recovery checks.

## Docker proxy

```sh
docker build --build-arg VERSION=v1.0.0 -t gatewise:v1.0.0 .
docker run --rm --name gatewise --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --add-host=host.docker.internal:host-gateway \
  -p 127.0.0.1:8080:8080 \
  -e GATEWISE_SERVICE_ORDERS_URL=http://host.docker.internal:9090 \
  gatewise:v1.0.0
```

The backend address and limits are examples. Use a backend reachable from the container; a container's `127.0.0.1` is its own loopback. The supplied config binds `0.0.0.0:8080`, forwards `/orders`, and requires the upstream URL at runtime. Mount your own JSON at `/etc/gatewise/config.json:ro` for multiple services. Pass runtime variables using Docker's `-e` or `--env-file`; the image disables automatic application `.env` loading. Non-root UID/GID 65532, static binary, CA roots, no runtime shell/toolchain. `/healthz` checks proxy liveness; `/metrics` exposes Prometheus metrics. `docker stop --time 10 gatewise` allows the default five-second graceful shutdown.

Versioned registry image: `ghcr.io/hn026/gatewise:v1.0.0` (`linux/amd64` and `linux/arm64`). [Docker installation and configuration](https://hn026.github.io/gatewise-site/#/installation/docker).

## Development

```sh
go test -race ./...
go vet ./...
go build ./cmd/gatewise
```

Public packages include `operation/`, `middleware/`, `sqlguard/`, `decision/`, `priority/`, `regulator/`, `postgres/` and `metrics/`. Executable wiring is in `cmd/`; implementation packages are in `internal/`; focused tests are under `tests/`.

## License

MIT. Gatewise is an independent project inspired by published overload-control techniques; it is not affiliated with Uber.

### Priority propagation

Optional `ServiceConfig.Priorities` adds 2–8 ordered tiers, compatibility mappings for Critical/Background, trusted HTTP metadata and optional rotating cohorts. Use `priority.With` after authentication and `priority.Transport` with an explicit downstream host allowlist. Missing metadata uses the route default; untrusted supplied metadata returns 403. Cohorts provide consistent ordering within a UTC epoch, not identical admission decisions across services or starvation guarantees. Existing services without tier configuration retain two-class admission. Priority configuration alone does not enable waiting or capacity borrowing.

[Priority setup](https://hn026.github.io/gatewise-site/#/installation/priority-propagation) · [Runnable service chain](https://hn026.github.io/gatewise-site/#/examples/priority-chain)

### Tenant sharing

Optional `TenantConfig.Sharing` turns tenant caps into target shares and allows identified tenants to borrow up to separate total ceilings. Active/recent demand protects other tenants’ shares; targets scale down when the adaptive limit is smaller, and returning below-share work can pause new borrowing. Running requests finish normally. Hard caps remain the default. [Setup and allocation rules](https://hn026.github.io/gatewise-site/#/installation/tenant-sharing).

Optional `ServiceConfig.Decisions` combines cached overload observations with existing admission controls. Start in observe mode, configure freshness/failure policies, then choose enforcement. Providers run in the background; existing integrations remain unchanged. [Signal setup](https://hn026.github.io/gatewise-site/#/installation/signal-setup) · [API and failure semantics](https://hn026.github.io/gatewise-site/#/reference/decision-reference).

Optional `regulator` providers measure Go memory and goroutine pressure. Resource budgets reserve planned logical write bytes and concurrent work per bounded, hashed key; costs come from a trusted operation planner. [Setup and measurement boundaries](https://hn026.github.io/gatewise-site/#/installation/resource-setup).
