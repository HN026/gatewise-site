# Gatewise API testing

Import `Gatewise.postman_collection.json` and `Gatewise-Local.postman_environment.json`, then select **Gatewise Local**. `Gatewise-performance.postman_collection.json` is a separate manual load collection. The same files and this guide are included in the Postman ZIP available on the documentation site.

These collections target the optional local demo services. They require the independent sibling modules `demo-backend`, `catalog-backend`, `middleware-backend`, or `benchmark-backend` for their respective scenarios. Those services are not included in the Gatewise module. Users connecting Gatewise to their own services should use their own endpoints and requests.

Run folders individually and sequentially on idle services. Some scripts issue concurrent requests, and some stages require stopping or restarting a backend. Set Postman request timeout to at least 10 seconds and script timeout to 60 seconds. Do not run the entire collection against one unchanged service setup.

## Proxy scenarios

Build the following executables from each project's directory:

| Project | Build command | Start command | Port |
| --- | --- | --- | --- |
| `demo-backend` | `go build -o bin/demo-backend ./cmd/demo-backend` | `./bin/demo-backend` | 9090 |
| `catalog-backend` | `go build -o bin/catalog-backend ./cmd/catalog-backend` | `./bin/catalog-backend` | 9091 |
| `gatewise` | `go build -o bin/gatewise ./cmd/gatewise` | `./bin/gatewise` | 8080 |

Use `configs/local.json` and configure `.env` using `.env.example`. Both demo services use a fixed limit of 2 with 1 critical slot reserved. Match `gatewise_url`, `users_limit`, `catalog_limit`, `users_reserved_critical` and `catalog_reserved_critical` to any local overrides. Priority scenarios require `0 < reserved < total <= 16`.

Run these folders with all three processes running:

1. **Both backends running** — routing, prefix stripping, forwarding, timeouts, route boundaries and backend 404 responses.
2. **Proxy concurrency (both running)** — service-specific saturation, rejection and capacity release.
3. **Proxy priorities (both running)** — background work leaves reserved capacity for critical work; both priorities share the total limit.
4. **Proxy metrics (fixed)** — counters, active work, histogram accounting, rejection reasons and unguarded metrics access.

`saturation_delay_ms` defaults to 1000 and must stay below the proxy's response-header timeout. `timeout_delay` must exceed that timeout (defaults: 3s versus 2s). Asynchronous test requests appear in the Postman Console or CLI output.

Run failure stages separately:

1. Stop only catalog and run **Catalog stopped (manual stage)**.
2. Restart catalog, stop only demo-backend, and run **Users stopped (manual stage)**.
3. Restart demo-backend afterward.

The stages verify that an unavailable backend returns 502 while the other service and Gatewise health remain available.

CLI example, from the extracted demo root:

```sh
postman collection run postman/Gatewise.postman_collection.json \
  -e postman/Gatewise-Local.postman_environment.json \
  -i 'Proxy concurrency (both running)' \
  --timeout-script 60000 --no-report-events
```

Change `-i` to the required folder after preparing its services.

## Direct middleware scenarios

From the independent `middleware-backend` directory:

```sh
go build -o bin/middleware-backend ./cmd/middleware-backend
./bin/middleware-backend
```

This runs directly on port 9092 with total capacity 2 and critical reservation 1. No proxy or upstream service is required. Run **Middleware basic (no proxy required)**, **Middleware concurrency (no proxy required)**, **Middleware priorities (no proxy required)**, then **Middleware metrics (fixed)**.

Match `middleware_url`, `middleware_limit` and `middleware_reserved_critical` to the service flags if changed. Both route wrappers share one public `middleware.Service`. `/healthz` and `/metrics` are outside the guard. The independent module currently uses a local `replace` to `../gatewise`.

## Adaptive scenarios

Keep the fixed proxy and middleware instances available as references. Start both demo backends, then start a separate proxy from the Gatewise directory:

```sh
GATEWISE_LISTEN_ADDR=127.0.0.1:8088 ./bin/gatewise -config configs/adaptive.json
```

Start a separate adaptive middleware instance from `middleware-backend`:

```sh
./bin/middleware-backend -listen 127.0.0.1:9093 -adaptive \
  -max-concurrent 6 -reserved-critical 1 \
  -min-concurrent 2 -initial-concurrent 4 \
  -adaptive-interval 500ms -min-samples 4 -target-latency 100ms
```

Run **Adaptive proxy (separate instance)** and **Adaptive middleware (separate instance)** sequentially. Match their adaptive URL/environment variables to any port overrides. The proxy's users service adapts; catalog remains a fixed reference.

The scenarios grow healthy saturated capacity, verify a slower handler's separate latency budget, induce latency pressure, check reservation at the minimum, and restore healthy work to observe recovery. Defaults are min 2, max 6, initial 4, 500ms feedback interval, 4 samples and a 100ms default latency budget. Reports routes use a 1s budget.

Leave those processes running, then run **Adaptive metrics (after adaptive scenarios)**. `X-Gatewise-Mode` and `X-Gatewise-Limit` are diagnostic snapshots. Existing work drains after a limit reduction, so active requests may temporarily exceed the new limit. Limits are per process.

## Monitoring

With Podman running, start the supplied monitoring stack from Gatewise:

