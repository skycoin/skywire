package gioterm

import (
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
			t.down = false
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
