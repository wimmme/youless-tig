// Package influx writes and reads InfluxDB over HTTP with the standard library:
// line protocol to /api/v2/write and InfluxQL to /query. Both work on InfluxDB
// 2.x (and 3.x); /api/v2/write is also what Telegraf's influxdb_v2_listener
// accepts, so writes can go through Telegraf.
package influx

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Point is one line of line protocol.
type Point struct {
	Measurement string
	Tags        map[string]string
	Fields      map[string]any // float64, int64, int, bool or string
	Time        time.Time
}

var (
	measurementEscaper = strings.NewReplacer(`,`, `\,`, ` `, `\ `, "\n", `\n`)
	keyEscaper         = strings.NewReplacer(`,`, `\,`, `=`, `\=`, ` `, `\ `, "\n", `\n`)
	stringEscaper      = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
)

// Line encodes the point with second precision. A point without fields cannot
// be written and returns an error, as do NaN and infinite numbers.
func (p Point) Line() (string, error) {
	if p.Measurement == "" {
		return "", fmt.Errorf("point without measurement")
	}
	var b strings.Builder
	b.WriteString(measurementEscaper.Replace(p.Measurement))
	for _, k := range sortedKeys(p.Tags) {
		if p.Tags[k] == "" {
			continue // empty tag values are not allowed in line protocol
		}
		b.WriteByte(',')
		b.WriteString(keyEscaper.Replace(k))
		b.WriteByte('=')
		b.WriteString(keyEscaper.Replace(p.Tags[k]))
	}
	n := 0
	for _, k := range sortedKeys(p.Fields) {
		v, err := fieldValue(p.Fields[k])
		if err != nil {
			return "", fmt.Errorf("%s field %s: %w", p.Measurement, k, err)
		}
		if n == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteByte(',')
		}
		b.WriteString(keyEscaper.Replace(k))
		b.WriteByte('=')
		b.WriteString(v)
		n++
	}
	if n == 0 {
		return "", fmt.Errorf("%s: point without fields", p.Measurement)
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(p.Time.Unix(), 10))
	return b.String(), nil
}

func fieldValue(v any) (string, error) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("not a finite number")
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case int64:
		return strconv.FormatInt(x, 10) + "i", nil
	case int:
		return strconv.Itoa(x) + "i", nil
	case bool:
		return strconv.FormatBool(x), nil
	case string:
		return `"` + stringEscaper.Replace(x) + `"`, nil
	}
	return "", fmt.Errorf("unsupported type %T", v)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
