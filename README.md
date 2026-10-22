<p><img src="public/logo.svg" alt="Gatewise logo" width="80" height="80" /></p>

# Gatewise documentation site

A separate React + TypeScript + Vite site for the Gatewise HTTP admission library and proxy. The library repository does not depend on this project.

## Develop

```sh
npm ci
npm run dev
```

Vite prints the local development URL. `npm run build` type-checks and writes a static site to `dist/`; `npm run preview -- --port 4173` serves that build. Hash-based navigation works on static hosting without server rewrites. GitHub Pages deployment is managed by `.github/workflows/pages.yml`; public assets use relative URLs to support the project subpath.

## Structure

- `src/pages/`: compact animated home, problem/design, installation/deployment, examples, benchmarks, metrics and reference.
- `src/components/`: documentation sidebar/search, code copying, charts and diagrams.
- `src/data/`: documentation content and measured benchmark summaries.
- `public/`: the original SVG logo, editable diagrams, downloadable charts, measured results and source-only demo ZIP.
- `examples/demo/`: self-contained demo source snapshots, launcher, Postman files and its own README.
- `tools/`: regenerate diagrams/charts or package the example download.

## Refresh examples

```sh
# Repackage the checked-in demo sources:
npm run package:demo
# Explicitly refresh source snapshots from the sibling Go projects first:
python3 tools/package_demo.py --sync-sources --projects ..
```

The sync option replaces only the managed example module snapshots. Make changes in the actual library/backend projects before syncing. Postman source is owned by `examples/demo/postman/`. Neither operation copies binaries, personal dotenv files, old logs or historical benchmark results. The ZIP includes SHA-256 source manifests and preserves executable shell scripts.

## Refresh diagrams and charts

```sh
npm run assets -- --results public/data/benchmark-results.json
```

One canonical model generates each SVG and editable `.drawio` file. Open `.drawio` files in diagrams.net to edit them; change the generator model to make future regeneration preserve those edits.

The measured benchmark JSON retains all 15 runs and their original values; hostnames, PIDs and local filesystem paths have been removed from the public export. Do not replace measured values with illustrative data. Benchmark phase-summary lines connect measured endpoints, not continuous scrape series. PNG benchmark figures are exported from that same report. The Metrics page instead uses native Grafana captures from a separate 50-second local run, with source time series and workload outcomes in `public/data/metrics-capture.json`. Only the home request-flow illustration animates; it supports pause and reduced-motion preferences. All technical pages use the documentation layout.

## Regenerate measured images

```sh
python3 -m venv /tmp/gatewise-charts
/tmp/gatewise-charts/bin/pip install matplotlib==3.11.2
/tmp/gatewise-charts/bin/python tools/export_benchmark_images.py
# Native Grafana PNG captures require Podman and built Go demo binaries:
python3 tools/capture_metrics.py --help
```

The capture tool starts an isolated local workload and monitoring stack, exports the dashboard panels, then cleans up its processes and containers. Its workload is distinct from the 15-run overload benchmark. Deployment instructions are examples; the systemd unit has not been exercised on a Linux host.

## Content and release status

Engineering references are linked on the Problem & design page. Explanations and figures describe Gatewise and are original; Uber's article text and figures are not copied. The color palette is inspired by C&E Growth Partners. Fonts are self-hosted; their licenses are in `public/fonts/`.

The installation guide targets Gatewise v0.1.0 at github.com/HN026/gatewise. The self-contained demo retains local module replacements for its included snapshots. Production operations guidance is separate from the locally measured evidence; production adoption or multi-host capacity is not claimed.

## Published projects

- Library: https://github.com/HN026/gatewise
- Website: https://hn026.github.io/gatewise-site/
- License: MIT (font license notices are retained separately).

## Maintain release history

Add published releases to `src/data/releases.ts`, newest first. Record the GitHub publication timestamp, local publication date (IST), requirements, changes, upgrade notes and tag-specific download links. The Versions page and sidebar version link use this shared data. Verify the GitHub tag and assets exist before publishing the site.
