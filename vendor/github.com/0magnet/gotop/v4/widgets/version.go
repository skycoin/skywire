package widgets

import (
	"image"

	ui "github.com/gizak/termui/v3"
)

// VersionLabel overlays a short right-aligned text (the skywire build version,
// as printed by `skywire -b`) on the top border, top-right corner. It is
// rendered AFTER the grid so it sits on top of the rightmost top widget's
// border without clearing it: its rect is exactly the text width (it does not
// call Block.Draw), so only the cells under the text are overwritten.
type VersionLabel struct {
	ui.Block
	text string
}

func NewVersionLabel(text string) *VersionLabel {
	self := &VersionLabel{Block: *ui.NewBlock(), text: text}
	self.Border = false
	return self
}

// Reposition sets the rect to the top-right corner for the given terminal
// width: a text-wide, one-row strip ending one column shy of the right edge
// (so the corner border char is preserved). Call on init + on every resize.
func (vl *VersionLabel) Reposition(termWidth int) {
	n := len(vl.text)
	if n == 0 {
		return
	}
	x0 := termWidth - n - 1
	if x0 < 0 {
		x0 = 0
	}
	vl.SetRect(x0, 0, termWidth-1, 1)
}

func (vl *VersionLabel) Draw(buf *ui.Buffer) {
	if vl.text == "" {
		return
	}
	buf.SetString(vl.text, ui.Theme.Default, image.Pt(vl.Inner.Min.X, vl.Inner.Min.Y))
}
