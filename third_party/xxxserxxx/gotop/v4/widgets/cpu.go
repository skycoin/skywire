package widgets

import (
	"fmt"
	"time"

	"github.com/VividCortex/ewma"
	"github.com/gizak/termui/v3"

	"github.com/skycoin/skywire/third_party/VictoriaMetrics/metrics"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/devices"
	ui "github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/termui"
)

// TODO Maybe group CPUs in columns if space permits
type CPUWidget struct {
	*ui.LineGraph
	CPUCount        int
	ShowAverageLoad bool
	ShowPerCPULoad  bool
	// ShowBreakdown switches the CPU graph to a multiload-ng-style per-state
	// breakdown (user/sys/iowait/irq/steal + a bold total) instead of the
	// single busy figure. Driven by the global multiload display flag; when set
	// it overrides the average/per-cpu modes.
	ShowBreakdown  bool
	updateInterval time.Duration
	cpuLoads       map[string]float64
	average        ewma.MovingAverage
}

var cpuLabels []string

func NewCPUWidget(updateInterval time.Duration, horizontalScale int, showAverageLoad bool, showPerCPULoad bool, showBreakdown bool) *CPUWidget {
	self := &CPUWidget{
		LineGraph:       ui.NewLineGraph(),
		CPUCount:        len(cpuLabels),
		updateInterval:  updateInterval,
		ShowAverageLoad: showAverageLoad,
		ShowPerCPULoad:  showPerCPULoad,
		ShowBreakdown:   showBreakdown,
		cpuLoads:        make(map[string]float64),
		average:         ewma.NewMovingAverage(),
	}
	self.LabelStyles[AVRG] = termui.ModifierBold
	self.Title = tr.Value("widget.label.cpu")
	self.HorizontalScale = horizontalScale

	if self.ShowBreakdown {
		// Per-state breakdown: pre-seed the state keys so the colorscheme
		// assigns each one a distinct color at construction, and bold the total.
		self.LabelStyles[devices.CPUStateTotal] = termui.ModifierBold
		for _, k := range devices.CPUBreakdownLabels {
			self.Data[k] = []float64{0}
		}
		self.startUpdating()
		return self
	}

	if !(self.ShowAverageLoad || self.ShowPerCPULoad) {
		if self.CPUCount <= 8 {
			self.ShowPerCPULoad = true
		} else {
			self.ShowAverageLoad = true
		}
	}

	if self.ShowAverageLoad {
		self.Data[AVRG] = []float64{0}
	}

	if self.ShowPerCPULoad {
		cpus := make(map[string]int)
		devices.UpdateCPU(cpus, self.updateInterval, self.ShowPerCPULoad)
		for k, v := range cpus {
			self.Data[k] = []float64{float64(v)}
		}
	}

	self.startUpdating()
	return self
}

func (cpu *CPUWidget) startUpdating() {
	cpu.update()
	go func() {
		for range time.NewTicker(cpu.updateInterval).C {
			cpu.update()
		}
	}()
}

const AVRG = "AVRG"

func (cpu *CPUWidget) EnableMetric() {
	if cpu.ShowBreakdown {
		for _, state := range devices.CPUBreakdownLabels {
			sc := state
			metrics.NewGauge(makeName("cpu", " "+sc), func() float64 {
				return cpu.cpuLoads[sc]
			})
		}
		return
	}
	if cpu.ShowAverageLoad {
		metrics.NewGauge(makeName("cpu", " avg"), func() float64 {
			return cpu.cpuLoads[AVRG]
		})
	} else {
		cpus := make(map[string]int)
		devices.UpdateCPU(cpus, cpu.updateInterval, cpu.ShowPerCPULoad)
		for key, perc := range cpus {
			kc := key
			cpu.cpuLoads[key] = float64(perc)
			metrics.NewGauge(makeName("cpu", key), func() float64 {
				return cpu.cpuLoads[kc]
			})
		}
	}
}

func (cpu *CPUWidget) Scale(i int) {
	cpu.LineGraph.HorizontalScale = i
}

func (cpu *CPUWidget) update() {
	go func() {
		if cpu.ShowBreakdown {
			states := make(map[string]int)
			devices.UpdateCPUBreakdown(states)
			cpu.Lock()
			defer cpu.Unlock()
			for _, state := range devices.CPUBreakdownLabels {
				percent := states[state]
				cpu.Data[state] = append(cpu.Data[state], float64(percent))
				cpu.Labels[state] = fmt.Sprintf("%3d%%", percent)
				cpu.cpuLoads[state] = float64(percent)
			}
			return
		}

		cpus := make(map[string]int)
		devices.UpdateCPU(cpus, cpu.updateInterval, true)
		cpu.Lock()
		defer cpu.Unlock()
		// AVG = ((AVG*i)+n)/(i+1)
		var sum int
		for key, percent := range cpus {
			sum += percent
			if cpu.ShowPerCPULoad {
				cpu.Data[key] = append(cpu.Data[key], float64(percent))
				cpu.Labels[key] = fmt.Sprintf("%3d%%", percent)
				cpu.cpuLoads[key] = float64(percent)
			}
		}
		if cpu.ShowAverageLoad {
			cpu.average.Add(float64(sum) / float64(len(cpus)))
			avg := cpu.average.Value()
			cpu.Data[AVRG] = append(cpu.Data[AVRG], avg)
			cpu.Labels[AVRG] = fmt.Sprintf("%3.0f%%", avg)
			cpu.cpuLoads[AVRG] = avg
		}
	}()
}