```sh
./deploy/observability/run.sh start
```

Grafana: <http://127.0.0.1:3000/d/gatewise-overview>. Prometheus: <http://127.0.0.1:9095>. Grafana is provisioned with a local anonymous Viewer; local admin defaults are `admin` / `gatewise-local`, configurable through `GRAFANA_ADMIN_PASSWORD`.

`deploy/observability/prometheus.yml` scrapes fixed/adaptive proxy ports 8080/8088 and middleware ports 9092/9093 every 5 seconds. Unused profiles show down. Rates and latency charts need traffic and at least two scrapes. Use instance/service filters to inspect each deployment.

`PROMETHEUS_PORT`, `GRAFANA_PORT` and `STACK_NAME` can override launcher settings. `CONTAINER_ENGINE=docker` selects Docker. `run.sh status` reports containers; `run.sh stop` removes this stack's containers and retains data volumes. It does not stop Go services. Docker Compose is also supported:

```sh
docker compose -f deploy/observability/compose.yaml up -d
```

Metrics distinguish priorities, rejection reasons, completion status, cancellations, panics and adaptive adjustments. Admitted duration excludes rejections. Counters reset on process restart. Raw URLs, customer IDs and request IDs are not automatic labels. Go applications register guards through `metrics.Register` with a caller-owned Prometheus registry and expose that registry outside guarded routes.

## Reproducible overload experiments

The independent sibling `benchmark-backend` is required, along with Go and `ps`. From Gatewise:

```sh
./benchmarks/run.sh
# Short wiring check; choose a new output directory for each run:
./benchmarks/run.sh -repeats 1 -phase-duration 2s -output /tmp/gatewise-check
```

The launcher builds current sources, starts its own services on unused loopback ports, and cleans up those processes on completion or interrupt. Existing demo services and monitoring containers are unaffected. `GO_BIN` and `BENCHMARK_BACKEND_DIR` override executable/location defaults. `-help` lists rates, repetition, duration, output and generator options. Nondefault workloads can invalidate the default expectations.

Three repetitions compare direct traffic, fixed/adaptive proxy and fixed/adaptive middleware. Each deployment gets a fresh fixture. After a 1s warm-up, 5s baseline/pressure/recovery phases change synthetic capacity `12 → 2 → 12` and offered RPS `200 → 400 → 400`. One critical arrival is scheduled for every seven background arrivals, without retries. Work takes 20ms plus 50ms per excess active request, capped at 400ms; each request keeps its initially selected duration. Guards use max/initial 6 and reservation 1; adaptive uses min 2, a 500ms interval, 10 samples and a 100ms target.

The printed results directory contains `results.json`, `summary.csv`, process logs, resource samples and phase-boundary metrics. Results are checkpointed after each deployment; a successful runner exit establishes that all configured repetitions completed. Generated results are ignored by Git.

Compare successful latency separately from rejection latency, and within-100ms goodput alongside total successful throughput and rejections. Goodput includes drain time. Percentiles use nearest rank; repetitions remain separate. Dispatches over 5ms late or above the generator ceiling are dropped and counted. The default delivery gate allows at most 1% dropped schedules per priority/phase; `-max-drop-percent 0` requires perfect delivery. Inspect drops and dispatch lag before attributing a limit to the server.

Child CPU is OS user+system time over the full process lifetime, including startup, warm-up, control, scraping and logging. RSS is sampled every 250ms, including the generator; observed peaks are lower bounds. Proxy retains normal access logging, while optional middleware logging is disabled in the fixture. These are local synthetic experiments; run without competing load.

For separate overhead measurements:

```sh
go test -run '^$' -bench . -benchmem -count 3 ./tests/benchmark
```

`BenchmarkHandler` isolates wrapping/allocations. `BenchmarkHTTP` compares direct/proxied local HTTP with logging disabled. These measurements have different scope from the process experiments.

## Manual fixture and performance collection

After building the fixture using the launcher, start it from Gatewise:

```sh
../benchmark-backend/bin/benchmark-backend -mode fixed
```

Set `benchmarkBaseUrl` to the fixture (default: `http://127.0.0.1:9190`) and run **Benchmark fixture (guarded, idle)**. Repeat with `-mode adaptive` to check that integration.

Run the performance CLI from the extracted demo root. For manual load, start a constrained fixture with `-mode fixed -capacity 2` or `-mode adaptive -capacity 2`. The separate performance collection sends one critical and seven background requests:

```sh
postman performance run postman/Gatewise-performance.postman_collection.json \
  -e postman/Gatewise-Local.postman_environment.json \
  --runner local --load-profile fixed --peak-vu 4 --duration 1 \
  --output ndjson --pass-if 'less_than(p95, 500)'
```

Postman virtual users form a closed loop: slower responses reduce offered RPS. Use the scheduled-arrival runner for the controlled comparison. Performance assertions accept 200 or deliberate admission 503; HTTP error rate may still count 503. Inspect rejection counts separately. The 500ms threshold is an example for this fixture.

To change contention manually, send `POST {{benchmarkBaseUrl}}/control` with JSON `{"capacity":2}` for pressure or `{"capacity":12}` for recovery. This control endpoint belongs only to the loopback fixture. The collections are portable v2.1 JSON; local runs require no cloud workspace. Use sample request data.
