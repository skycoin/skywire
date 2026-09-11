// Package flags pkg/flags/cloud.go c0-com-util
//
// The cloud in the rain.
//
// On a terminal wide enough to have room to spare beside the help text, the
// Skycoin cloud appears in it — not drawn over the rain but made of it. Cells
// inside the silhouette are lit to a dim floor with the glyph the rain already
// holds at that position, so the mark is the same alphabet scrambling in the
// same streams, and the streams still fall visibly brighter through it.
//
// Brightening the rain that was already there does not work, and the reason is
// worth keeping: the rain is mostly gaps and its streams are vertical, so
// scaling only the lit cells traces the streams rather than the shape. Filling
// the silhouette to a floor is what makes it findable.
//
// The shape is Skycoin's own, from the logo skycoin/skycoin serves in its block
// explorer — a cloud cut by diagonal slashes. The slashes are what distinguish
// it from any other cloud, and they are also what costs the resolution: see
// gencloud for why there is no small variant.
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

	// cloudFloor is the least the rain may be inside the shape, on the 0-255
	// scale its palette is indexed by.
	//
	// This is what makes the logo legible. Brightening the cells the rain
	// already lit is not enough on its own — the rain is mostly gaps and its
	// streams run vertically, so brightening traces the streams and not the
	// shape. Lighting every cell inside the silhouette to a dim floor draws
	// the outline; the streams still fall through it at their own brightness,
	// four times this and up, so the shape reads as rain that is denser here
	// rather than as a panel behind the rain.
	//
	// 95 against trails that run to 200. The mark is a cloud cut by diagonal
	// slashes, and the slashes are the part that identifies it: they are one or
	// two cells wide, which is also the width of the rain's own gaps, so the
	// filled bands have to sit clearly above an ordinary trail cell or the
	// slashes read as more rain. Higher flattens the streams into an even block
	// and loses the weather; lower and the mark stops being a Skycoin cloud and
	// becomes a cloud.
	cloudFloor = 95
)

// cloudMask brightens the rain inside the logo's silhouette, when there is
// room for it beside the help text.
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
// text and places it against the right margin, vertically centered.
//
// Largest-that-fits rather than one size scaled: these are cell grids, and a
// scaled cell grid is a different, worse picture. Four sizes cover the range
// from a terminal with just enough room to one opened full width.
func (c *cloudMask) Fit(cols, rows int) {
	c.on = false

	free := cols - (HelpWidth + nameIndent) - cloudGutter - cloudMargin
	if free <= 0 {
		return
	}

	for i := len(cloudShapes) - 1; i >= 0; i-- {
		shape := cloudShapes[i]
		st := backdrop.Stencil{Rows: shape, Floor: cloudFloor}
		w, h := st.Size()
		if w > free || h+cloudRowRoom > rows {
			continue
		}
		st.X = cols - cloudMargin - w
		st.Y = (rows - h) / 2
		c.st, c.on = st, true
		return
	}
}

// IntensityAt implements backdrop.Mask. With no room found, every cell is
// handed back exactly as the rain had it.
func (c *cloudMask) IntensityAt(x, y, n int) int {
	if !c.on {
		return n
	}
	return c.st.IntensityAt(x, y, n)
}
