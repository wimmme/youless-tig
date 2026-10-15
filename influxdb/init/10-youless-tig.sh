#!/bin/sh
# Runs once, when the InfluxDB container sets itself up for the first time
# (DOCKER_INFLUXDB_INIT_MODE=setup). Creates one token per service with only the
# rights it needs, and writes each to the shared "secrets" volume:
#
#   telegraf_token            write to the bucket          (Telegraf → InfluxDB)
#   youless_tig_token         read the bucket              (youless-tig's API queries)
#   grafana_token             read the bucket              (Grafana's data source)
#   telegraf_listener_token   random, not an InfluxDB token (youless-tig → Telegraf)
#
# The admin token is not handed to any service.
set -eu

dir=/etc/influxdb2/secrets
mkdir -p "$dir"
org="$DOCKER_INFLUXDB_INIT_ORG"
bucket_id="$(influx bucket list --org "$org" --name "$DOCKER_INFLUXDB_INIT_BUCKET" --hide-headers | cut -f1)"
[ -n "$bucket_id" ] || { echo "youless-tig init: bucket $DOCKER_INFLUXDB_INIT_BUCKET not found" >&2; exit 1; }

# token <file> <description> <permission flags…>
token() {
	file="$1" desc="$2"
	shift 2
	influx auth create --org "$org" --description "$desc" "$@" --hide-headers | cut -f3 | tr -d '\n' > "$dir/$file.tmp"
	[ -s "$dir/$file.tmp" ] || { echo "youless-tig init: no token for $file" >&2; exit 1; }
	chmod 0644 "$dir/$file.tmp"
	mv "$dir/$file.tmp" "$dir/$file"
}

token telegraf_token "telegraf: write $DOCKER_INFLUXDB_INIT_BUCKET" --write-bucket "$bucket_id"
token youless_tig_token "youless-tig: read $DOCKER_INFLUXDB_INIT_BUCKET" --read-bucket "$bucket_id"
token grafana_token "grafana: read $DOCKER_INFLUXDB_INIT_BUCKET" --read-bucket "$bucket_id"

head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$dir/telegraf_listener_token.tmp"
chmod 0644 "$dir/telegraf_listener_token.tmp"
mv "$dir/telegraf_listener_token.tmp" "$dir/telegraf_listener_token"

echo "youless-tig init: tokens written to the secrets volume"
