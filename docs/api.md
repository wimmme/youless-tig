# The YouLess-compatible API

youless-tig answers the read endpoints of a YouLess LS120 on its own port
(`YOULESS_TIG_PORT`, default 8080), in the same formats, so an existing YouLess
client can point at youless-tig instead of the device. Log pages come from
InfluxDB, so they reach back as far as your data does, not just as far as the
device's memory.

The endpoints and formats are described in the YouLess LS120 user manual
(chapter *Data interface / API*); this page lists how youless-tig answers them.

| Request | Answer |
|---|---|
| `GET /d` | device identification (model, firmware, MAC), as the device; refreshed hourly |
| `GET /e` | the last `/e` youless-tig read (at most `POLL_INTERVAL` old); 503 when there is no reading from the last minute (or 3 poll intervals) |
| `GET /f` | the last `/f` (P1 phase data), same rules |
| `GET /a`, `GET /a?f=j` | status text or JSON, read from the device at most once per poll interval |
| `GET /V?h=N` `/V?w=N` `/V?d=N` `/V?m=N` | electricity log: minute, 10-minute, hour and day pages |
| `GET /W?…`, `/K?…`, `/Z?…` | gas, water and S0 logs, same parameters |
| `GET /healthz` | youless-tig's own status (not on a YouLess), see below |

Anything else, and any other method than `GET` or `HEAD`, is refused. Settings,
login and the raw P1 telegram (`/V?p=1`) are never passed to the device: the
API cannot change the YouLess.

## Log pages

Pages work as on the device (observed on firmware 1.6.4):

- `h=N` (minute) and `w=N` (10 minutes): page 1 holds the 30 (48) most recent
  complete intervals, page 2 the ones before, and so on.
- `d=N` (hour): page N is one calendar day, page 1 is today up to the last
  complete hour.
- `m=N` (day): N is a month number (1–12): the most recent month with that
  number, up to yesterday.

The answer is byte for byte what the device would send, including its quirks:
values as text with a decimal comma, a sign column (`" 37,890"`, `"-2,360"`),
`"*"` for an interval without data, and a closing `null`:

```json
{"un":"Watt","tm":"2026-10-15T01:09:00","dt":60,"val":[" 2"," 3",…,null]}
```

Differences from the device:

- **Older pages.** Beyond the device's last page (minute page 20, 10-minute page
  30, hour page 70) youless-tig answers from InfluxDB as long as the page holds
  stored data, and 404 after that.
- **`m=N&y=YYYY`** selects the month of any year (an addition; the device only
  has the last twelve months).
- **Missing values** are `"*"`, for instance for times before youless-tig was
  first started.
- Before answering a recent page, youless-tig reads that page from the device
  if it did not in the last 30 seconds (minute log) to 15 minutes (day log), so
  recent pages are as fresh as the device's. Older pages never touch the device.

## Health

`GET /healthz` returns `200` with

```json
{"status":"ok","last_poll":"2026-10-15T08:00:00Z","channels":["electricity","gas","water"]}
```

and `503` with `"status":"device not read recently"` when the device has not
been read for 5 minutes. The Docker health check uses it.

## Security

The API has no password, like the device. It only reads, but it shows your
consumption to anyone who can reach it: publish the port only on your LAN
(`YOULESS_TIG_BIND`), not on the internet. Behind a reverse proxy you can add
authentication there; YouLess clients rarely support it.
