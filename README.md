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

## Reproduce release validation

The current reliability protocol uses four independent three-minute PostgreSQL runs, one for each fixed/adaptive middleware/proxy deployment. It checks overload, SQL delay and errors, caller deadlines, cleanup and recovery. Stop competing local load before collecting a report. The short `--quick` mode is exploratory and cannot qualify a release.

```sh
python3 examples/commerce/reliability_benchmark.py --engine podman
# Plot the completed report with Matplotlib installed:
python3 tools/export_reliability.py
# Validate the exact ZIP and all saved Postman profiles against real PostgreSQL:
python3 tools/verify_demo.py --engine podman
```

The release-example workflow can run this full protocol on Linux as well. Local and Linux reports remain separate because their hosts differ. The original failed exploratory smoke is retained alongside its exact recovery counts. Memory/goroutine checks are bounded observations, not proof of long-term leak freedom or production capacity.

Earlier policy, controller, tenant, priority, resource and SQL/standby experiments retain their own raw reports, settings and source provenance. Use the matching release example and its README to reproduce a historical dataset. Recorded dashboard images come from separate monitoring captures; they are not additional repetitions of a performance experiment. The library is independent of PostgreSQL and these example APIs.

## Releases and deployment

The site targets Gatewise v1.0.0. Add published versions to `src/data/releases.ts`, newest first, with verified GitHub timestamps, IST dates, requirements, upgrade notes and tag-specific downloads. The Versions page and sidebar use this shared source. Never change an existing release tag to update the site.

The Pages workflow type-checks/builds and deploys `main`. Library: https://github.com/HN026/gatewise · Site: https://hn026.github.io/gatewise-site/ · License: MIT.

## Tenant-isolation evidence

`python3 examples/commerce/tenant_benchmark.py --output /tmp/tenant-results.json` runs three repeats across ten adapters, using the included snapshots. Export the full passed report with `python3 tools/export_tenant_benchmark.py --report /tmp/tenant-results.json` in a Matplotlib environment. Current tenant figures are separate from the retained v0.2.0 service-overload experiment. The adapter interface preserves application, generator and measurement contracts; the recorded Cinnamon audit does not claim that a direct Cinnamon comparison has been completed.

Priority-chain reproduction: `python3 examples/commerce/priority_benchmark.py --output /tmp/priority-results.json`; publish a complete passed 12-run report with `python3 tools/export_priority_benchmark.py --report /tmp/priority-results.json` in a Matplotlib environment. Historical v0.2/v0.3 measurements retain their original labels.
