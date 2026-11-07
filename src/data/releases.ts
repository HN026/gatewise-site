// Newest published release first. Keep notes and downloads tied to its Git tag.
export const releases = [
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
