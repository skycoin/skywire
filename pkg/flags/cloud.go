// Package flags pkg/flags/cloud.go c0-com-util
//
// The cloud in the rain.
//
// On a terminal wide enough to have room to spare beside the help text, the
// Skycoin cloud appears in it — a field of the logo's own blue with the rain
// falling through it as a knockout, the same glyphs in the same streams, drawn
// black where they cross the mark and green everywhere else.
//
// The color is the shape and the rain is only what passes over it. Three
// earlier attempts had it the other way round and each failed for the same
// reason: the rain is mostly gaps and its streams are vertical, so anything
// that draws a shape *out of* the rain is at the mercy of where the rain
// happens to be. Brightening the lit cells traced the streams. Filling the
// dark ones with the rain's glyphs made a picture out of katakana. Recoloring
// only the lit cells was the prettiest and the least legible of the three.
//
// The shape is Skycoin's own, from the logo skycoin/skycoin serves in its block
// explorer — a cloud cut by diagonal slashes. The slashes are what distinguish
// it from any other cloud, and they are also what costs the resolution: see
// gencloud for why there is no small variant. They survive at one cell wide
// here because the wash does not depend on a stream falling in them.
//
// It costs nothing when there is no room. The whole thing is gated on width:
// the help text is wrapped to a hardcoded 80 columns (see HelpWidth), which is
// what makes "everything past column 86 is free" a fact rather than a guess,
// and below that width the mask hands every cell back exactly as the rain had
// it.
package flags

import (
	"github.com/0magnet/termanim/matrix/backdrop"
)

//go:generate go run ./gencloud gencloud/skycoin-cloud.png cloudshapes.go

const (
	// cloudGutter is the clear columns kept between the text's own column
	// budget and the logo, so the two never read as one block.
	cloudGutter = 4

	// cloudMargin is the clear columns kept to the right of the logo. The rain
	// runs to the edge of the screen and the logo should not.
	cloudMargin = 2

	// cloudRowRoom is the rows that must be left over after the logo, so it
	// never fills the screen top to bottom.
	cloudRowRoom = 2
)

// cloudMask washes the logo's silhouette in Skycoin blue and knocks the rain
// through it in black, when there is room for it beside the help text.
//
// It implements backdrop.Fitter because neither half of the placement can be
// known when the options are built: the screen width is settled inside the
// compositor, and the number of rows depends on how long this particular help
// screen turned out to be.
type cloudMask struct {
	st backdrop.Stencil
	on bool
}

// Fit picks the largest silhouette that fits the space left over beside the
// text and centers it in that space.
//
// Both directions matter, and independently. A wide terminal gives the width
// for a bigger logo; a long help screen gives the height. `skywire cli --help`
// is tall enough for a size that `cli pty exec --help` has no room for on the
// same terminal, so the size is chosen per screen rather than per terminal.
//
// Largest-that-fits rather than one size scaled: these are cell grids, and a
// resampled cell grid is a worse picture. The variants run from 31x15 to
// 73x36, covering a terminal with just enough room through one opened full
// width.
//
// Centered in the free column range rather than pinned to the right margin.
// Pinned, it read as something stuck to the edge of the window; centered
// between the text and the edge it reads as occupying the space the text left
// over, which is what it is.
func (c *cloudMask) Fit(cols, rows int) {
	c.on = false

	// The free range is everything right of the text's column budget and its
	// gutter, up to the margin kept off the screen edge.
	from := HelpWidth + nameIndent + cloudGutter
	to := cols - cloudMargin
	free := to - from
	if free <= 0 {
		return
	}

	for i := len(cloudShapes) - 1; i >= 0; i-- {
		shape := cloudShapes[i]
		st := backdrop.Stencil{Rows: shape}
		w, h := st.Size()
		if w > free || h+cloudRowRoom > rows {
			continue
		}
		st.X = from + (free-w)/2
		st.Y = (rows - h) / 2
		c.st, c.on = st, true
		return
	}
}

// WashAt implements backdrop.Washer: the silhouette is a solid field of
// Skycoin blue, painted whether or not the rain lit the cell.
//
// This is what finally makes the mark read, and it took three tries to get
// here. Brightening the rain inside the shape traced the streams instead of
// the shape, because the streams are vertical and the shape is not. Lighting
// the shape's dark cells with the rain's own glyphs made it solid but turned
// it into a picture made of katakana rather than rain. Recoloring only the
// cells the rain lit was prettiest and least legible of all: the rain is
// mostly gaps, so the mark came out as a handful of blue smears.
//
// Painting the background leaves the rain exactly as sparse as it was and
// makes the region solid anyway. The outline is crisp and the diagonal slashes
// survive at one cell wide, because neither depends on a stream happening to
// fall there.
func (c *cloudMask) WashAt(x, y int) (int32, int32, int32, bool) {
	if !c.on || !c.st.Covers(x, y) {
		return 0, 0, 0, false
	}
	return cloudR, cloudG, cloudB, true
}

// TintAt implements backdrop.Tinter: the rain crossing the silhouette is drawn
// black, so it reads as a knockout through the blue rather than as glyphs laid
// on top of it.
//
// Black rather than a darkened green: against the wash the point is contrast,
// not hue, and the rain keeps its shape — the same glyphs in the same streams,
// scrambling as they always do — while the color behind them is the logo. What
// falls through the slashes stays green, which is what keeps the mark looking
// like weather passing behind something rather than a panel with a pattern.
func (c *cloudMask) TintAt(x, y int, r, g, b int32) (int32, int32, int32) {
	if !c.on || !c.st.Covers(x, y) {
		return r, g, b
	}
	return 0, 0, 0
}

// The blue the silhouette is washed in: #002d66, the logo's own #0072ff taken
// down to something that sits behind the rain rather than in front of it.
//
// Dimmed because it can be. The shape comes from the wash covering every cell
// of the silhouette, not from the color being loud, so the mark stays exactly
// as crisp at a fifth of the brightness — the slashes are cut by which cells
// are washed, and that does not change. At full strength the cloud reads as a
// panel bolted over the screen; at this one it reads as something the rain is
// falling in front of, which is the effect worth having.
const (
	cloudR = 0x00
	cloudG = 0x2d
	cloudB = 0x66
)

// IntensityAt implements backdrop.Mask. With no room found, every cell is
// handed back exactly as the rain had it.
func (c *cloudMask) IntensityAt(x, y, n int) int {
	if !c.on {
		return n
	}
	return c.st.IntensityAt(x, y, n)
}
