# Changelog

All notable changes. Format based on [Keep a Changelog](https://keepachangelog.com/);
versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- `.env.example` now holds only the settings for youless-tig with your own
  InfluxDB; the complete stack has its own `.env.stack.example`. Both are split
  into what you must and what you can set.
- Default host ports that rarely clash: API 8030, Grafana 3030, InfluxDB 8031
  (were 8080, 3000 and 8086). Set `YOULESS_TIG_PORT`, `GRAFANA_PORT` or
  `INFLUXDB_PORT` in `.env` to keep the old ones.
- Clearer explanation of ports: the API always listens on 8080 inside the
  container, `YOULESS_TIG_PORT` only chooses the port on the Docker host
  (the example settings files, both compose files, docs/configuration.md).

## [0.1.0] - 2026-10-15

First release.

### Added
- Reads a YouLess LS120: live readings every 10 seconds (power, P1 import and
  export per tariff, voltage, current and power per phase, the Belgian capacity
  peak, gas, water, S0) and the device's minute, 10-minute, hour and day logs.
- Stores everything in InfluxDB 2, directly or through Telegraf; on the first
  start the device's whole history is read, later gaps are filled from its logs.
- YouLess-compatible API (`/d`, `/e`, `/f`, `/a`, `/V`, `/W`, `/K`, `/Z`) that
  answers like the device, with log pages reaching back as far as the stored data.
- `docker-compose.yml` for an existing InfluxDB, `docker-compose.stack.yml` with
  Telegraf, InfluxDB and Grafana, least-privilege tokens created on first start,
  and a provisioned Grafana dashboard.
- YouLess password support (login with cookie).
- A log page the YouLess does not answer is tried up to three times, so one lost
  request does not stop the first read of its history.
