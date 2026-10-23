#!/bin/sh
set -eu

engine=${CONTAINER_ENGINE:-podman}
stack=${STACK_NAME:-gatewise-observability}
case "$stack" in *[!a-zA-Z0-9_.-]*|'') echo "Invalid STACK_NAME" >&2; exit 1;; esac
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
prometheus="$stack-prometheus"
grafana="$stack-grafana"

owned() {
 [ "$("$engine" inspect --format '{{index .Config.Labels "io.gatewise.stack"}}' "$1")" = "$stack" ] || {
  echo "Refusing to alter a container not owned by this stack: $1" >&2; exit 1;
 }
}

case "${1:-start}" in
 start)
  "$engine" network inspect "$stack" >/dev/null 2>&1 || "$engine" network create "$stack" >/dev/null
  if "$engine" container inspect "$prometheus" >/dev/null 2>&1; then
   owned "$prometheus"; "$engine" start "$prometheus" >/dev/null
  else
   # Podman on macOS supplies both host aliases through its built-in DNS.
   # Forcing host-gateway there can fail when no internal host IP is configured.
   case "$(basename "$engine")" in podman) set --;; *) set -- --add-host host.docker.internal:host-gateway;; esac
   "$engine" run -d --name "$prometheus" --label "io.gatewise.stack=$stack" \
    --network "$stack" --network-alias prometheus "$@" \
    -p "127.0.0.1:${PROMETHEUS_PORT:-9095}:9090" \
    -v "$directory/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
    -v "$stack-prometheus-data:/prometheus" \
    docker.io/prom/prometheus:v3.16.0 \
    --config.file=/etc/prometheus/prometheus.yml --storage.tsdb.retention.time=24h >/dev/null
  fi
  if "$engine" container inspect "$grafana" >/dev/null 2>&1; then
   owned "$grafana"; "$engine" start "$grafana" >/dev/null
  else
   "$engine" run -d --name "$grafana" --label "io.gatewise.stack=$stack" --network "$stack" \
    -p "127.0.0.1:${GRAFANA_PORT:-3000}:3000" \
    -e GF_SECURITY_ADMIN_USER=admin -e "GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_ADMIN_PASSWORD:-gatewise-local}" \
    -e GF_AUTH_ANONYMOUS_ENABLED=true -e GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer -e GF_USERS_ALLOW_SIGN_UP=false \
    -e GF_ANALYTICS_REPORTING_ENABLED=false -e GF_ANALYTICS_CHECK_FOR_UPDATES=false -e GF_ANALYTICS_CHECK_FOR_PLUGIN_UPDATES=false \
    -v "$directory/grafana/provisioning:/etc/grafana/provisioning:ro" \
    -v "$directory/grafana/dashboards:/var/lib/grafana/dashboards:ro" \
    -v "$stack-grafana-data:/var/lib/grafana" \
    docker.io/grafana/grafana:13.3.0 >/dev/null
  fi
  echo "Prometheus: http://127.0.0.1:${PROMETHEUS_PORT:-9095}"
  echo "Grafana: http://127.0.0.1:${GRAFANA_PORT:-3000}/d/gatewise-overview"
  ;;
 stop)
  for container in "$grafana" "$prometheus"; do
   if "$engine" container inspect "$container" >/dev/null 2>&1; then
    owned "$container"; "$engine" stop "$container" >/dev/null; "$engine" rm "$container" >/dev/null
   fi
  done
  echo "Stopped containers; data volumes retained."
  ;;
 status)
  for container in "$prometheus" "$grafana"; do
   "$engine" inspect --format '{{.Name}}: {{.State.Status}}' "$container"
  done
  ;;
 *) echo "Usage: $0 [start|stop|status]" >&2; exit 1;;
esac
