// Package skycloud pkg/skycloud/mask_test.go c0-com-util
package skycloud

import (
	"image"
	"reflect"
	"testing"
)

func TestNoRoomNoCloud(t *testing.T) {
	for _, size := range [][2]int{{40, 40}, {200, 5}} {
		m := New()
		m.Fit(size[0], size[1])
		if _, on := m.Placed(); on {
			t.Errorf("%dx%d: the cloud was placed with no room for it", size[0], size[1])
		}
		if got := m.IntensityAt(10, 2, 123); got != 123 {
			t.Errorf("%dx%d: intensity changed to %d with no cloud placed", size[0], size[1], got)
		}
	}
}

func TestFitGrowsAndStaysInBox(t *testing.T) {
	var lastW int
	for _, cols := range []int{60, 80, 120, 200} {
		m := New()
		m.Box = func(cols, rows int) image.Rectangle { return image.Rect(10, 0, cols-3, rows) }
		m.Fit(cols, 50)
		st, on := m.Placed()
		if !on {
			t.Fatalf("cols=%d: expected the cloud to fit", cols)
		}
		w, _ := st.Size()
		if w < lastW {
			t.Errorf("cols=%d: a smaller cloud (%d) than at a narrower screen (%d)", cols, w, lastW)
		}
		lastW = w
		if st.X < 10 || st.X+w > cols-3 {
			t.Errorf("cols=%d: cloud spans %d to %d, outside the box", cols, st.X, st.X+w)
		}
	}
}

func TestFitIsNotSticky(t *testing.T) {
	m := New()
	m.Fit(200, 50)
	if _, on := m.Placed(); !on {
		t.Fatal("expected the cloud to fit at 200 columns")
	}
	m.Fit(20, 50)
	if _, on := m.Placed(); on {
		t.Error("the cloud stayed placed after a fit to a narrow screen")
	}
}

func TestPlaceIsKept(t *testing.T) {
	m := New()
	m.Place([]string{"##", "##"}, 5, 6)
	m.Fit(10, 10)
	if !m.Covers(5, 6) || m.Covers(4, 6) {
		t.Error("Fit moved a placed stencil")
	}
	if got := m.IntensityAt(6, 7, 0); got != Floor {
		t.Errorf("inside the cloud got %d, want the floor %d", got, Floor)
	}
}

func TestTint(t *testing.T) {
	m := New()
	m.Place([]string{"#"}, 0, 0)
	if r, g, b := m.TintAt(0, 0, 0, 255, 0); r != 0 || g == 0 || b <= g {
		t.Errorf("inside got %d,%d,%d, want blue", r, g, b)
	}
	if r, g, b := m.TintAt(1, 0, 0, 200, 0); r != 0 || g != 200 || b != 0 {
		t.Errorf("outside got %d,%d,%d, want it unchanged", r, g, b)
	}
	m.Outside = 128
	if _, g, _ := m.TintAt(1, 0, 0, 200, 0); g != 100 {
		t.Errorf("outside with Outside 128 got green %d, want 100", g)
	}
	m.Bright = true
	if _, _, b := m.TintAt(0, 0, 0, 100, 0); b != 100 {
		t.Errorf("Bright inside got blue %d, want the brightest channel 100", b)
	}
}

// The generated shapes are what Sample makes of the logo today, so the help
// screen and anything sampling the logo itself draw the same cloud.
func TestShapesMatchSample(t *testing.T) {
	img, err := Logo()
	if err != nil {
		t.Fatal(err)
	}
	got := Shapes()
	if len(got) == 0 {
		t.Fatal("no shapes were generated")
	}
	for i, shape := range got {
		rows := len(shape)
		var want []string
		for _, r := range []int{rows, rows + 1, rows + 2} {
			if s := Trim(Sample(img, Columns(img, r), r)); len(s) == rows {
				want = s
				break
			}
		}
		if !reflect.DeepEqual(shape, want) {
			t.Errorf("shape %d (%d rows) differs from a fresh sample. Run go generate ./pkg/skycloud/...", i, rows)
		}
	}
}
