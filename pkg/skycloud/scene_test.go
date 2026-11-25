// Package skycloud pkg/skycloud/scene_test.go c0-com-util
package skycloud

import (
	"reflect"
	"testing"

	"github.com/0magnet/termanim/matrix/backdrop"
)

func grab(s *Scene, cols, rows int) []backdrop.Cell {
	f := s.Fill()
	out := make([]backdrop.Cell, 0, cols*rows)
	for y := range rows {
		for x := range cols {
			c, _ := f.At(x, y)
			out = append(out, c)
		}
	}
	return out
}

// The looping scene, cloud and all, is the same a period on.
func TestLoopSceneRepeats(t *testing.T) {
	s := NewLoop(7, 120)
	s.Resize(160, 50)
	if _, on := s.Mask.Placed(); on {
		t.Fatal("placed before the first fill")
	}
	s.Seek(10)
	a := grab(s, 160, 50)
	if _, on := s.Mask.Placed(); !on {
		t.Fatal("the cloud did not fit on a 160x50 screen")
	}
	s.Seek(10 + s.Period())
	if !reflect.DeepEqual(a, grab(s, 160, 50)) {
		t.Error("the frame a period on differs")
	}
	s.Seek(11)
	if reflect.DeepEqual(a, grab(s, 160, 50)) {
		t.Error("the next step is the same frame, the rain is not moving")
	}
}

// Advance turns seconds into steps at StepRate, carrying the fraction.
func TestLoopSceneAdvance(t *testing.T) {
	s := NewLoop(1, 300)
	s.Resize(40, 20)
	for range 10 {
		s.Advance(0.05)
	}
	if s.step != StepRate/2 {
		t.Errorf("after half a second at %d steps a second the step is %d", StepRate, s.step)
	}
}

// Every cell inside the placed cloud is lit and blue, in either rain.
func TestCloudIsLitAndBlue(t *testing.T) {
	for name, s := range map[string]*Scene{"live": NewLive(3), "loop": NewLoop(3, 90)} {
		s.Resize(160, 50)
		f := s.Fill()
		st, on := s.Mask.Placed()
		if !on {
			t.Fatalf("%s: the cloud did not fit", name)
		}
		w, h := st.Size()
		for y := st.Y; y < st.Y+h; y++ {
			for x := st.X; x < st.X+w; x++ {
				if !st.Covers(x, y) {
					continue
				}
				c, ok := f.At(x, y)
				if !ok {
					t.Fatalf("%s: cell %d,%d inside the cloud is dark", name, x, y)
				}
				if r, g, b := c.Fg.RGB(); b <= g || r > g {
					t.Fatalf("%s: cell %d,%d inside the cloud is %d,%d,%d, not blue", name, x, y, r, g, b)
				}
			}
		}
	}
}
