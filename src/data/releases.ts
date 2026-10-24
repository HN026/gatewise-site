// Newest published release first. Keep notes and downloads tied to its Git tag.
export const releases = [
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
