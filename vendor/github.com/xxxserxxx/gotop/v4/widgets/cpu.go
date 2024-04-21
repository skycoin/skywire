package widgets

import (
	"fmt"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/VividCortex/ewma"
	"github.com/xxxserxxx/gotop/v4/devices"

	"github.com/gizak/termui/v3"
	ui "github.com/xxxserxxx/gotop/v4/termui"
)

// TODO Maybe group CPUs in columns if space permits
type CPUWidget struct {
	*ui.LineGraph
	CPUCount        int
	ShowAverageLoad bool
	ShowPerCPULoad  bool
	updateInterval  time.Duration
	cpuLoads        map[string]float64
	average         ewma.MovingAverage
}

var cpuLabels []string

func NewCPUWidget(updateInterval time.Duration, horizontalScale int, showAverageLoad bool, showPerCPULoad bool) *CPUWidget {
	self := &CPUWidget{
		LineGraph:       ui.NewLineGraph(),
		CPUCount:        len(cpuLabels),
		updateInterval:  updateInterval,
		ShowAverageLoad: showAverageLoad,
		ShowPerCPULoad:  showPerCPULoad,
		cpuLoads:        make(map[string]float64),
		average:         ewma.NewMovingAverage(),
	}
	self.LabelStyles[AVRG] = termui.ModifierBold
	self.Title = tr.Value("widget.label.cpu")
	self.HorizontalScale = horizontalScale

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

	self.update()

	go func() {
		for range time.NewTicker(self.updateInterval).C {
			self.update()
		}
	}()

	return self
}

const AVRG = "AVRG"

func (cpu *CPUWidget) EnableMetric() {
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
