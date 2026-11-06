// Newest published release first. Keep notes and downloads tied to its Git tag.
export const releases = [
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
