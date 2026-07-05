//go:build linux || darwin
// +build linux darwin

package devices

import (
	"github.com/shirou/gopsutil/v3/host"
)

func init() {
	devs() // Populate the sensorMap
	RegisterStartup(startBlock)
	RegisterTemp(getTemps)
	RegisterDeviceList(Temperatures, devs, defs)
	RegisterShutdown(endBlock)
}

func getTemps(temps map[string]int) map[string]error {
	sensors, err := host.SensorsTemperatures()
	if err != nil {
		if _, ok := err.(*host.Warnings); ok {
			// ignore warnings
		} else {
			return map[string]error{"gopsutil host": err}
		}
	}
	for _, sensor := range sensors {
		label := sensorMap[sensor.SensorKey]
		if _, ok := temps[label]; ok {
			temps[label] = int(sensor.Temperature)
		}
	}

	readDiskTemps(temps)
	return nil
}

// Optimization to avoid string manipulation every update
var sensorMap map[string]string
