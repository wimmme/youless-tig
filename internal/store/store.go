// Package store is the data model in InfluxDB: which measurements, tags and
// fields youless-tig writes, and the queries the YouLess-compatible API needs.
// docs/data-model.md describes the same model for dashboard builders; keep
// both in step.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wimmme/youless-tig/internal/influx"
	"github.com/wimmme/youless-tig/internal/youless"
)

// Measurement names.
const (
	Electricity = "youless_electricity" // /e and /f, at the meter's timestamp
	Gas         = "youless_gas"         // gas meter reading, at the gas meter's timestamp
	Water       = "youless_water"       // water meter reading, at the water meter's timestamp
	S0          = "youless_s0"          // S0 input, at the S0 timestamp
	Log         = "youless_log"         // the device's internal logs, one value per interval
)

// Store reads and writes one device's data.
type Store struct {
	DB     *influx.Client
	Device string // value of the "device" tag
}

func (s *Store) tags(extra ...string) map[string]string {
	t := map[string]string{"device": s.Device}
	for i := 0; i+1 < len(extra); i += 2 {
		t[extra[i]] = extra[i+1]
	}
	return t
}

func put(f map[string]any, key string, v *float64) {
	if v != nil {
		f[key] = *v
	}
}

// ElectricityPoint is the main meter reading from /e plus the phase data from
// /f (which may be nil). Voltages of 0 are left out: the meter does not send
// that phase.
func (s *Store) ElectricityPoint(e *youless.Energy, f *youless.Phases, loc *time.Location) influx.Point {
	fields := map[string]any{}
	put(fields, "power_w", e.Power)
	put(fields, "net_kwh", e.Net)
	put(fields, "import_t1_kwh", e.Import1)
	put(fields, "import_t2_kwh", e.Import2)
	put(fields, "export_t1_kwh", e.Export1)
	put(fields, "export_t2_kwh", e.Export2)
	if f != nil && e.Import1 != nil {
		if f.Tariff != nil && *f.Tariff > 0 {
			fields["tariff"] = int64(*f.Tariff)
		}
		for i, p := range []struct {
			v, a, w *float64
		}{{f.Voltage1, f.Current1, f.Power1}, {f.Voltage2, f.Current2, f.Power2}, {f.Voltage3, f.Current3, f.Power3}} {
			if p.v == nil || *p.v == 0 {
				continue
			}
			n := fmt.Sprint(i + 1)
			put(fields, "voltage_l"+n+"_v", p.v)
			put(fields, "current_l"+n+"_a", p.a)
			put(fields, "power_l"+n+"_w", p.w)
		}
		if f.CapacityPeak != nil && *f.CapacityPeak > 0 {
			put(fields, "capacity_avg_w", f.CapacityAvg)
			put(fields, "capacity_peak_w", f.CapacityPeak)
			if f.PeakTime != nil {
				if t, ok := youless.ParseLocalStamp(*f.PeakTime, loc); ok {
					fields["capacity_peak_time"] = t.Unix()
				}
			}
		}
	}
	return influx.Point{Measurement: Electricity, Tags: s.tags(), Fields: fields, Time: time.Unix(e.Time, 0)}
}

// MeterPoint is a gas or water reading (m³) at the meter's own timestamp.
// ok is false when the device has no such reading.
func (s *Store) MeterPoint(measurement string, total *float64, stamp *int64, loc *time.Location) (influx.Point, bool) {
	if total == nil || stamp == nil {
		return influx.Point{}, false
	}
	t, ok := youless.ParseLocalStamp(*stamp, loc)
	if !ok {
		return influx.Point{}, false
	}
	return influx.Point{Measurement: measurement, Tags: s.tags(), Fields: map[string]any{"total_m3": *total}, Time: t}, true
}

// S0Point is the S0 input reading; ok is false without an S0 timestamp.
func (s *Store) S0Point(e *youless.Energy) (influx.Point, bool) {
	if e.S0Time == nil || *e.S0Time <= 0 || e.S0Total == nil {
		return influx.Point{}, false
	}
	fields := map[string]any{"total_kwh": *e.S0Total}
	put(fields, "power_w", e.S0Power)
	return influx.Point{Measurement: S0, Tags: s.tags(), Fields: fields, Time: time.Unix(*e.S0Time, 0)}, true
}

// LogPoints are the values of a log page after `after` (zero: all); "*" values
// (no data) are skipped.
func (s *Store) LogPoints(ch youless.Channel, r youless.Resolution, p *youless.LogPage, after time.Time) []influx.Point {
	var pts []influx.Point
	for i, v := range p.Values {
		t := p.Time(i)
		if v == nil || !t.After(after) {
			continue
		}
		pts = append(pts, influx.Point{
			Measurement: Log,
			Tags:        s.tags("channel", string(ch), "resolution", string(r)),
			Fields:      map[string]any{"value": *v},
			Time:        t,
		})
	}
	return pts
}

// Write stores points.
func (s *Store) Write(ctx context.Context, pts []influx.Point) error {
	return s.DB.Write(ctx, pts)
}

func (s *Store) logWhere(ch youless.Channel, r youless.Resolution) string {
	return fmt.Sprintf(`"device"=%s AND "channel"=%s AND "resolution"=%s`,
		influx.QuoteString(s.Device), influx.QuoteString(string(ch)), influx.QuoteString(string(r)))
}

// LastLogTime is the time of the newest stored value of a log, or
// influx.ErrNoData.
func (s *Store) LastLogTime(ctx context.Context, ch youless.Channel, r youless.Resolution) (time.Time, error) {
	series, err := s.DB.Query(ctx, fmt.Sprintf(`SELECT last("value") FROM %s WHERE %s`, influx.QuoteIdent(Log), s.logWhere(ch, r)))
	if err != nil {
		return time.Time{}, err
	}
	for _, sr := range series {
		for _, row := range sr.Values {
			if len(row) > 0 {
				return rowTime(row[0])
			}
		}
	}
	return time.Time{}, influx.ErrNoData
}

// LogValues are the stored values of a log in [from, to), keyed by Unix time.
func (s *Store) LogValues(ctx context.Context, ch youless.Channel, r youless.Resolution, from, to time.Time) (map[int64]float64, error) {
	q := fmt.Sprintf(`SELECT "value" FROM %s WHERE %s AND time >= %s AND time < %s`,
		influx.QuoteIdent(Log), s.logWhere(ch, r), influx.Time(from), influx.Time(to))
	series, err := s.DB.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[int64]float64{}
	for _, sr := range series {
		for _, row := range sr.Values {
			if len(row) < 2 || row[1] == nil {
				continue
			}
			t, err := rowTime(row[0])
			if err != nil {
				return nil, err
			}
			n, ok := row[1].(json.Number)
			if !ok {
				return nil, fmt.Errorf("influx: value %v is not a number", row[1])
			}
			v, err := n.Float64()
			if err != nil {
				return nil, err
			}
			out[t.Unix()] = v
		}
	}
	return out, nil
}

func rowTime(v any) (time.Time, error) {
	n, ok := v.(json.Number)
	if !ok {
		return time.Time{}, fmt.Errorf("influx: time %v is not a number", v)
	}
	sec, err := n.Int64()
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}
