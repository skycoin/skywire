package widgets

import (
	"fmt"
	"time"

	"github.com/gizak/termui/v3"

	"github.com/skycoin/skywire/third_party/VictoriaMetrics/metrics"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/devices"
	ui "github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/termui"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/utils"
)

type MemWidget struct {
	*ui.LineGraph
	// ShowBreakdown switches the memory graph to a multiload-ng-style breakdown
	// (used/buff/cache + a bold total) instead of a single used-% line. Driven
	// by the global multiload display flag.
	ShowBreakdown  bool
	updateInterval time.Duration
}

func NewMemWidget(updateInterval time.Duration, horizontalScale int, showBreakdown bool) *MemWidget {
	widg := &MemWidget{
		LineGraph:      ui.NewLineGraph(),
		ShowBreakdown:  showBreakdown,
		updateInterval: updateInterval,
	}
	widg.Title = tr.Value("widget.label.mem")
	widg.HorizontalScale = horizontalScale

	if widg.ShowBreakdown {
		widg.LabelStyles[devices.MemTotal] = termui.ModifierBold
		for _, k := range devices.MemBreakdownLabels {
			widg.Data[k] = []float64{0}
		}
		widg.updateBreakdown()
		go func() {
			for range time.NewTicker(widg.updateInterval).C {
				widg.Lock()
				widg.updateBreakdown()
				widg.Unlock()
			}
		}()
		return widg
	}

	mems := make(map[string]devices.MemoryInfo)
	devices.UpdateMem(mems)
	for name, mem := range mems {
		if mem.Total > 0 {
			widg.Data[name] = []float64{0}
			widg.renderMemInfo(name, mem)
		}
	}

	go func() {
		for range time.NewTicker(widg.updateInterval).C {
			widg.Lock()
			devices.UpdateMem(mems)
			for label, mi := range mems {
				if mi.Total > 0 {
					widg.renderMemInfo(label, mi)
				}
			}
			widg.Unlock()
		}
	}()

	return widg
}

// MultiloadMemBase is the base hue for the multiload-ng-style memory breakdown:
// a saturated green (256-color cube (r,g,b)=(0,5,0)), matching multiload's RAM
// convention regardless of the active colorscheme. used is the vivid base green
// and the lighter components fade toward white.
const MultiloadMemBase = termui.Color(46)

// AssignShades colors the breakdown components as tints of a single base hue
// (multiload-ng style): used (vivid base) -> total (lightest, near white), with
// the total label bolded.
func (mem *MemWidget) AssignShades(base termui.Color) {
	order := devices.MemBreakdownLabels
	for i, name := range order {
		t := 0.0
		if len(order) > 1 {
			t = float64(i) / float64(len(order)-1)
		}
		// spread from 0 (pure base hue for `used`) to 0.85 (near white for the
		// lightest component) so the subtypes stay clearly distinct.
		mem.LineColors[name] = ui.Tint(base, t*0.85)
	}
	mem.LabelStyles[devices.MemTotal] = termui.ModifierBold
}

func (mem *MemWidget) EnableMetric() {
	if mem.ShowBreakdown {
		for _, k := range devices.MemBreakdownLabels {
			kc := k
			metrics.NewGauge(makeName("memory", " "+kc), func() float64 {
				if ds, ok := mem.Data[kc]; ok && len(ds) > 0 {
					return ds[len(ds)-1]
				}
				return 0.0
			})
		}
		return
	}
	mems := make(map[string]devices.MemoryInfo)
	devices.UpdateMem(mems)
	for l := range mems {
		lc := l
		metrics.NewGauge(makeName("memory", l), func() float64 {
			if ds, ok := mem.Data[lc]; ok {
				return ds[len(ds)-1]
			}
			return 0.0
		})
	}
}

func (mem *MemWidget) Scale(i int) {
	mem.LineGraph.HorizontalScale = i
}

// updateBreakdown appends the latest per-component percentages, labeling each
// with its percent and absolute size. Caller holds the lock.
func (mem *MemWidget) updateBreakdown() {
	pcts := make(map[string]int)
	devices.UpdateMemBreakdown(pcts)
	total := mem.totalBytes()
	for _, k := range devices.MemBreakdownLabels {
		p := pcts[k]
		mem.Data[k] = append(mem.Data[k], float64(p))
		bytes, mag := utils.ConvertBytes(uint64(float64(p) / 100.0 * float64(total)))
		mem.Labels[k] = fmt.Sprintf("%3d%% %5.1f%s", p, bytes, mag)
	}
}

// totalBytes returns total physical RAM for sizing the breakdown labels.
func (mem *MemWidget) totalBytes() uint64 {
	mems := make(map[string]devices.MemoryInfo)
	devices.UpdateMem(mems)
	if mi, ok := mems["Main"]; ok {
		return mi.Total
	}
	return 0
}

func (mem *MemWidget) renderMemInfo(line string, memoryInfo devices.MemoryInfo) {
	mem.Data[line] = append(mem.Data[line], memoryInfo.UsedPercent)
	memoryTotalBytes, memoryTotalMagnitude := utils.ConvertBytes(memoryInfo.Total)
	memoryUsedBytes, memoryUsedMagnitude := utils.ConvertBytes(memoryInfo.Used)
	mem.Labels[line] = fmt.Sprintf("%3.0f%% %5.1f%s/%.0f%s",
		memoryInfo.UsedPercent,
		memoryUsedBytes,
		memoryUsedMagnitude,
		memoryTotalBytes,
		memoryTotalMagnitude,
	)
}
