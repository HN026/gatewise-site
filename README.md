<p><img src="public/logo.svg" alt="Gatewise logo" width="80" height="80" /></p>

# Gatewise documentation site

Technical documentation and measured evidence for [Gatewise](https://github.com/HN026/gatewise). The Go library has no dependency on this React/TypeScript/Vite project.

## Development

```sh
npm ci
npm run dev
npm run build
npm run preview -- --port 4173
```

Only the home request-flow illustration animates; documentation pages support deep links through hash navigation. Assets use relative URLs for GitHub Pages. Fonts are self-hosted with retained licenses. The original palette takes inspiration from C&E Growth Partners; explanations, logo and diagrams are original. Engineering articles are referenced without copying their text or figures.

## Structure

- `src/pages/`: home, problem/design, installation/deployment, examples, benchmarks, metrics, reference and versions.
- `src/data/`: technical content, release history and summaries derived from measured reports.
- `examples/commerce/`: self-contained source snapshots, three PostgreSQL-backed service roles, launcher, open-loop benchmark, controller validation and Postman contract.
- `public/`: logo, editable diagrams, report data, recorded figures and source-only demo download.
- `tools/`: source packaging, diagram generation, measured-data export and native monitoring captures.

## Refresh the example

```sh
npm run package:demo
# Explicitly replace managed source snapshots from sibling projects:
python3 tools/package_demo.py --sync-sources --projects ..
```

Canonical modules are `../gatewise` and `../commerce-backend`. Make code changes there before syncing. The example launcher, workload and portable Postman contract are owned by `examples/commerce/`. Packaging excludes compiled binaries, local dotenv files, logs and result directories; its ZIP includes a SHA-256 source manifest. The replacement demo starts at most four HTTP application processes: three service roles and, in proxy mode, Gatewise. PostgreSQL and optional monitoring are infrastructure.

## Reproduce current measurements

Extract the demo and follow its README. It requires Go 1.25+, Python 3.10+ and running Podman or Docker. Default runs take about 32 minutes plus startup/drain; controller validation is separate. Stop competing load before measurement.

```sh
python3 examples/commerce/benchmark.py --output /tmp/commerce-results.json
python3 examples/commerce/validate_controller.py --output /tmp/controller-validation.json
# Plot measured results in an environment containing Matplotlib:
python3 tools/export_commerce_benchmark.py --report /tmp/commerce-results.json --controller-report /tmp/controller-validation.json
# Separate native Grafana captures; requires the official image renderer:
python3 tools/capture_commerce_metrics.py --projects examples/commerce
python3 tools/generate_commerce_diagram.py
```

The exporter requires all fifteen measured runs and their checks to pass. Figures preserve run-level percentiles, rejection/error counts and data provenance. Controller traces are sampled observations. The full report includes generator delivery, pool waits, backend heap/goroutines, runtime versions and binary hashes. Captured dashboard PNGs come from a separate monitoring run against the same real services, not the repeated performance experiment. Historical v0.1.0 synthetic data remains available in Git history and its tagged release; current documentation and default demo focus on commerce.

Measurements were made on macOS with Podman. They are local production-style workloads, not production capacity or long-duration soak evidence. Optional monitoring requires containers to reach host listeners; adapt scrape networking for your deployment. The library itself is independent of PostgreSQL and example APIs. Production operations, authentication, TLS and replica budgets remain application concerns.

## Releases and deployment

The site targets Gatewise v0.7.0. Add published versions to `src/data/releases.ts`, newest first, with verified GitHub timestamps, IST dates, requirements, upgrade notes and tag-specific downloads. The Versions page and sidebar use this shared source. Never change an existing release tag to update the site.

The Pages workflow type-checks/builds and deploys `main`. Library: https://github.com/HN026/gatewise · Site: https://hn026.github.io/gatewise-site/ · License: MIT.

## Tenant-isolation evidence

`python3 examples/commerce/tenant_benchmark.py --output /tmp/tenant-results.json` runs three repeats across ten adapters, using the included snapshots. Export the full passed report with `python3 tools/export_tenant_benchmark.py --report /tmp/tenant-results.json` in a Matplotlib environment. Current tenant figures are separate from the retained v0.2.0 service-overload experiment. The adapter interface preserves application, generator and measurement contracts; the recorded Cinnamon audit does not claim that a direct Cinnamon comparison has been completed.

Priority-chain reproduction: `python3 examples/commerce/priority_benchmark.py --output /tmp/priority-results.json`; publish a complete passed 12-run report with `python3 tools/export_priority_benchmark.py --report /tmp/priority-results.json` in a Matplotlib environment. Historical v0.2/v0.3 measurements retain their original labels.
