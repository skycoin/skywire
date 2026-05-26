package termui

import (
	"image"
	"log"

	ui "github.com/gizak/termui/v3"
)

// Sparkline is like: ▅▆▂▂▅▇▂▂▃▆▆▆▅▃. The data points should be non-negative integers.
type Sparkline struct {
	Data       []int
	Title1     string
	Title2     string
	TitleColor ui.Color
	LineColor  ui.Color
}

// SparklineGroup is a renderable widget which groups together the given sparklines.
type SparklineGroup struct {
	*ui.Block
	Lines []*Sparkline
}

// Add appends a given Sparkline to the *SparklineGroup.
func (s *SparklineGroup) Add(sl Sparkline) {
	s.Lines = append(s.Lines, &sl)
}

// NewSparkline returns an unrenderable single sparkline that intended to be added into a SparklineGroup.
func NewSparkline() *Sparkline {
	return &Sparkline{}
}

// NewSparklineGroup return a new *SparklineGroup with given Sparklines, you can always add a new Sparkline later.
func NewSparklineGroup(ss ...*Sparkline) *SparklineGroup {
	return &SparklineGroup{
		Block: ui.NewBlock(),
		Lines: ss,
	}
}

func (s *SparklineGroup) Draw(buf *ui.Buffer) {
	s.Block.Draw(buf)

	lc := len(s.Lines) // lineCount

	// renders each sparkline and its titles
	for i, line := range s.Lines {

		// prints titles
		title1Y := s.Inner.Min.Y + 1 + (s.Inner.Dy()/lc)*i
		title2Y := s.Inner.Min.Y + 2 + (s.Inner.Dy()/lc)*i
		title1 := ui.TrimString(line.Title1, s.Inner.Dx())
		title2 := ui.TrimString(line.Title2, s.Inner.Dx())
		if s.Inner.Dy() > 5 {
			buf.SetString(
				title1,
				ui.NewStyle(line.TitleColor, ui.ColorClear, ui.ModifierBold),
				image.Pt(s.Inner.Min.X, title1Y),
			)
		}
		if s.Inner.Dy() > 6 {
			buf.SetString(
				title2,
				ui.NewStyle(line.TitleColor, ui.ColorClear, ui.ModifierBold),
				image.Pt(s.Inner.Min.X, title2Y),
			)
		}

		sparkY := (s.Inner.Dy() / lc) * (i + 1)
		// finds max data in current view used for relative heights
		max := 1
		for i := len(line.Data) - 1; i >= 0 && s.Inner.Dx()-((len(line.Data)-1)-i) >= 1; i-- {
			if line.Data[i] > max {
				max = line.Data[i]
			}
		}
		// prints sparkline
		for x := s.Inner.Dx(); x >= 1; x-- {
			char := ui.BARS[1]
			if (s.Inner.Dx() - x) < len(line.Data) {
				offset := s.Inner.Dx() - x
				curItem := line.Data[(len(line.Data)-1)-offset]
				percent := float64(curItem) / float64(max)
				index := int(percent*float64(len(ui.BARS)-2)) + 1
				if index < 1 || index >= len(ui.BARS) {
					log.Printf(
						"invalid sparkline data value. index: %v, percent: %v, curItem: %v, offset: %v",
						index, percent, curItem, offset,
					)
				} else {
					char = ui.BARS[index]
				}
			}
			buf.SetCell(
				ui.NewCell(char, ui.NewStyle(line.LineColor)),
				image.Pt(s.Inner.Min.X+x-1, s.Inner.Min.Y+sparkY-1),
			)
		}
		dx := s.Inner.Dx()
		if len(line.Data) > 4*dx {
			line.Data = line.Data[dx-1:]
		}
	}
}
