# InfluxDB: bucket, token and retention

youless-tig works with InfluxDB 2.x. It writes with the v2 write API
(`/api/v2/write`) and reads with InfluxQL on `/query`; InfluxDB 2 maps the bucket
name to an InfluxQL database by itself (a *virtual DBRP mapping*), so nothing
needs to be set up for that.

The complete stack ([stack.md](stack.md)) does all of this for you. These steps
are for your own InfluxDB.

## Bucket

In the InfluxDB UI: *Load Data → Buckets → Create bucket*, name `youless`, delete
data *never* (or a retention you like, see below). Or with the CLI:

```sh
influx bucket create --org home --name youless --retention 0
```

## Token

Give youless-tig its own token, with access to this bucket only, never your
admin (operator) token.

In the UI: *Load Data → API Tokens → Generate API Token → Custom API Token*,
tick **Read** and **Write** for the bucket `youless`, and nothing else. Or:

```sh
bucket_id=$(influx bucket list --org home --name youless --hide-headers | cut -f1)
influx auth create --org home --description "youless-tig" \
  --read-bucket "$bucket_id" --write-bucket "$bucket_id"
```

Put the token in `.env` as `INFLUX_TOKEN` (or in a file, see `INFLUX_TOKEN_FILE`
in [configuration.md](configuration.md)). For Grafana make another token with
**Read** only.

## How much space it takes

With the defaults (a reading every 10 seconds, a P1 meter with three phases,
gas and water) youless-tig writes about 9,000 points a day plus the device logs,
roughly 50–100 MB a year in InfluxDB's compressed storage. Keeping everything
forever is fine for years on any home server.

## Retention

To keep less, set the bucket's retention, e.g. two years:

```sh
influx bucket update --id "$bucket_id" --retention 104w
```

Retention applies to the whole bucket. To keep the day totals forever but the
10-second readings only for a while, put youless-tig in a short-retention bucket
and copy `youless_log` with `resolution = 'day'` to a second bucket with an
InfluxDB task, or simply keep everything: the day and hour logs are small.

## Checking the data

In the InfluxDB UI's Data Explorer, or with InfluxQL:

```sh
influx v1 shell --org home
> USE youless
> SELECT last("power_w") FROM "youless_electricity"
> SELECT count("value") FROM "youless_log" GROUP BY "channel", "resolution"
```

All measurements and fields: [data-model.md](data-model.md).
