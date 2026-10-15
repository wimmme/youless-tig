# Security

## Reporting a vulnerability

Please report security problems privately through GitHub's
[*Report a vulnerability*](https://github.com/wimmme/youless-tig/security/advisories/new)
form, not in a public issue. You get an answer within a week.

## What youless-tig does to stay safe

- **Reads only.** It sends the YouLess only GET requests for its read endpoints
  (and the login when a password is set). The compatible API forwards nothing to
  the device: it cannot be used to change settings.
- **Least privilege.** In the stack every service gets its own InfluxDB token
  with only the rights it needs; no service holds the admin token. Use a
  bucket-scoped token with your own InfluxDB too ([docs/influxdb.md](docs/influxdb.md)).
- **Secrets** come from `.env` (keep it `chmod 600` and out of git) or from files
  (`*_FILE`, e.g. Docker secrets). They are never logged; a failing YouLess login
  does not show the password.
- **The container** runs as a non-root user on a read-only file system, without
  Linux capabilities and with `no-new-privileges`.
- **No telemetry.** youless-tig contacts nothing but your YouLess and your
  InfluxDB (or Telegraf). The stack turns off Grafana's and InfluxDB's usage
  reporting and update checks.

## What you should know

- The YouLess speaks plain HTTP and its password travels in the URL of the login
  request. Keep the YouLess and the Docker host on a trusted network.
- The compatible API has no authentication, like the device. It shows your
  energy use to anyone who can reach it: publish it on your LAN only
  (`YOULESS_TIG_BIND`), never on the internet.
- Grafana is published on your LAN by default. Use a strong admin password, and
  a reverse proxy with HTTPS if you reach it from elsewhere.
