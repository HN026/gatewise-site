# Gatewise commerce example

Three independent HTTP services and PostgreSQL. Proxy mode adds Gatewise as the fourth application process; middleware mode runs three processes. PostgreSQL and optional monitoring are infrastructure. This is a reproducible local experiment, not a production deployment template.

## Requirements and startup

Go 1.25+, Python 3.10+, and running Podman or Docker. The PostgreSQL 18.6 Alpine image is downloaded if absent. Local module replacements resolve the included Gatewise source snapshot; no unpublished module fetch is required.

```sh
python3 run.py --profile middleware-adaptive
# Other modes: direct, middleware-fixed, proxy-fixed, proxy-adaptive
python3 run.py --engine docker --profile proxy-adaptive
```

The launcher chooses free loopback ports, creates a uniquely named PostgreSQL container/volume and three fresh databases, builds both modules, and prints service, backend, metrics, admin and Postman-environment addresses. Ctrl+C stops only this launcher's processes and removes its disposable databases/volume. Do not use persistent or production data. Credentials `gatewise / gatewise-local` are public, disposable example credentials; PostgreSQL and admin listeners bind only to localhost.

Orders: `POST /orders/checkout` with `Idempotency-Key` and `{"amount_cents":1234}`, then `GET /orders/{id}`. First checkout returns 201, exact replay 200, conflicting amount 409. Catalog: `GET /catalog/products?limit=10` returns persisted products. Inventory: `POST /inventory/reservations` with an idempotency key and `{"product_id":1,"quantity":1}`; `GET /inventory/stock/1` reads remaining stock. Reservations and stock decrements share one transaction; replay does not decrement twice and insufficient stock returns 409. These services demonstrate separate resources; checkout is not a distributed transaction across all three.

Each service has `POST /<service>/reports/rebuild` classified as background. Reports run synchronously inside the HTTP handler, not in detached queue workers, so admission tracks their complete lifetime. Reports aggregate its own 20,000 seeded rows and upsert a report in a transaction. Each service has its own four-connection pool and, in protected modes, a separate admission budget: ceiling 12, reserved critical 4, adaptive minimum 5, initial 12, interval 500ms, minimum 10 samples, 100ms latency target. These are experimental settings, not recommended production defaults. A one-second SQL deadline includes pool acquisition. HTTP client benchmark timeout is three seconds; no automatic retries occur.

## Postman

Import `postman/Commerce.postman_collection.json` and the environment file printed by the launcher. Run the complete collection in order against a fresh database. Set a 5s request timeout and 15s script timeout. It validates persistence, idempotency, inventory consistency, validation, injected SQL failures, admission pressure and recovery. Control steps restore healthy settings. Postman CLI users can run the same contract and clean up automatically:

```sh
python3 run.py --profile proxy-adaptive --verify
```

The optional CLI must be on PATH; the GUI requires no CLI. No cloud workspace or account is required for local requests. Generated environments contain only local service addresses, not database credentials.

## Optional metrics

Run `python3 run.py --profile proxy-adaptive --monitor` to start local Prometheus and Grafana with dynamically configured scrape targets. The launcher prints their addresses. The commerce dashboard includes admission, SQL pools, controller feedback and backend Go heap/goroutines; the generic library dashboard remains available. Monitoring containers and their disposable volumes are removed on exit. This monitoring setup was validated on macOS/Podman and requires containers to reach host listeners; native Linux deployments may require different scrape networking. Rates need at least two scrapes and traffic. This adds monitoring infrastructure, not additional business services.

## Repeat the measured experiment

Stop other workloads first. Defaults are three repetitions of all five profiles with rotating order, fresh databases per run, and about 32 minutes of offered traffic plus startup/drain. Around 2.28 million requests are independently scheduled by the Go generator; it does not wait for a response to schedule the next arrival.

```sh
python3 benchmark.py --output results/commerce-results.json
# Wiring check only; never compare its results with the published full run:
python3 benchmark.py --repeats 1 --phase-scale .1 --output results/smoke.json
```

Phases: baseline 5s at 200 RPS; linear ramp 10s from 200 to 800 RPS; pressure 15s at 1,200 RPS; burst 5s at 2,400 RPS; intermittent SQL faults 10s at 800 RPS; recovery 15s at 1,200 RPS; sustained real-SQL traffic 60s at 1,500 RPS; low traffic 5s at 10 RPS. The service mix is 60% orders, 20% catalog, 20% inventory. Business/background split is 80/20 normally and 20/80 during pressure/burst. Ramp adds 10ms SQL delay; pressure/burst add explicit 40ms `pg_sleep` and 30× report work. Error phase fails every fourth transaction with SQL division by zero. Baseline, recovery, sustained and low traffic inject no delay/errors. The sustained phase uses real SQL alone.

