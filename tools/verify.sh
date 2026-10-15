#!/bin/sh
# Everything the repository can check, in Docker, so the host needs only Docker:
# gofmt, go vet, go test -race (unit tests, no network), the production and
# simulator images, the compose files, and the release notes of every
# CHANGELOG.md version. The integration test is separate: tools/integration.sh.
#
#   sh tools/verify.sh
set -eu
cd "$(dirname "$0")/.."
sh tools/release-notes.sh --check
docker build --target test -t youless-tig:test .
docker build -t youless-tig:dev .
docker build --target sim -t youless-sim:dev .
for f in docker-compose.yml docker-compose.stack.yml; do
	YOULESS_URL=http://youless.example TZ=UTC INFLUX_URL=http://influxdb.example:8086 INFLUX_TOKEN=x \
		INFLUX_ORG=o INFLUX_BUCKET=b INFLUXDB_ADMIN_PASSWORD=x GRAFANA_ADMIN_PASSWORD=x \
		docker compose --env-file /dev/null -f "$f" config --quiet
done
echo "verify: OK"
