package termui

import (
	ui "github.com/gizak/termui/v3"
	gizak "github.com/gizak/termui/v3/widgets"
)

// LineGraph implements a line graph of data points.
type Gauge struct {
	*gizak.Gauge
}

func NewGauge() *Gauge {
	return &Gauge{
		Gauge: gizak.NewGauge(),
	}
}

func (g *Gauge) Draw(buf *ui.Buffer) {
	g.Gauge.Draw(buf)
	g.Gauge.SetRect(g.Min.X, g.Min.Y, g.Inner.Dx(), g.Inner.Dy())
}
