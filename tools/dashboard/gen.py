#!/usr/bin/env python3
"""Generates grafana/dashboards/youless.json, the YouLess dashboard.

    python3 tools/dashboard/gen.py grafana/dashboards/youless.json

Edit this file rather than the JSON, then regenerate and commit both. The
queries use InfluxQL against the data model in docs/data-model.md; the Go test
grafana_test.go and the integration test run every query.
"""
import json
import sys

DS = {"type": "influxdb", "uid": "${datasource}"}
DEV = '"device" =~ /^$device$/'
E, G, W, L = '"youless_electricity"', '"youless_gas"', '"youless_water"', '"youless_log"'

_next_id = [0]


def nid():
    _next_id[0] += 1
    return _next_id[0]


def target(q, ref="A", alias=None):
    t = {"datasource": DS, "refId": ref, "query": q, "rawQuery": True, "resultFormat": "time_series"}
    if alias:
        t["alias"] = alias
    return t


def stat(title, q, unit, x, y, decimals=None, time_from=None, desc=None, color="blue"):
    p = {
        "id": nid(), "type": "stat", "title": title, "datasource": DS,
        "gridPos": {"x": x, "y": y, "w": 4, "h": 4},
        "targets": [target(q)],
        "fieldConfig": {"defaults": {"unit": unit, "color": {"mode": "fixed", "fixedColor": color}}, "overrides": []},
        "options": {
            "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "colorMode": "value", "graphMode": "none", "textMode": "value",
            "justifyMode": "auto", "orientation": "auto",
        },
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    if time_from:
        p["timeFrom"] = time_from
    if desc:
        p["description"] = desc
    return p


def series(title, targets, unit, x, y, w=24, h=8, bars=False, time_from=None, desc=None, decimals=None):
    custom = {
        "drawStyle": "bars" if bars else "line", "lineWidth": 1, "fillOpacity": 80 if bars else 10,
        "showPoints": "never", "spanNulls": False, "barAlignment": -1 if bars else 0,
    }
    p = {
        "id": nid(), "type": "timeseries", "title": title, "datasource": DS,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": [target(q, chr(65 + i), a) for i, (q, a) in enumerate(targets)],
        "fieldConfig": {"defaults": {"unit": unit, "custom": custom}, "overrides": []},
        "options": {
            "legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
            "tooltip": {"mode": "multi", "sort": "none"},
        },
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    if time_from:
        p["timeFrom"] = time_from
    if desc:
        p["description"] = desc
    return p


def row(title, y):
    return {"id": nid(), "type": "row", "title": title, "collapsed": False,
            "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}


def log(ch, res):
    return f"{L} WHERE {DEV} AND \"channel\" = '{ch}' AND \"resolution\" = '{res}' AND $timeFilter"


def per_interval(field):
    return f'SELECT mean("{field}") FROM {E} WHERE {DEV} AND $timeFilter GROUP BY time($__interval) fill(null)'


def daily(expr):
    return f"SELECT {expr} FROM {E} WHERE {DEV} AND $timeFilter GROUP BY time(1d) fill(none) tz('$timezone')"


def build():
    panels, y = [], 0
    panels.append(row("Now", y))
    y += 1
    panels += [
        stat("Power now", f'SELECT last("power_w") FROM {E} WHERE {DEV} AND time > now() - 5m', "watt", 0, y,
             color="orange", desc="Net power: positive is taken from the grid, negative is sent back."),
        stat("Import today", f'SELECT spread("import_t1_kwh") + spread("import_t2_kwh") FROM {E} WHERE {DEV} AND $timeFilter',
             "kwatth", 4, y, decimals=2, time_from="now/d", color="red"),
        stat("Export today", f'SELECT spread("export_t1_kwh") + spread("export_t2_kwh") FROM {E} WHERE {DEV} AND $timeFilter',
             "kwatth", 8, y, decimals=2, time_from="now/d", color="green"),
        stat("Gas today", f'SELECT spread("total_m3") FROM {G} WHERE {DEV} AND $timeFilter',
             "m3", 12, y, decimals=3, time_from="now/d", color="yellow"),
        stat("Water today", f'SELECT spread("total_m3") * 1000 FROM {W} WHERE {DEV} AND $timeFilter',
             "litre", 16, y, decimals=0, time_from="now/d", color="blue"),
        stat("Capacity peak this month", f'SELECT last("capacity_peak_w") / 1000 FROM {E} WHERE {DEV} AND time > now() - 1h',
             "kwatt", 20, y, decimals=2, color="purple",
             desc="Highest quarter-hour average power this month (Belgian capacity tariff). Empty elsewhere."),
    ]
    y += 4
    panels.append(row("Power", y))
    y += 1
    panels.append(series("Net power", [(per_interval("power_w"), "net")], "watt", 0, y, w=16))
    panels.append(series("Power per phase", [(per_interval(f"power_l{i}_w"), f"L{i}") for i in (1, 2, 3)], "watt", 16, y, w=8))
    y += 8
    panels.append(series("Voltage", [(per_interval(f"voltage_l{i}_v"), f"L{i}") for i in (1, 2, 3)], "volt", 0, y, w=12, h=7, decimals=1))
    panels.append(series("Current", [(per_interval(f"current_l{i}_a"), f"L{i}") for i in (1, 2, 3)], "amp", 12, y, w=12, h=7, decimals=2))
    y += 7
    panels.append(row("History (from the YouLess logs)", y))
    y += 1
    panels.append(series("Electricity per day (net)", [(f'SELECT "value" FROM {log("electricity", "day")}', "net kWh")],
                         "kwatth", 0, y, w=12, bars=True, time_from="90d", decimals=2,
                         desc="The YouLess day log: net consumption per day, negative when more was sent back than taken."))
    panels.append(series("Import and export per day", [
        (daily('spread("import_t1_kwh") + spread("import_t2_kwh")'), "import"),
        (daily('-1 * (spread("export_t1_kwh") + spread("export_t2_kwh"))'), "export"),
    ], "kwatth", 12, y, w=12, bars=True, time_from="30d", decimals=2,
        desc="From the P1 meter readings youless-tig stores, so it starts on the day youless-tig was first started."))
    y += 8
    panels.append(series("Electricity per hour", [(f'SELECT "value" / 1000 FROM {log("electricity", "hour")}', "net kWh")],
                         "kwatth", 0, y, bars=True, time_from="7d", decimals=2))
    y += 8
    panels.append(series("Gas per day", [(f'SELECT "value" FROM {log("gas", "day")}', "m³")],
                         "m3", 0, y, w=12, bars=True, time_from="90d", decimals=3))
    panels.append(series("Water per day", [(f'SELECT "value" * 1000 FROM {log("water", "day")}', "litre")],
                         "litre", 12, y, w=12, bars=True, time_from="90d", decimals=0))
    y += 8
    panels.append(series("Net power per minute (long-term)", [(f'SELECT "value" FROM {log("electricity", "minute")}', "W")],
                         "watt", 0, y, time_from="2d",
                         desc="The YouLess minute log, kept for as long as InfluxDB keeps data."))

    device_query = 'SHOW TAG VALUES FROM "youless_electricity" WITH KEY = "device"'
    return {
        "uid": "youless-tig", "title": "YouLess", "tags": ["youless", "energy"], "timezone": "browser",
        "editable": True, "graphTooltip": 1, "refresh": "30s", "schemaVersion": 39, "version": 1,
        "time": {"from": "now-24h", "to": "now"},
        "templating": {"list": [
            {"name": "datasource", "label": "Data source", "type": "datasource", "query": "influxdb",
             "current": {}, "hide": 0, "refresh": 1, "regex": "", "includeAll": False, "multi": False, "options": []},
            {"name": "device", "label": "YouLess", "type": "query", "datasource": DS, "query": device_query,
             "definition": device_query, "refresh": 1, "includeAll": False, "multi": False,
             "current": {}, "options": [], "sort": 1, "hide": 0},
            {"name": "timezone", "label": "Time zone (daily totals)", "type": "textbox", "query": "Europe/Brussels",
             "current": {"text": "Europe/Brussels", "value": "Europe/Brussels"}, "hide": 0,
             "options": [{"selected": True, "text": "Europe/Brussels", "value": "Europe/Brussels"}]},
        ]},
        "annotations": {"list": []},
        "panels": panels,
    }


if __name__ == "__main__":
    with open(sys.argv[1], "w", newline="\n", encoding="utf-8") as f:
        json.dump(build(), f, indent=2, ensure_ascii=False)
        f.write("\n")
