//go:build cgo && unix

package gioterm

import (
	"math"
	"time"

	"github.com/0magnet/gio/f32"
	"github.com/0magnet/gio/io/pointer"

	"github.com/0magnet/progterm/glterm"
)

// longPress is how long a still finger is held to open the context menu,
// as a phone has no right button.
const longPress = 500 * time.Millisecond

// touch holds a finger's press back until it is known to be a tap, a drag
// that selects, or a long press that opens the menu: forwarded at once, a
// press would clear the selection the menu is wanted for.
type touch struct {
	down  bool // pressed, and nothing sent yet
	menu  bool // the press opened the menu; the rest of the touch is dropped
	drag  bool // the press was sent, and the finger is selecting
	at    f32.Point
	since time.Time
}

// event takes a touch event at now, with slop the distance a finger may
// move and still be held, and returns what the terminal is to be sent.
func (t *touch) event(e pointer.Event, now time.Time, slop float32) []glterm.MouseEvent {
	at := func(p f32.Point, b glterm.Button, a glterm.Action) glterm.MouseEvent {
		return glterm.MouseEvent{X: p.X, Y: p.Y, Button: b, Action: a, Mods: mods(e.Modifiers)}
	}
	switch e.Kind {
	case pointer.Press:
		*t = touch{down: true, at: e.Position, since: now}
		return nil
	case pointer.Drag, pointer.Move:
		if t.menu {
			return nil
		}
		if t.down {
			if d := e.Position.Sub(t.at); d.X*d.X+d.Y*d.Y < slop*slop {
				return nil
			}
			t.down, t.drag = false, true
			return []glterm.MouseEvent{at(t.at, glterm.ButtonLeft, glterm.Press), at(e.Position, glterm.ButtonNone, glterm.Move)}
		}
		return []glterm.MouseEvent{at(e.Position, glterm.ButtonNone, glterm.Move)}
	case pointer.Release:
		var out []glterm.MouseEvent
		if t.down {
			out = append(out, at(t.at, glterm.ButtonLeft, glterm.Press))
		}
		if !t.menu {
			// The finger may lift past the last move, and the selection ends there.
			if !t.down {
				out = append(out, at(e.Position, glterm.ButtonNone, glterm.Move))
			}
			out = append(out, at(e.Position, glterm.ButtonLeft, glterm.Release))
		}
		*t = touch{}
		return out
	case pointer.Cancel:
		*t = touch{}
	}
	return nil
}

// due reports when a held press becomes a long press, if one is waiting.
func (t *touch) due() (time.Time, bool) { return t.since.Add(longPress), t.down }

// expire opens the menu, by a right press, for a press held still past
// longPress at now.
func (t *touch) expire(now time.Time) []glterm.MouseEvent {
	if !t.down || now.Before(t.since.Add(longPress)) {
		return nil
	}
	t.down, t.menu = false, true
	return []glterm.MouseEvent{{X: t.at.X, Y: t.at.Y, Button: glterm.ButtonRight, Action: glterm.Press}}
}

// abort ends the touch when a second finger makes it a pinch: a selection
// being dragged is let go, and nothing else is sent.
func (t *touch) abort() []glterm.MouseEvent {
	var out []glterm.MouseEvent
	if t.drag {
		out = append(out, glterm.MouseEvent{X: t.at.X, Y: t.at.Y, Button: glterm.ButtonLeft, Action: glterm.Release})
	}
	*t = touch{}
	return out
}

// pinch scales the font with two fingers. From the second finger down until
// the last is up the touch is the pinch's, and no finger selects.
type pinch struct {
	at map[pointer.ID]f32.Point
	on bool
	d0 float32 // the fingers' distance when the second came down
	z0 float64 // the zoom then
}

// event takes a touch event and reports whether the pinch took it, setting
// *zoom as the fingers move.
func (p *pinch) event(e pointer.Event, zoom *float64) bool {
	if p.at == nil {
		p.at = map[pointer.ID]f32.Point{}
	}
	switch e.Kind {
	case pointer.Press:
		p.at[e.PointerID] = e.Position
	case pointer.Drag, pointer.Move:
		if _, ok := p.at[e.PointerID]; ok {
			p.at[e.PointerID] = e.Position
		}
	case pointer.Release:
		delete(p.at, e.PointerID)
	case pointer.Cancel:
		clear(p.at)
	}
	switch {
	case p.on && len(p.at) == 0:
		p.on = false
		return true
	case p.on:
		if len(p.at) == 2 {
			*zoom = glterm.PinchZoom(p.z0, p.d0, p.dist())
		}
		return true
	case len(p.at) == 2:
		p.on, p.d0, p.z0 = true, p.dist(), *zoom
		return true
	}
	return false
}

// dist is the distance between the first two fingers down.
func (p *pinch) dist() float32 {
	var pts []f32.Point
	for _, v := range p.at {
		pts = append(pts, v)
	}
	if len(pts) < 2 {
		return 0
	}
	d := pts[0].Sub(pts[1])
	return float32(math.Hypot(float64(d.X), float64(d.Y)))
}
