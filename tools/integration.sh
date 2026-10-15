#!/bin/sh
# Integration test: starts the complete stack of docker-compose.stack.yml with the
# simulated YouLess (test/integration/stack.override.yml), runs the Go tests in
# test/integration against it, and removes everything again. Needs Docker with
# Compose 2.24 or newer; touches no real device and publishes no ports.
#
#   sh tools/integration.sh            (KEEP=1 to leave the stack running)
set -eu
cd "$(dirname "$0")/.."
project="${PROJECT:-ytig-it}"
dc() {
	docker compose -p "$project" --env-file test/integration/test.env \
		-f docker-compose.stack.yml -f test/integration/stack.override.yml "$@"
}
cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		echo "── logs ──"
		dc logs --no-color --tail 40 youless-tig telegraf influxdb || true
	fi
	[ -n "${KEEP:-}" ] || dc --profile test down -v --remove-orphans >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT INT TERM

# The compose file for an existing InfluxDB must at least be valid.
INFLUX_URL=http://influxdb.example:8086 INFLUX_TOKEN=example \
	docker compose --env-file test/integration/test.env -f docker-compose.yml config --quiet \
	|| { echo "docker-compose.yml is not valid" >&2; exit 1; }

dc --profile test down -v --remove-orphans >/dev/null 2>&1 || true
dc up -d --build --wait --wait-timeout 300
dc run --rm tester
echo "integration: OK"
