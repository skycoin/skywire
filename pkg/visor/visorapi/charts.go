package visorapi

import "time"

// VisorChartsArgs asks for a visor's status page samples in [From, To), from
// the hourly means when Hourly is set.
type VisorChartsArgs struct {
	From, To time.Time
	Hourly   bool
}

// ChartSample is one sample of every series the visor charts.
type ChartSample struct {
	At time.Time
	V  map[string]float64
}

// ChartStart is one start of the visor and the build it ran.
type ChartStart struct {
	At              time.Time
	Version, Commit string
}

// VisorCharts is a visor's status page history for one window.
type VisorCharts struct {
	Samples []ChartSample
	Starts  []ChartStart
}
