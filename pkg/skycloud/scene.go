// Package skycloud pkg/skycloud/scene.go c0-com-util
package skycloud

import (
	"github.com/gdamore/tcell/v3"

	"github.com/0magnet/termanim/canvas"
	"github.com/0magnet/termanim/matrix"
	"github.com/0magnet/termanim/matrix/backdrop"
	"github.com/0magnet/termanim/matrix/loop"
)

// StepRate is rain steps per second, the rate termanim's rain was tuned at.
const StepRate = 30

// Scene is the cloud in moving rain, filling a backdrop.Frame. It is a
// canvas.CellAnimation, so canvas.RunCells plays it on a screen.
type Scene struct {
	// Mask is the cloud. New() unless the caller sets one before Resize.
	Mask *Mask

	seed   int64
	period int // steps in a loop; zero is the live rain
	live   *matrix.Matrix
	loop   *loop.Rain
	frame  *backdrop.Frame
	acc    float64
	step   int
}

// NewLive is the cloud in termanim's simulated rain, which never repeats.
func NewLive(seed int64) *Scene { return &Scene{Mask: New(), seed: seed} }

// NewLoop is the cloud in rain that repeats every period steps exactly, for a
// clip that loops. See loop.New for how period is rounded.
func NewLoop(seed int64, period int) *Scene {
	return &Scene{Mask: New(), seed: seed, period: max(period, 1)}
}

// Resize starts the rain over on a cols by rows grid.
func (s *Scene) Resize(cols, rows int) {
	s.frame = backdrop.NewFrame(cols, rows)
	s.frame.SetMask(s.Mask)
	s.live, s.loop = nil, nil
	if s.period > 0 {
		s.loop = loop.New(s.seed, cols, rows, s.period)
		s.loop.Seek(s.step)
		return
	}
	s.live = matrix.New(s.seed)
	s.live.Resize(cols, rows)
	// The simulation starts empty. Two screens of steps fill it.
	s.live.Advance(2 * rows)
}

// Period is the steps in one loop, or zero for the live rain.
func (s *Scene) Period() int {
	if s.loop == nil {
		return 0
	}
	return s.loop.Period()
}

// Advance moves the rain on by dt seconds.
func (s *Scene) Advance(dt float64) {
	if s.live != nil {
		s.live.AdvanceTime(dt)
		return
	}
	s.acc += dt * StepRate
	n := int(s.acc)
	s.acc -= float64(n)
	s.Seek(s.step + n)
}

// Seek moves a looping scene to step t. The live rain cannot go back and
// ignores it.
func (s *Scene) Seek(t int) {
	s.step = t
	if s.loop != nil {
		s.loop.Seek(t)
	}
}

// Fill draws the current step into the frame and returns it.
func (s *Scene) Fill() *backdrop.Frame {
	switch {
	case s.live != nil:
		s.frame.FromMatrix(s.live, 256)
	case s.loop != nil:
		s.frame.FromRain(s.loop, s.loop.Palette, 256)
	}
	return s.frame
}

// Frame implements canvas.CellAnimation.
func (s *Scene) Frame(screen tcell.Screen, cols, rows int, dt float64) {
	s.Advance(dt)
	f := s.Fill()
	for y := range rows {
		for x := range cols {
			c, ok := f.At(x, y)
			if !ok {
				screen.Put(x, y, canvas.Blank, tcell.StyleDefault) //nolint:errcheck // one cell cannot fail
				continue
			}
			canvas.PutRune(screen, x, y, c.Rune, tcell.StyleDefault.Foreground(c.Fg).Background(c.Bg).Bold(c.Bold))
		}
	}
}
