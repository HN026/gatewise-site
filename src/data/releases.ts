// Newest published release first. Keep notes and downloads tied to its Git tag.
export const releases = [
{
  "version": "v0.9.0",
  "publishedAt": "2026-11-08T14:54:10Z",
  "date": "8 November 2026",
  "summary": "Opt-in stable adaptive feedback with priority-aware latency checks, bounded movement and clearer controller diagnostics.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "Stable is an explicit profile for middleware, native operations and the proxy. Legacy remains the default; explicit settings and operator bounds remain authoritative.",
    "Growth requires real refused demand and healthy headroom. Priority-tier feedback, rising-latency holds, sparse holds, idle reset and binding-signal coordination are explained in diagnostics and setup.",
    "927,540 HTTP requests across 18 fixed-code repeated comparisons; all 210 invariant/comparison checks passed. Reports retain successful work, p95, rejection/errors, per-service tiers, periodic movement and censored recovery observations.",
    "Exact source-only demo passed 2,172 Postman assertions across 59 profiles. Race tests, vet and CI passed; dashboards and reproducible comparison scripts are included."
  ],
  "upgrade": "Opt-in: set AdaptiveConfig.Profile / adaptive.profile to stable in adaptive mode. Keep explicit bounds and latency budgets; selecting a profile does not erase existing overrides. Defaults still start at reservation +1. Inspect selected control feedback separately from aggregate p90 and inspect effective signal limits separately from the adaptive baseline. Sparse feedback can produce an observable hold without meeting the ordinary sample minimum. Use keyed config literals. Roll back to legacy and restart. No queue, retry, preemption, production-capacity guarantee or automatic operator-bound tuning is added. Legacy defaults also need zero advanced overrides; measured preset behavior is not a human tuning-effort study.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.9.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.9.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.9.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.9.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.9.0/postman.zip"
    },
    {
      "name": "Controller comparison \u00b7 gzip",
      "path": "releases/download/v0.9.0/adaptation-results.json.gz"
    },
    {
      "name": "Criteria and calculations",
      "path": "releases/download/v0.9.0/adaptation-provenance.json"
    },
    {
      "name": "Recorded environment",
      "path": "releases/download/v0.9.0/adaptation-environment.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.9.0/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.8.1",
  "publishedAt": "2026-11-08T13:14:54Z",
  "date": "8 November 2026",
  "summary": "Optional SQL contention feedback and existing capacity sharing improve background progress in the measured example.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "New SQLPoolContention provider distinguishes occupancy from new pool waits. It returns 0/1, uses a construction-time counter baseline, and requires a configurable quiet interval plus a nonfull pool to recover. Snapshot explains the last observation.",
    "Opt-in balanced demo policy combines contention feedback with existing 3:1 interactive/batch sharing and borrowing. Server-owned classes use the tenant-policy dimension; existing configurations and controller defaults are unchanged.",
    "24,552 requests in 24 repeated comparison runs and 248 passing checks. Critical/background tradeoffs, recovery, rejection/error counts and min/max repeat ranges are published separately from retained v0.8.0 evidence.",
    "Exact source-only demo passed 1,902 Postman assertions across 53 profiles, including actual pool-pressure activation and background recovery. Race tests, vet and CI passed."
  ],
  "upgrade": "Opt-in. Existing SQLPool and decision semantics remain unchanged. To try contention feedback, construct regulator.NewSQLPoolContention, collect in the background and use a rule threshold of 1. RecoveryQuiet defaults to 200 ms; earlier cumulative waits are ignored. The balanced example also needs explicit server-owned workload sharing. A pressure cap 1 with reservation 1 still blocks background work during actual contention. There is no new queue, retry, preemption, starvation bound or replacement controller. Repeat with your own operation costs and capacity before adoption.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.8.1.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.8.1.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.8.1/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.8.1/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.8.1/postman.zip"
    },
    {
      "name": "Pool comparison \u00b7 gzip",
      "path": "releases/download/v0.8.1/pool-balance-results.json.gz"
    },
    {
      "name": "Calculations and criteria",
      "path": "releases/download/v0.8.1/pool-balance-provenance.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.8.1/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.8.0",
  "publishedAt": "2026-11-08T11:28:32Z",
  "date": "8 November 2026",
  "summary": "One admission engine for HTTP, worker jobs and SQL lifetimes, with real pool and physical-standby signals.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "Transport-neutral operation.Engine with trusted priority/tenant/resource metadata, Begin/Do, exact-once completion, context propagation and explicit same-engine nesting rules. HTTP middleware and proxy use this shared core.",
    "Driver-independent sqlguard statements, streamed rows and transaction ownership. InTx commits on success and rolls back on error/panic; cancellation retains permits through actual cleanup.",
    "SQLPool utilization and primary-side PostgreSQL physical-standby observation with WAL byte backlog, heartbeat freshness and explicit unknown-data handling.",
    "Bounded work-kind/outcome/duration metrics, updated dashboards, technical installation/reference and an independent worker/storage/standby demo with four HTTP application processes maximum.",
    "28,080 requests in 36 contention runs, four physical-standby profiles and 528 passing checks. Exact downloadable demo: 1,794 Postman assertions across 49 profiles."
  ],
  "upgrade": "Existing HTTP middleware/proxy consumers retain their entry points; operation/SQL integration is opt-in. Use separate engines for HTTP, worker and database capacity. Pass Permit.Context/tx.Context, use transaction methods inside InTx and always close rows. Configure driver/network timeouts for transaction control. Sampling does not preempt work, and driver cleanup does not establish engine-native database enforcement or server-side termination after ambiguous failures. Do not sum layered work metrics as end-to-end throughput. Limits remain per engine/process.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.8.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.8.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.8.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.8.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.8.0/postman.zip"
    },
    {
      "name": "Operation report \u00b7 gzip",
      "path": "releases/download/v0.8.0/operation-results.json.gz"
    },
    {
      "name": "Chart calculations",
      "path": "releases/download/v0.8.0/operation-chart-provenance.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.8.0/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.7.0",
  "publishedAt": "2026-11-08T10:31:52Z",
  "date": "8 November 2026",
  "summary": "Measured memory/goroutine signals and atomic planned write-byte/hot-key protection in the shared decision engine.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "Optional regulator.Memory and Goroutines providers measure backend runtime pressure. Memory uses explicit Go budgets/finite GOMEMLIMIT and visible Linux cgroup limits; proxy mode reads the protected backend, not its own runtime.",
    "Decision-engine budgets combine planned concurrent logical write bytes with per-key concurrency. Keys are hashed, tracking/expiry/fallback are bounded, and all matching reservations commit or reject together.",
    "Trusted CostResolver and reserved proxy metadata, observe/enforce accounting, rollback after later rejection and handler-lifetime release. Cancellation alone does not release still-running work.",
    "Resource explanations, bounded metrics/dashboard panels, offline configuration checks and detailed middleware/proxy installation examples.",
    "Real allocations, fanout, persisted PostgreSQL writes and hot-key contention: 87,840 requests, 36 runs, 396 passing checks. The exact demo passed 1,506 Postman assertions across 37 profiles, with a separate real 256MiB Linux cgroup-v2 check."
  ],
  "upgrade": "Opt-in: existing consumers can leave resource providers/budgets unset. Budgets require a trusted CostResolver (proxy: cost_metadata.trusted_peers); derive logical cost and keys from the validated operation plan, not body length or untrusted headers. X-Gatewise-Cost is reserved and unresolved values are stripped by the proxy. Start in observe mode. Memory/goroutine signals are sampled and can overshoot; write/key reservations are atomic. Raw keys never become diagnostics/labels. Limits are per engine/process, and work outliving a handler needs separate accounting.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.7.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.7.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.7.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.7.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.7.0/postman.zip"
    },
    {
      "name": "Resource report \u00b7 gzip",
      "path": "releases/download/v0.7.0/resource-results.json.gz"
    },
    {
      "name": "Chart calculations",
      "path": "releases/download/v0.7.0/resource-chart-provenance.json"
    },
    {
      "name": "Measured overhead",
      "path": "releases/download/v0.7.0/resource-overhead.txt"
    },
    {
      "name": "Linux measurement check",
      "path": "releases/download/v0.7.0/resource-platform-validation.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.7.0/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.6.0",
  "publishedAt": "2026-11-08T09:04:32Z",
  "date": "8 November 2026",
  "summary": "An optional shared decision engine combines cached overload signals with existing service, tenant and priority controls.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "Service, resource and trusted-actor observations include measurement time, freshness bounds and explicit unknown-data fallback. The smallest matching ceiling wins; explicit rejection takes precedence.",
    "Atomic admission retains hard service, tenant, priority and cohort rules. Signal ceilings do not overwrite the adaptive baseline, including during concurrent limit changes.",
    "Observe mode records proposals without signal enforcement. Background or push collection handles timeout/error/panic, stale/out-of-order/future data and bounded stuck providers.",
    "Inspectable admission stages, bounded Prometheus diagnostics, dashboard panels, offline proxy config validation and complete middleware/proxy setup.",
    "The final PostgreSQL experiment sent 86,400 requests across 36 runs and passed 348 checks. The exact demo passed 1,158 Postman assertions across 25 profiles. Raw evidence, measured charts and code-path overhead are included."
  ],
  "upgrade": "Opt-in: leave ServiceConfig.Decisions or services.name.decisions unset to retain existing integration behaviour. Start in observe mode, choose explicit freshness/failure policies, then enable enforcement after validation. Limits are process-local; scoped caps compare shared service occupancy. Providers must honour cancellation; a noncooperative provider can retain one worker per signal. The demo providers are controlled inputs, not automatic runtime/database regulators.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.6.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.6.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.6.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.6.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.6.0/postman.zip"
    },
    {
      "name": "Signal report \u00b7 gzip",
      "path": "releases/download/v0.6.0/decision-results.json.gz"
    },
    {
      "name": "Chart calculations",
      "path": "releases/download/v0.6.0/decision-chart-provenance.json"
    },
    {
      "name": "Measured overhead",
      "path": "releases/download/v0.6.0/decision-overhead.txt"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.6.0/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.5.0",
  "publishedAt": "2026-11-08T08:23:49Z",
  "date": "8 November 2026",
  "summary": "Optional tenant sharing and bounded borrowing, with protection for returning demand and measured capacity/latency tradeoffs.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "TenantConfig.Sharing adds target shares, separate total borrowing ceilings, identity-specific overrides and a configurable recent-demand window. Hard caps remain the default.",
    "Targets scale when demand exceeds current capacity; below-share capacity rejection pauses new lending while running work drains. Service, priority and cohort limits still apply.",
    "Aggregate demand/allocation metrics, a tenant_share_reserved rejection reason, admission headers and updated dashboards explain allocation without identity labels.",
    "The demo compares hard cap 2 against share 2 with ceilings 4/12: 249,696 requests, 36 runs and 456 passing checks. A separate live handoff test passed 28 checks; the extracted demo passed 758 Postman assertions across 17 profiles.",
    "Plain-language setup, editable handoff diagram and raw reports explain both the throughput benefit and extra waiting/rejections from a larger ceiling."
  ],
  "upgrade": "Sharing is opt-in. With it enabled, tenant MaxConcurrent/Limits become target shares; Sharing.MaxConcurrent/Limits set total ceilings. DemandTTL defaults to 500ms and must not exceed IdleTTL. Remove sharing environment overrides as well as the JSON object to disable it. Shares apply per guard/process; running work is not preempted and latency or starvation freedom is not guaranteed. Read per-tenant p95 and rejection rates alongside total throughput.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.5.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.5.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.5.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.5.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.5.0/postman.zip"
    },
    {
      "name": "Sharing report \u00b7 gzip",
      "path": "releases/download/v0.5.0/sharing-results.json.gz"
    },
    {
      "name": "Live handoff validation",
      "path": "releases/download/v0.5.0/sharing-validation.json"
    },
    {
      "name": "Chart calculations",
      "path": "releases/download/v0.5.0/sharing-chart-provenance.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.5.0/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.4.0",
  "publishedAt": "2026-11-08T07:45:26Z",
  "date": "8 November 2026",
  "summary": "Ordered priority tiers, trusted context/HTTP propagation and rotating cohorts through a real downstream service chain.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "Optional 2\u20138 ordered tiers and cumulative reservations, with explicit Critical/Background compatibility mappings.",
    "Authenticated application-context integration, immediate-peer trust checks and an outbound transport restricted to configured destination hosts.",
    "Optional deterministic cohort ordering with UTC rotation, local-headroom admission and bounded tier/source diagnostics.",
    "A database-backed orders-to-catalog flow, editable architecture diagram, per-tier benchmark charts and updated Postman/demo bundles.",
    "Measured 102,960 requests across 12 runs: 120 checks passed, 16,221 successful flows preserved importance, and 16,220 same-epoch cohort comparisons matched. A separate trust-boundary experiment passed 56 checks; 614 Postman assertions passed across 13 profiles."
  ],
  "upgrade": "Tier policy is opt-in. Without it, existing service admission retains two-class rules. With tiers, set ReservedCritical/reserved_critical to zero and use tier reservations; adaptive minimum must exceed their sum. Authenticate context values and configure inbound peers/outbound hosts explicitly. X-Gatewise-Priority is reserved and unresolved values are stripped by the proxy, including legacy routes. Cohort consistency depends on settings and clocks; no identical-rejection, starvation-freedom or latency guarantee is made.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.4.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.4.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.4.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.4.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.4.0/postman.zip"
    },
    {
      "name": "Priority report \u00b7 gzip",
      "path": "releases/download/v0.4.0/priority-results.json.gz"
    },
    {
      "name": "Trust-boundary validation",
      "path": "releases/download/v0.4.0/priority-validation.json"
    },
    {
      "name": "Chart provenance",
      "path": "releases/download/v0.4.0/priority-chart-provenance.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.4.0/commerce-contract-results.json"
    }
  ]
},
{
  "version": "v0.3.0",
  "publishedAt": "2026-11-07T11:46:17Z",
  "date": "7 November 2026",
  "summary": "Optional tenant and caller isolation with bounded accounting, trusted identity handling and measured PostgreSQL noisy-neighbor results.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "TenantConfig supports default and per-identity concurrency caps, bounded tracking, idle expiry and explicit missing/overflow policies in middleware and proxy modes.",
    "Tenant identity resolves from authenticated context or a dedicated header from configured trusted immediate peers. Forwarded IP headers do not establish trust.",
    "Tenant rejection reasons and aggregate tracking/in-flight metrics are available without identity labels; dashboards and installation examples include tenant controls.",
    "The source-only commerce demo includes an extensible comparison adapter, tenant load scenarios and separate Postman tenant contracts.",
    "Published evidence includes 239,040 requests, 30 runs, 264 passing experiment checks and 498 passing Postman assertions across nine profiles. Hard caps protected ordinary traffic while reducing aggregate throughput."
  ],
  "upgrade": "Tenant isolation is opt-in; existing configurations retain service-level admission. Limits apply per guard/process, not across replicas. Authenticate identities before resolving them, and make trusted ingress overwrite the identity header. Hard caps do not borrow spare capacity. No direct Cinnamon comparison is claimed.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.3.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.3.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.3.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.3.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.3.0/postman.zip"
    },
    {
      "name": "Tenant report \u00b7 gzip",
      "path": "releases/download/v0.3.0/tenant-results.json.gz"
    },
    {
      "name": "Chart provenance",
      "path": "releases/download/v0.3.0/tenant-chart-provenance.json"
    },
    {
      "name": "Accounting microbenchmark",
      "path": "releases/download/v0.3.0/tenant-accounting-overhead.txt"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.3.0/commerce-contract-results.json"
    },
    {
      "name": "Cinnamon availability audit",
      "path": "releases/download/v0.3.0/cinnamon-availability.json"
    }
  ]
},
{
  "version": "v0.2.0",
  "publishedAt": "2026-11-07T09:30:29Z",
  "date": "7 November 2026",
  "summary": "Evaluated-window controller visibility, offline configuration validation, and PostgreSQL-backed overload and recovery evidence.",
  "requirements": "Library: Go 1.22+; commerce example: Go 1.25+",
  "changes": [
    "ControllerSnapshot and adaptive-only metrics expose evaluated samples, normalized p90, failure fraction, saturation, decision and evaluation time.",
    "The proxy supports -check-config to validate resolved configuration without starting listeners or contacting backends.",
    "The demo now contains independent PostgreSQL-backed orders, catalog and inventory services, plus the optional Gatewise proxy.",
    "Published measurements include 2,280,750 requests across 15 runs, six gradual-slowdown validation runs and 370 passing Postman assertions.",
    "Installation examples cover adaptive mode in both integrations, practical limit tuning and controller diagnostics; fresh metrics images use the commerce services."
  ],
  "upgrade": "Existing service configuration and middleware construction remain supported. The controller adjustment algorithm is unchanged. The replacement demo needs Go 1.25+, Python 3.10+ and a container engine; its launcher prints dynamic addresses and a Postman environment. Review latency, rejection and pool-wait results before choosing limits.",
  "downloads": [
    {
      "name": "Source ZIP",
      "path": "archive/refs/tags/v0.2.0.zip"
    },
    {
      "name": "Source tar.gz",
      "path": "archive/refs/tags/v0.2.0.tar.gz"
    },
    {
      "name": "Demo example",
      "path": "releases/download/v0.2.0/gatewise-demo.zip"
    },
    {
      "name": "Demo SHA-256",
      "path": "releases/download/v0.2.0/gatewise-demo.sha256"
    },
    {
      "name": "Postman bundle",
      "path": "releases/download/v0.2.0/postman.zip"
    },
    {
      "name": "Measured report \u00b7 gzip",
      "path": "releases/download/v0.2.0/commerce-results.json.gz"
    },
    {
      "name": "Controller validation",
      "path": "releases/download/v0.2.0/controller-validation.json"
    },
    {
      "name": "Postman results",
      "path": "releases/download/v0.2.0/commerce-contract-results.json"
    }
  ]
},
  {
    version: 'v0.1.0',
    publishedAt: '2026-11-06T18:51:30Z',
    date: '7 November 2026',
    summary: 'Initial release of HTTP admission control for Go applications and HTTP backends.',
    requirements: 'Go 1.22 or newer',
    changes: [
      'Go middleware and a standalone HTTP reverse proxy.',
      'Per-service concurrency limits and capacity reserved for critical requests.',
      'Optional adaptive admission using request latency and error feedback.',
      'Named-service routing, JSON configuration, environment overrides and graceful shutdown.',
      'Prometheus metrics, a Grafana dashboard and a local benchmark runner.',
    ],
    upgrade: 'Initial release. Existing source-based integrations should use github.com/HN026/gatewise imports and pin v0.1.0 in go.mod.',
    downloads: [
      { name: 'Source ZIP', path: 'archive/refs/tags/v0.1.0.zip' },
      { name: 'Source tar.gz', path: 'archive/refs/tags/v0.1.0.tar.gz' },
      { name: 'Demo example', path: 'releases/download/v0.1.0/gatewise-demo.zip' },
      { name: 'Demo SHA-256', path: 'releases/download/v0.1.0/gatewise-demo.sha256' },
      { name: 'Postman bundle', path: 'releases/download/v0.1.0/postman.zip' },
    ],
  },
] as const;

export const latestRelease = releases[0];
export const repositoryUrl = 'https://github.com/HN026/gatewise';
