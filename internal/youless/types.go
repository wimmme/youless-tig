// Package youless reads a YouLess energy monitor (LS120, firmware 1.6 or newer)
// over its local HTTP/JSON interface. It only reads: no settings, no time sync,
// nothing that changes the device.
package youless

import (
	"fmt"
	"time"
)

// Device is the /d response: fixed identification.
type Device struct {
	Model    string `json:"model"`
	Firmware string `json:"fw"`
	MAC      string `json:"mac"`
}

// Energy is the first element of the /e response: the primary meter readings.
// Pointer fields are absent when the device has no such input (no P1 connection,
// no gas or water meter on the smart meter).
type Energy struct {
	Time    int64    `json:"tm"`  // Unix time (UTC) of the main meter reading
	Net     *float64 `json:"net"` // net total main meter, kWh
	Power   *float64 `json:"pwr"` // net power main meter, W
	S0Time  *int64   `json:"ts0"` // Unix time (UTC) of the S0 reading
	S0Total *float64 `json:"cs0"` // S0 counter, kWh
	S0Power *float64 `json:"ps0"` // S0 power, W
	Import1 *float64 `json:"p1"`  // P1 import tariff 1, kWh
	Import2 *float64 `json:"p2"`  // P1 import tariff 2, kWh
	Export1 *float64 `json:"n1"`  // P1 export tariff 1, kWh
	Export2 *float64 `json:"n2"`  // P1 export tariff 2, kWh
	Gas     *float64 `json:"gas"` // gas meter, m³
	GasTime *int64   `json:"gts"` // local time of the gas reading, yymmddhhmm
	Water   *float64 `json:"wtr"` // water meter, m³
	WaterTm *int64   `json:"wts"` // local time of the water reading, yymmddhhmm
}

// Phases is the /f response: secondary P1 data points. Values are zero when the
// smart meter does not send them; the capacity tariff (pa, pp, pts) is Belgian only.
type Phases struct {
	Tariff       *int     `json:"tr"`  // current tariff, 1 or 2
	CapacityAvg  *float64 `json:"pa"`  // average power of the running capacity-tariff quarter, W
	CapacityPeak *float64 `json:"pp"`  // peak capacity-tariff power this month, W
	PeakTime     *int64   `json:"pts"` // local time of that peak, yymmddhhmm
	Current1     *float64 `json:"i1"`  // A
	Current2     *float64 `json:"i2"`
	Current3     *float64 `json:"i3"`
	Voltage1     *float64 `json:"v1"` // V
	Voltage2     *float64 `json:"v2"`
	Voltage3     *float64 `json:"v3"`
	Power1       *float64 `json:"l1"` // W
	Power2       *float64 `json:"l2"`
	Power3       *float64 `json:"l3"`
}

// Channel is one of the internal log channels.
type Channel string

const (
	Electricity Channel = "electricity" // /V, net electricity
	Gas         Channel = "gas"         // /W
	Water       Channel = "water"       // /K
	S0          Channel = "s0"          // /Z, S0 pulse input
)

// Channels lists every channel in a fixed order.
var Channels = []Channel{Electricity, Gas, Water, S0}

// Path is the device path of the channel's log, e.g. "/V".
func (c Channel) Path() string {
	switch c {
	case Electricity:
		return "/V"
	case Gas:
		return "/W"
	case Water:
		return "/K"
	case S0:
		return "/Z"
	}
	return ""
}

// ChannelForPath is the inverse of Path ("/V" → Electricity).
func ChannelForPath(p string) (Channel, bool) {
	for _, c := range Channels {
		if c.Path() == p {
			return c, true
		}
	}
	return "", false
}

// ParseChannel accepts a channel name as used in the configuration.
func ParseChannel(s string) (Channel, error) {
	for _, c := range Channels {
		if string(c) == s {
			return c, nil
		}
	}
	return "", fmt.Errorf("unknown channel %q (want electricity, gas, water or s0)", s)
}

// Resolution is one of the logs of a channel, each with its own interval.
type Resolution string

const (
	Minute     Resolution = "minute" // ?h=N, 30 values per page
	TenMinutes Resolution = "10min"  // ?w=N, 48 values per page
	Hour       Resolution = "hour"   // ?d=N, one calendar day per page
	Day        Resolution = "day"    // ?m=N, one calendar month per page (N = month number)
)

// Resolutions lists every resolution from fine to coarse.
var Resolutions = []Resolution{Minute, TenMinutes, Hour, Day}

// Param is the query parameter that selects this log ("h", "w", "d", "m").
func (r Resolution) Param() string {
	switch r {
	case Minute:
		return "h"
	case TenMinutes:
		return "w"
	case Hour:
		return "d"
	case Day:
		return "m"
	}
	return ""
}

// ResolutionForParam is the inverse of Param.
func ResolutionForParam(p string) (Resolution, bool) {
	for _, r := range Resolutions {
		if r.Param() == p {
			return r, true
		}
	}
	return "", false
}

// Interval is the time between two values of this log.
func (r Resolution) Interval() time.Duration {
	switch r {
	case Minute:
		return time.Minute
	case TenMinutes:
		return 10 * time.Minute
	case Hour:
		return time.Hour
	case Day:
		return 24 * time.Hour
	}
	return 0
}

// DevicePages is how many pages an LS120 keeps of this log: 20×30 minutes
// (10 hours), 30×48 ten-minute values (10 days), 70 days of hours, 12 months.
func (r Resolution) DevicePages() int {
	switch r {
	case Minute:
		return 20
	case TenMinutes:
		return 30
	case Hour:
		return 70
	case Day:
		return 12
	}
	return 0
}

// Unit is the unit the device reports for a channel's log: an average flow for
// the minute, 10-minute and hour logs and a consumption for the day log.
func Unit(c Channel, r Resolution) string {
	water := c == Gas || c == Water
	switch {
	case r == Day && water:
		return "m3"
	case r == Day:
		return "kWh"
	case water:
		return "L"
	default:
		return "Watt"
	}
}