Database state persists between phases within a run; completed writes can affect later report costs. Each new deployment run starts fresh.

Results include per-service successful p95 (excluding rejected/error responses), successful work within 100ms per offered second, 503 rates, non-503 5xx (the `backend_5xx` field also includes proxy gateway errors), pool wait counts/durations, scheduled/sent/dropped counts, dispatch lag, arrival latency, sampled concurrency/controller state, Go heap and goroutines. The generator has 4,096 in-flight slots and a 3s timeout; missed schedules and transport errors remain explicit. Sampled traces are observations, not exact peaks. Different hardware/VM limits can change both absolute capacity and comparative results. This does not establish production capacity, long-duration leak freedom, multi-host behavior or fault tolerance.

## Targeted controller validation

`python3 validate_controller.py --output results/controller-validation.json` runs three repetitions of both adaptive modes. At constant 600 RPS, real SQL delay increases through 0, 10, 20, 40 and 80ms in three-second stages. It then exercises a burst, intermittent SQL errors, eight seconds of recovery at 1,500 RPS, sustained traffic and low traffic. The same pool, delivery, bounds and recovery checks apply. Run this separately from the main benchmark.

## Controls and safety boundary

Each service has its own separate numeric-loopback admin listener: `GET /state`, `PUT /control` with `{"work_multiplier":1,"sql_delay_ms":0,"failure_every":0}`. These controls are test instrumentation, never publish them on the business listener or expose them through a gateway. The example has no authentication, TLS, durable orchestration, migrations framework or distributed transactions. Gatewise remains independent of PostgreSQL and of this demo's application APIs.

## Tenant isolation (v0.3.0)

Run `python3 run.py --profile proxy-adaptive --tenants` or choose middleware-fixed / middleware-adaptive / proxy-fixed. The demo configures 2 concurrent requests per tenant, 32 identified entries and a 2s idle TTL. These are experimental settings. `X-Demo-Tenant` is accepted only from loopback and exists for local testing; production identity must come from authentication and trusted ingress metadata. The backend does not authenticate users.

Import `postman/Tenants.postman_collection.json` with the generated environment. The original Commerce collection remains for tenant-disabled profiles. `--tenants --verify` runs the tenant collection. It validates real domain persistence, noisy/ordinary callers, missing/malformed identities, registry pressure, expiry and privacy of metrics. Controls restore healthy SQL settings.

Run `python3 tenant_benchmark.py --output results/tenant-results.json` separately from other load. Three repeats rotate ten adapters: direct, plain semaphore, fixed/adaptive proxy and middleware, with tenant isolation on/off. Phases: 3s baseline (each actor 8 RPS), 12s noisy neighbor (noisy 480 RPS plus three ordinary actors at 8 RPS each, explicit 40ms SQL delay), 4s identity churn (400 fresh identities/s plus ordinary actors, ordinary SQL), and 4s recovery (all four actors at 8 RPS, ordinary SQL). All operations are critical; service proportions remain 60/20/20. Each run starts fresh databases. It takes about 12 minutes plus startup/drain. A `.2` phase scale is a wiring check, not equivalent evidence.

The shared comparison interface in comparison.py supplies application URLs and metadata to the same Go generator. A new implementation needs an actual adapter, version/configuration provenance and the same measurement contract. No Cinnamon implementation is included. Direct comparison requires access to real code and permission to publish measurements.

Hard tenant caps intentionally do not borrow unused capacity. Registry overflow rejects new identities by default; known active identities are never evicted. Missing/overflow shared buckets are opt-in in the library and are separately bounded. Identity churn cannot create unbounded tracking, but allowing a caller to invent identities defeats the intended per-tenant policy.

## Priority chain

Run `python3 run.py --profile middleware-fixed --priorities` or `--profile proxy-adaptive --priorities`. Orders flows call the database-backed catalog API; priority and rotating cohort diagnostics are returned for both hops. Import `postman/Priorities.postman_collection.json` with the printed environment, or add `--verify`. Loopback peers and the public seed are for this disposable demo only. Run tenant and priority contract modes separately.

`python3 priority_benchmark.py --output results/priority-results.json` measures three repetitions of four integration modes across baseline, two downstream-pressure levels and recovery. The shared proxy serves both hops, retaining at most four application processes. All SQL pools have four connections; catalog admission is six and orders twelve. One unmeasured stable-key probe runs between phases. No application retries are used in measured traffic.

`python3 validate_priority.py --output results/priority-validation.json` exercises trusted and untrusted orders entries during downstream SQL pressure. It preserves trust on the internal proxy-to-backend hop and checks that forged forwarding headers do not promote a caller.

## Tenant sharing

Use `python3 run.py --profile middleware-fixed --sharing 4` (or `proxy-adaptive`) for a share of 2 and total ceiling of 4. Add `--verify` to run `postman/Sharing.postman_collection.json`. `--tenants` alone retains hard caps. The demand window is 500ms; identities are synthetic and loopback-only.

