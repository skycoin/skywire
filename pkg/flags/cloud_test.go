// Package flags pkg/flags/cloud_test.go c1-com-util
package flags

import (
	"testing"

	"github.com/skycoin/skywire/pkg/skycloud"
)

// The cloud stays off a terminal without room beside the text. The threshold is
// 128 columns: the text and its indent, the gutter, the margin and the 40 column
// narrowest shape.
func TestHelpCloudNeedsRoom(t *testing.T) {
	for _, cols := range []int{80, 100, 118, 127} {
		m := helpCloud()
		m.Fit(cols, 40)
		if _, on := m.Placed(); on {
			t.Errorf("cols=%d: the cloud appeared with no room for it", cols)
		}
	}
	m := helpCloud()
	m.Fit(128, 40)
	if _, on := m.Placed(); !on {
		t.Error("cols=128: the cloud did not appear")
	}
}

// The cloud always clears the text's column budget and keeps its margin.
func TestHelpCloudClearsText(t *testing.T) {
	for _, cols := range []int{128, 140, 160, 200} {
		m := helpCloud()
		m.Fit(cols, 40)
		st, on := m.Placed()
		if !on {
			t.Fatalf("cols=%d: expected the cloud to fit", cols)
		}
		w, _ := st.Size()
		if st.X < HelpWidth+nameIndent+cloudGutter {
			t.Errorf("cols=%d: cloud starts at %d, inside the text's budget", cols, st.X)
		}
		if right := st.X + w; right > cols-skycloud.Margin {
			t.Errorf("cols=%d: cloud ends at %d, past the margin", cols, right)
		}
	}
}
