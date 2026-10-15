# Data model in InfluxDB

What youless-tig writes, for building your own dashboards and queries. Every
measurement has the tag `device` (the `DEVICE_NAME` setting, default `youless`),
so one bucket can hold several YouLess devices.

Timestamps are the device's own: the time of the meter reading, not the time
youless-tig read it. Reading the same value twice therefore writes the same
point twice, which InfluxDB stores once.

## Live readings

Written at every poll (`POLL_INTERVAL`, default 10 s) when the device has a new reading.

### `youless_electricity`

From `/e` and, with a P1 smart meter, `/f`. Time: the main meter reading (`tm`).

| Field | Unit | From | Notes |
|---|---|---|---|
| `power_w` | W | `pwr` | net power; negative when sending power back |
| `net_kwh` | kWh | `net` | net meter total |
| `import_t1_kwh`, `import_t2_kwh` | kWh | `p1`, `p2` | P1: taken from the grid, tariff 1 and 2 |
| `export_t1_kwh`, `export_t2_kwh` | kWh | `n1`, `n2` | P1: sent back, tariff 1 and 2 |
| `tariff` | 1 or 2 | `tr` | P1: current tariff (integer) |
| `voltage_l1_v`, `voltage_l2_v`, `voltage_l3_v` | V | `v1`…`v3` | P1, per phase; a phase without voltage (single-phase meter) is left out |
| `current_l1_a`, `current_l2_a`, `current_l3_a` | A | `i1`…`i3` | P1, per phase |
| `power_l1_w`, `power_l2_w`, `power_l3_w` | W | `l1`…`l3` | P1, per phase, net |
| `capacity_avg_w` | W | `pa` | Belgium only: average power of the running quarter hour |
| `capacity_peak_w` | W | `pp` | Belgium only: highest quarter-hour average this month |
| `capacity_peak_time` | Unix seconds | `pts` | Belgium only: when that peak was (integer) |

Without a P1 smart meter only `power_w` and `net_kwh` are written.

### `youless_gas`, `youless_water`

| Field | Unit | Notes |
|---|---|---|
| `total_m3` | m³ | meter total |

Time: the meter's own reading time (`gts`, `wts`). Gas and water meters send a
new value every 5 minutes (older ones every hour), so these points are that far
apart. Only written when the channel is collected (`CHANNELS`).

### `youless_s0`

The S0 pulse input, only with `CHANNELS` including `s0`. Time: the S0 reading (`ts0`).

| Field | Unit |
|---|---|
| `total_kwh` | kWh |
| `power_w` | W |

## The device's logs: `youless_log`

The YouLess keeps logs of its own: per minute (10 hours), per 10 minutes
(10 days), per hour (70 days) and per day (one year). youless-tig copies them,
so they are kept as long as your bucket keeps data, and gaps in the live data
(youless-tig or InfluxDB down) are filled from them.

| Tag | Values |
|---|---|
| `channel` | `electricity`, `gas`, `water`, `s0` |
| `resolution` | `minute`, `10min`, `hour`, `day` |

| Field | Notes |
|---|---|
| `value` | one value per interval, timestamped at the **start** of the interval |

The unit depends on the resolution, as on the device:

| Resolution | electricity, s0 | gas, water |
|---|---|---|
| `minute` | W, average over the minute | — (the device has no minute log for these) |
| `10min` | W, average over 10 minutes | L/h, average flow |
| `hour` | W, average over the hour (= Wh in that hour) | L/h (= litres in that hour) |
| `day` | kWh in that day | m³ in that day |

Electricity values are net: negative when more was sent back than taken.

## Example queries (InfluxQL)

Consumption per day for the last 30 days, from the day log:

```sql
SELECT "value" FROM "youless_log"
WHERE "device" = 'youless' AND "channel" = 'electricity' AND "resolution" = 'day'
  AND time > now() - 30d
```

Import and export per day, from the P1 meter readings:

```sql
SELECT spread("import_t1_kwh") + spread("import_t2_kwh") AS "import",
       spread("export_t1_kwh") + spread("export_t2_kwh") AS "export"
FROM "youless_electricity" WHERE "device" = 'youless' AND time > now() - 30d
GROUP BY time(1d) tz('Europe/Brussels')
```

The same in Flux, for InfluxDB 2's own Data Explorer:

```flux
from(bucket: "youless")
  |> range(start: -30d)
  |> filter(fn: (r) => r._measurement == "youless_log" and r.channel == "electricity" and r.resolution == "day")
```
