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
# InfluxDB runs only executable init scripts and skips the others silently.
[ -x influxdb/init/10-youless-tig.sh ] || { echo "influxdb/init/10-youless-tig.sh must be executable" >&2; exit 1; }
docker build --target test -t youless-tig:test .
docker build -t youless-tig:dev .
docker build --target sim -t youless-sim:dev .
fake() {
	YOULESS_URL=http://youless.example TZ=UTC INFLUX_URL=http://influxdb.example:8086 INFLUX_TOKEN=x \
		INFLUX_ORG=o INFLUX_BUCKET=b INFLUXDB_ADMIN_PASSWORD=x GRAFANA_ADMIN_PASSWORD=x "$@"
}
# ports <compose file> [VAR=value…]: the "container→host" port pairs it publishes.
ports() {
	f="$1"
	shift
	fake env "$@" docker compose --env-file /dev/null -f "$f" config --format json | tr -d ' \n' |
		grep -o '"target":[0-9]*,"published":"[0-9]*"' | sed 's/"target":\([0-9]*\),"published":"\([0-9]*\)"/\1→\2/' | sort | tr '\n' ' '
}
expect() {
	[ "$2" = "$3" ] || { echo "$1: ports '$2', want '$3'" >&2; exit 1; }
}
for f in docker-compose.yml docker-compose.stack.yml; do
	fake docker compose --env-file /dev/null -f "$f" config --quiet
done
# Default host ports avoid 8080, 3000 and 8086; .env changes only the host side.
expect docker-compose.yml "$(ports docker-compose.yml)" "8080→8030 "
expect docker-compose.yml "$(ports docker-compose.yml YOULESS_TIG_PORT=9000)" "8080→9000 "
expect docker-compose.stack.yml "$(ports docker-compose.stack.yml)" "3000→3030 8080→8030 8086→8031 "
expect docker-compose.stack.yml "$(ports docker-compose.stack.yml YOULESS_TIG_PORT=9000 GRAFANA_PORT=9001 INFLUXDB_PORT=9002)" \
	"3000→9001 8080→9000 8086→9002 "
# Each example settings file is complete for its compose file.
for pair in ".env.example docker-compose.yml" ".env.stack.example docker-compose.stack.yml"; do
	set -- $pair
	sed -e 's/^INFLUX_TOKEN=$/INFLUX_TOKEN=x/' -e 's/^\(INFLUXDB_ADMIN_PASSWORD\|GRAFANA_ADMIN_PASSWORD\)=$/\1=x/' "$1" > /tmp/youless-tig-verify.env
	docker compose --env-file /tmp/youless-tig-verify.env -f "$2" config --quiet || { echo "$1 does not fit $2" >&2; exit 1; }
done
rm -f /tmp/youless-tig-verify.env
echo "verify: OK"
