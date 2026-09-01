//go:build linux
// +build linux

package devices

import (
	"log"
	"strings"

	"github.com/shirou/gopsutil/v3/host"
)

// All possible thermometers
func devs() []string {
	if sensorMap == nil {
		sensorMap = make(map[string]string)
	}
	sensors, err := host.SensorsTemperatures()
	if err != nil {
		// gopsutil returns *host.Warnings for sensors it couldn't fully read;
		// that is not fatal, so only complain about real errors.
		if _, ok := err.(*host.Warnings); !ok {
			log.Printf("gopsutil reports %s", err)
		}
		if len(sensors) == 0 {
			log.Printf("no temperature sensors returned")
			return []string{}
		}
	}
	rv := make([]string, 0, len(sensors))
	seen := make(map[string]bool)
	for _, sensor := range sensors {
		// gopsutil v3 already strips the _input/_thermal suffixes from
		// SensorKey; trim them anyway for older versions, then use the key as
		// the label. (The previous code only kept keys that still had a suffix,
		// which under gopsutil v3 is none of them — so the widget showed nothing.)
		label := strings.TrimSuffix(sensor.SensorKey, "_input")
		label = strings.TrimSuffix(label, "_thermal")
		if label == "" || sensor.Temperature <= 0 { // skip empty/dead sensors
			continue
		}
		sensorMap[sensor.SensorKey] = label
		if !seen[label] {
			seen[label] = true
			rv = append(rv, label)
		}
	}
	return rv
}

// defs returns every detected sensor label (deduplicated). gopsutil v3 reports
// one entry per live sensor, so there is no _input/_max/_crit duplication to
// filter out.
func defs() []string {
	// MUST be called AFTER init()
	rv := make([]string, 0, len(sensorMap))
	seen := make(map[string]bool)
	for _, v := range sensorMap {
		if !seen[v] {
			seen[v] = true
			rv = append(rv, v)
		}
	}
	return rv
}
