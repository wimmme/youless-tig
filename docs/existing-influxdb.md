# youless-tig with your own InfluxDB

`docker-compose.yml` runs only youless-tig. It writes to and reads from an
InfluxDB 2.x you already run; your Grafana shows the data.

## 1. Prepare InfluxDB

Create a bucket (e.g. `youless`) and a token with **read and write** access to
that bucket only: [docs/influxdb.md](influxdb.md). youless-tig needs read
access for the log pages of its API and to know where to continue after a restart.

## 2. Configure and start

```sh
mkdir youless-tig && cd youless-tig
curl -fsSLO https://raw.githubusercontent.com/wimmme/youless-tig/main/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/wimmme/youless-tig/main/.env.example -o .env
chmod 600 .env
```

In `.env` fill in the *Required* part: `YOULESS_URL`, `TZ`, `INFLUX_URL` (e.g.
`http://192.168.1.10:8086`; use the container name if InfluxDB runs on the same
Docker network), `INFLUX_ORG`, `INFLUX_BUCKET` and `INFLUX_TOKEN`. The API is
published on port 8030; change `YOULESS_TIG_PORT` under *Optional* if that port
is taken. Then:

```sh
docker compose up -d
docker compose logs -f
```

If InfluxDB runs in Docker on the same host, the youless-tig container must be
able to reach it: either through the host's address and published port, or by
joining InfluxDB's network. For the latter add to `docker-compose.yml`:

```yaml
services:
  youless-tig:
    networks: [default, influx]
networks:
  influx:
    external: true
    name: <the network of your InfluxDB container>
```

## 3. Optional: write through your Telegraf

If your Telegraf already feeds InfluxDB, youless-tig can send its writes there
instead, for instance to use Telegraf's buffering. Add a listener to your
`telegraf.conf`:

```toml
[[inputs.influxdb_v2_listener]]
  service_address = ":8186"
  token = "a-long-random-string"   # the same as INFLUX_WRITE_TOKEN
```

and set in `.env`:

```sh
INFLUX_WRITE_URL=http://<telegraf host>:8186
INFLUX_WRITE_TOKEN=a-long-random-string
```

Telegraf writes to its own output's bucket. youless-tig still reads from
`INFLUX_URL`, so `INFLUX_TOKEN` then needs only read access to the bucket the
data ends up in.

## 4. Grafana

Add an InfluxDB data source using InfluxQL and import the dashboard:
[docs/grafana.md](grafana.md).
