package widgets

import (
	"fmt"
	"strings"
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
	// realName maps each compact legend key (e.g. "core 0") back to the verbose
	// sysfs sensor name (e.g. "coretemp_core_0") that devices.UpdateTemps fills.
	realName map[string]string
}

func NewTempGraphWidget(updateInterval time.Duration, tempScale TempScale, filter []string, horizontalScale int) *TempGraphWidget {
	self := &TempGraphWidget{
		LineGraph:      ui.NewLineGraph(),
		updateInterval: updateInterval,
		TempScale:      tempScale,
	}
	self.Title = tr.Value("widget.label.temp")
	self.HorizontalScale = horizontalScale

	sensors := filter
	if len(sensors) == 0 {
		sensors = devices.Devices(devices.Temperatures, false)
	}
	self.realName = make(map[string]string, len(sensors))
	for _, s := range sensors {
		short := uniqueTempName(shortenTempName(s), self.realName)
		self.realName[short] = s
		self.Data[short] = []float64{0}
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
// scale. Sensors are fetched by their real sysfs name and stored under the
// compact legend key. Caller holds the lock (except the initial construction
// call).
func (t *TempGraphWidget) update() {
	temps := make(map[string]int, len(t.realName))
	for _, real := range t.realName {
		temps[real] = 0
	}
	devices.UpdateTemps(temps) // fills Celsius values for known sensor names

	for short, real := range t.realName {
		c := temps[real]
		t.Data[short] = append(t.Data[short], float64(c))
		disp := c
		if t.TempScale == Fahrenheit {
			disp = utils.CelsiusToFahrenheit(c)
		}
		t.Labels[short] = fmt.Sprintf("%d°%c", disp, t.TempScale)
	}
}

// shortenTempName trims verbose sysfs sensor names down to compact legend
// labels: coretemp_core_0 -> "core 0", coretemp_package_id_0 -> "pkg 0". The
// real name is kept (realName map) for the sensor fetch.
func shortenTempName(name string) string {
	s := strings.TrimPrefix(name, "coretemp_")
	s = strings.ReplaceAll(s, "package_id_", "pkg ")
	s = strings.ReplaceAll(s, "core_", "core ")
	s = strings.ReplaceAll(s, "_", " ")
	return s
}

// uniqueTempName disambiguates compact names that collide (two sensors
// shortening to the same label) by appending an index, keeping realName 1:1.
func uniqueTempName(short string, used map[string]string) string {
	if _, ok := used[short]; !ok {
		return short
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s %d", short, i)
		if _, ok := used[cand]; !ok {
			return cand
		}
	}
}
