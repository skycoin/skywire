package widgets

import (
	"fmt"
	"time"

	"github.com/skycoin/skywire/third_party/VictoriaMetrics/metrics"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/devices"
	ui "github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/termui"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/utils"
)

// TempGraphWidget plots each temperature sensor as a line over time
// (multiload-ng style), used for the "temp" slot under the global multiload
// display flag. The default display uses the text-readout TempWidget instead.
//
// The graph value is the sensor's Celsius reading, which maps directly onto the
// LineGraph's 0-100 scale (sensors rarely exceed 100 C). When Fahrenheit is
// selected only the label is converted, so the graph stays correctly scaled.
type TempGraphWidget struct {
	*ui.LineGraph
	updateInterval time.Duration
	TempScale      TempScale
}

func NewTempGraphWidget(tempScale TempScale, filter []string, horizontalScale int) *TempGraphWidget {
	self := &TempGraphWidget{
		LineGraph:      ui.NewLineGraph(),
		updateInterval: time.Second * 5,
		TempScale:      tempScale,
	}
	self.Title = tr.Value("widget.label.temp")
	self.HorizontalScale = horizontalScale

	sensors := filter
	if len(sensors) == 0 {
		sensors = devices.Devices(devices.Temperatures, false)
	}
	for _, s := range sensors {
		self.Data[s] = []float64{0}
	}

	self.update()
	go func() {
		for range time.NewTicker(self.updateInterval).C {
			self.Lock()
			self.update()
			self.Unlock()
		}
	}()

	return self
}

func (t *TempGraphWidget) EnableMetric() {
	for k := range t.Data {
		kc := k
		metrics.NewGauge(makeName("temp", kc), func() float64 {
			if ds, ok := t.Data[kc]; ok && len(ds) > 0 {
				return ds[len(ds)-1]
			}
			return 0.0
		})
	}
}

func (t *TempGraphWidget) Scale(i int) {
	t.LineGraph.HorizontalScale = i
}

// update appends the latest Celsius reading per sensor, labeled in the chosen
// scale. Caller holds the lock (except the initial construction call).
func (t *TempGraphWidget) update() {
	temps := make(map[string]int)
	for k := range t.Data {
		temps[k] = 0
	}
	devices.UpdateTemps(temps) // fills Celsius values for known keys

	for name, c := range temps {
		t.Data[name] = append(t.Data[name], float64(c))
		disp := c
		if t.TempScale == Fahrenheit {
			disp = utils.CelsiusToFahrenheit(c)
		}
		t.Labels[name] = fmt.Sprintf("%d°%c", disp, t.TempScale)
	}
}