`python3 sharing_benchmark.py --output results/sharing-results.json` compares hard caps and ceilings 4/12 through solo load, returning tenants, all tenants busy, solo load again and healthy recovery. All traffic is critical. First-success time includes the wait until the tenant’s first scheduled arrival. Report throughput, ordinary latency and rejections together; a large ceiling can add SQL-pool waiting.

`python3 validate_sharing.py --output results/sharing-validation.json` checks returning customers while borrowed requests are still running. This targeted test retries 503 every 10ms (up to 3s); the separate scheduled throughput experiment never retries.

## Controlled overload signals

Run `python3 run.py --profile proxy-adaptive --decisions enforce --verify` or choose `--decisions observe`. The Signals Postman contract tests delivery and policy with two controlled providers on each existing loopback admin listener. No extra application process is added. Threshold `.8`, pressure cap `4`, unknown cap `2`, polling `100ms`, deadline `50ms` and freshness `600ms` are experimental settings, not recommended production values. Signal controls are absent from the business listener. Observe mode keeps hard service limits while recording proposals.

`python3 decision_benchmark.py --output results/decision-results.json` compares disabled, observe and enforce modes for both fixed/adaptive middleware and proxy deployments, three repeats each. Healthy traffic, pressure, conflicting sources, source failure and recovery run against real PostgreSQL. The provider values are controlled; this release does not infer pressure from SQL-pool metrics. See the site’s benchmark page for outcomes, rejections, pool waiting, measured overhead and limitations.

## Real resource regulators

Run `python3 run.py --profile proxy-fixed --resources enforce --verify` (or choose observe/disabled). This uses the existing three applications and optional shared proxy. Providers measure each backend, while operation metadata comes from a validated plan. The isolated demo trusts loopback clients to send that same plan in X-Gatewise-Cost; a deployed ingress must authenticate and overwrite this reserved header. The backend rejects plan mismatches.

`python3 resource_benchmark.py --output results/resource-results.json` compares all three policies in fixed/adaptive middleware and proxy modes, three repetitions. It allocates 8MiB/request, creates 32 waiting child goroutines, persists 128KiB values, and contends on a hot row while cold keys continue. All routes are critical; 20% of traffic is unrelated probe work. Memory uses a 128MiB Go-managed budget at ratio .65; goroutine threshold is 180; both sample every 50ms and propose cap 2. Byte budget is 256KiB, per-key concurrency 1, registry 32 and idle TTL 2s. These are test values, not defaults or production guidance.

The runner collects unused runtime pages explicitly between phases. This is fixture cleanup, not an automatic recovery-time guarantee. Runtime pressure can overshoot or oscillate; byte/key reservations are atomic and held until handler completion. Waiting goroutines demonstrate count pressure, not CPU overload. Read probe p95, rejection/error rates, pressure-work outcomes and actual resource use together. All temporary database containers/volumes are removed on exit.

## Operation and physical-standby example

```sh
python3 run.py --profile middleware-fixed --operations pool --verify
python3 run.py --profile proxy-adaptive --operations pool
python3 operation_benchmark.py --output /tmp/operation-results.json
```

`--operations` accepts disabled/fixed/pool. Disabled bypasses public primary/follower SQL admission, fixed adds a four-operation budget with one critical reservation, and pool also samples actual pool utilization to restrict background jobs. All modes keep the six-worker executor and bounded queue of 16. The administrative stream probe always uses sqlguard. Import `postman/Operations.postman_collection.json` with the printed environment. The collection checks real transactions, standby reads, stream lifetime, pool exhaustion/deadline cleanup, worker execution and recovery.

The launcher adds a disposable physical standby, not an HTTP application. Its named replication connection uses demo-only credentials. The benchmark pauses real WAL replay and generates writes to create a backlog, then resumes; it also stops/restarts the standby and distinguishes unknown data from pressure. A four-second idle check verifies that an idle caught-up standby remains healthy. All fixtures are uniquely named and removed at shutdown. This is a local fault experiment, not a production database topology or durable job broker.

HTTP, worker and SQL engines have independent budgets. SQL permits span connection acquisition through statement completion, rows Close/terminal iteration or transaction Commit/Rollback. Cancellation alone never frees active work; transaction control waits for driver cleanup/timeouts. Use transaction methods and tx.Context inside InTx, always close rows, and do not reuse the same active engine recursively. Samples use InUse/MaxOpen and WAL byte distance plus heartbeat freshness; no fake lag values. Driver completion is not proof of server-side termination after ambiguous network failure. Default benchmark repeats four HTTP deployments and three SQL policies three times; p95 is successful latency, paired with rejections, deadline responses, worker outcomes and pool waiting.
