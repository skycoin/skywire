// Package flags pkg/flags/cloud_test.go c0-com-util
package flags

import "testing"

// The logo stays off any terminal that does not have room for it beside the
// text. This is the whole gate: below it, every cell is handed back untouched
// and the help screen is exactly what it was.
//
// The threshold is 118 columns — 80 of text, its indent, the gutter, the
// margin, and 30 for the narrowest variant that still resolves the logo's
// diagonal slashes.
func TestNoRoomNoCloud(t *testing.T) {
	for _, cols := range []int{80, 90, 100, 110} {
		c := &cloudMask{}
		c.Fit(cols, 40)
		if c.on {
			t.Errorf("cols=%d: the logo appeared with no room for it", cols)
		}
		if got := c.IntensityAt(50, 20, 123); got != 123 {
			t.Errorf("cols=%d: intensity changed to %d with no logo placed", cols, got)
		}
	}
}

// A short help screen has no room for it either, however wide the terminal is.
func TestNoRoomVerticallyNoCloud(t *testing.T) {
	c := &cloudMask{}
	c.Fit(200, 5)
	if c.on {
		t.Error("the logo appeared on a screen five rows tall")
	}
}

// Wider terminals get larger variants, and the logo always clears the text's
// column budget and keeps its right margin.
func TestPlacementGrowsAndClears(t *testing.T) {
	var lastW int
	for _, cols := range []int{120, 130, 150, 200} {
		c := &cloudMask{}
		c.Fit(cols, 40)
		if !c.on {
			t.Fatalf("cols=%d: expected the logo to fit", cols)
		}
		w, _ := c.st.Size()
		if w < lastW {
			t.Errorf("cols=%d: got a smaller logo (%d) than at a narrower terminal (%d)", cols, w, lastW)
		}
		lastW = w

		if c.st.X < HelpWidth+nameIndent+cloudGutter {
			t.Errorf("cols=%d: logo starts at %d, inside the text's budget", cols, c.st.X)
		}
		if right := c.st.X + w; right > cols-cloudMargin {
			t.Errorf("cols=%d: logo ends at %d, past the margin", cols, right)
		}
	}
}

// Fit is called per help screen, so a mask reused on a smaller one must give
// up its placement rather than keep drawing off the edge.
func TestFitIsNotSticky(t *testing.T) {
	c := &cloudMask{}
	c.Fit(200, 40)
	if !c.on {
		t.Fatal("expected the logo to fit at 200 columns")
	}
	c.Fit(80, 40)
	if c.on {
		t.Error("the logo stayed placed after being fitted to a narrow screen")
	}
}

// Every shape the generator emits must be non-empty and rectangular enough to
// place: a row longer than the block's own measured width would draw outside
// the space Fit reserved for it.
func TestShapesAreWellFormed(t *testing.T) {
	if len(cloudShapes) == 0 {
		t.Fatal("no shapes were generated")
	}
	for i, shape := range cloudShapes {
		if len(shape) == 0 {
			t.Errorf("shape %d is empty", i)
			continue
		}
		var ink bool
		for _, row := range shape {
			for _, r := range row {
				if r != ' ' {
					ink = true
					break
				}
			}
		}
		if !ink {
			t.Errorf("shape %d is all blank", i)
		}
	}
}
