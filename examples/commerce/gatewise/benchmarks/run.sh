#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
backend=${BENCHMARK_BACKEND_DIR:-"$repo/../benchmark-backend"}
go_bin=${GO_BIN:-go}

if [ ! -f "$backend/go.mod" ]; then
  echo "Independent fixture missing: $backend (set BENCHMARK_BACKEND_DIR)" >&2
  exit 1
fi

(
  cd "$repo"
  "$go_bin" build -o bin/gatewise ./cmd/gatewise
  "$go_bin" build -o bin/gatewise-bench ./cmd/gatewise-bench
)
(
  cd "$backend"
  GOWORK=off "$go_bin" build -o bin/benchmark-backend ./cmd/benchmark-backend
)
cd "$repo"
exec ./bin/gatewise-bench -backend-bin "$backend/bin/benchmark-backend" "$@"
