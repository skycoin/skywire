//go:build js && wasm

package winbox

// Docking: pinning a window to an edge of the viewport, where it keeps a fixed
// thickness and stretches along that edge.
//
// This is the one feature here that WinBox.js does not have, and it is opt-in:
// the zero value of every type below is "not docked", so a program that never
// mentions docking behaves exactly as the port did before it existed. That
// property is worth more than the feature — the point of this package is to be
// a faithful port, and a faithful port has to stay faithful by default.
//
// It earns its place because it is not a new layout model, only a constrained
// use of the one already here. Maximize is
//
//	resizeRaw(rootW-Left-Right, rootH-Top-Bottom); moveRaw(Left, Top)
//
// and a dock is that with one axis pinned to a thickness instead of filling.
// The library also already arranges windows along an edge and has them share
// it — that is what the minimize stack is — so an edge is not a foreign idea
// here either.
//
// What a dock adds that maximize does not have is RESERVATION. A docked window
// can take its strip out of the viewport for everyone else, so a maximized
// window fills what is left rather than covering the dock. That is what makes
// a docked control panel usable, and it is why DockReserve is the default mode:
// a dock that merely floats at an edge is a window with an unusual position,
// which is something you could already arrange by hand.
//
// Per-window Top/Right/Bottom/Left offsets are ignored while docked. They exist
// to inset a free-floating window from the viewport, and a dock's whole job is
// to be against the edge; honoring both would mean two answers to where the
// edge is.

// Edge is the side of the viewport a window is docked to. The zero value,
// EdgeNone, means the window is not docked.
type Edge uint8

// The four edges a window can dock to.
const (
	EdgeNone Edge = iota
	EdgeTop
	EdgeRight
	EdgeBottom
	EdgeLeft
)

// String names the edge, as used in the window's CSS class.
func (e Edge) String() string {
	switch e {
	case EdgeTop:
		return "top"
	case EdgeRight:
		return "right"
	case EdgeBottom:
		return "bottom"
	case EdgeLeft:
		return "left"
	}
	return "none"
}

// vertical reports whether the edge runs along the top or bottom, so the dock's
// thickness is a height rather than a width.
func (e Edge) vertical() bool { return e == EdgeTop || e == EdgeBottom }

// DockMode is what a dock does to the space it occupies. The zero value
// reserves it, which is the useful case.
type DockMode uint8

// The two ways a dock can relate to the rest of the viewport.
const (
	// DockReserve takes the dock's strip out of the area other windows treat
	// as the viewport: maximize fills what is left, and the minimize stack
	// lines up inside it.
	DockReserve DockMode = iota
	// DockOverlay leaves the viewport alone, so the dock sits over whatever is
	// behind it. Useful for a panel that should hide the model rather than
	// shrink it.
	DockOverlay
)

// stackDock is the docked windows in the order they docked, which is the order
// they claim space in: each dock is placed inside the area left by the docks
// before it, so a left dock and a bottom dock meet at a corner instead of
// overlapping it. Undocking and re-docking moves a window to the end, which is
// how you change that order.
var stackDock []*WinBox

// dockContentBox is the area left over after every reserving dock — where a
// maximized window goes, and where the minimize stack lines up.
func dockContentBox() (x, y, w, h float64) {
	return dockBoxBefore(nil)
}

// dockBoxBefore is the area available to the given docked window: the viewport
// less every reserving dock ahead of it in the stack. Passing nil returns the
// area left after all of them.
//
// Hidden and minimized docks are skipped rather than reserving space they are
// not using — a panel put away should give its strip back.
func dockBoxBefore(before *WinBox) (x, y, w, h float64) {
	x, y, w, h = 0, 0, rootW, rootH
	for _, d := range stackDock {
		if d == before {
			return
		}
		if d.dockMode != DockReserve || d.Hidden || d.Min || d.dock == EdgeNone {
			continue
		}
		t := d.dockThickIn(w, h)
		switch d.dock {
		case EdgeTop:
			y += t
			h -= t
		case EdgeBottom:
			h -= t
		case EdgeLeft:
			x += t
			w -= t
		case EdgeRight:
			w -= t
		case EdgeNone:
		}
	}
	return
}

// dockThickIn is the dock's thickness clamped to the box it has to fit in and
// to the window's own minimum, so a dock can never be dragged to nothing or
// past the far side of the viewport.
func (w *WinBox) dockThickIn(boxW, boxH float64) float64 {
	t := w.dockThick
	var lo, hi float64
	if w.dock.vertical() {
		lo, hi = w.MinHeight, boxH
	} else {
		lo, hi = w.MinWidth, boxW
	}
	if t < lo {
		t = lo
	}
	if t > hi {
		t = hi
	}
	if t < 0 {
		t = 0
	}
	return t
}

// clampDockThick clamps the stored thickness to what the dock's box allows.
// Dragging past either end would otherwise bank travel that has to be dragged
// all the way back before the edge moves again.
func (w *WinBox) clampDockThick() {
	_, _, bw, bh := dockBoxBefore(w)
	w.dockThick = w.dockThickIn(bw, bh)
}

// Dock pins the window to an edge with the given thickness — a height when
// docking to the top or bottom, a width when docking to the left or right. A
// zero-value size keeps the window's current extent along that axis, so
// w.Dock(EdgeLeft, Unit{}) docks a 300px-wide window as a 300px-wide dock.
//
// Docking cancels maximize and fullscreen, and restores a minimized window,
// since all four are answers to the same question about where the window goes.
// The pre-dock geometry is remembered, and Undock puts it back.
func (w *WinBox) Dock(edge Edge, size Unit) *WinBox {
	if edge == EdgeNone {
		return w.Undock()
	}

	if isFullscreen != nil {
		cancelFullscreen()
	}
	if w.Min {
		removeMinStack(w)
	}
	if w.Max {
		w.Max = false
		w.RemoveClass("max")
	}

	// Remembered once, on the way in. Docking an already-docked window to
	// another edge must not overwrite the geometry with the dock's own, or
	// undocking would return it to the strip it was just in.
	if w.dock == EdgeNone {
		w.preDock = dockGeom{w.X, w.Y, w.Width, w.Height, true}
		stackDock = append(stackDock, w)
	} else {
		w.RemoveClass("dock-" + w.dock.String())
	}

	if !size.falsy() {
		if edge.vertical() {
			w.dockThick = size.parse(rootH, 0)
		} else {
			w.dockThick = size.parse(rootW, 0)
		}
	} else if w.dockThick == 0 {
		if edge.vertical() {
			w.dockThick = w.Height
		} else {
			w.dockThick = w.Width
		}
	}

	w.dock = edge
	w.AddClass("dock")
	w.AddClass("dock-" + edge.String())

	updateDocks()

	if w.OnDock != nil {
		w.OnDock(w, edge)
	}
	return w
}

// Undock returns the window to floating, at the position and size it had when
// it docked.
func (w *WinBox) Undock() *WinBox {
	if w.dock == EdgeNone {
		return w
	}

	w.RemoveClass("dock-" + w.dock.String())
	w.RemoveClass("dock")
	w.dock = EdgeNone

	for i, d := range stackDock {
		if d == w {
			stackDock = append(stackDock[:i], stackDock[i+1:]...)
			break
		}
	}

	if w.preDock.valid {
		w.X, w.Y = w.preDock.x, w.preDock.y
		w.Width, w.Height = w.preDock.w, w.preDock.h
		w.preDock.valid = false
	}
	w.applySize()
	w.applyPos()

	// The docks behind it inherit the space it gave up.
	updateDocks()

	if w.OnUndock != nil {
		w.OnUndock(w)
	}
	return w
}

// Docked reports which edge the window is docked to, or EdgeNone.
func (w *WinBox) Docked() Edge { return w.dock }

// DockThickness is the dock's current extent across its edge, in pixels.
func (w *WinBox) DockThickness() float64 { return w.dockThick }

// SetDockThickness resizes a docked window across its edge. It is what the
// dock's own resize handle drives, and does nothing to an undocked window.
func (w *WinBox) SetDockThickness(px float64) *WinBox {
	if w.dock == EdgeNone {
		return w
	}
	w.dockThick = px
	updateDocks()
	return w
}

// dockGeom is the floating geometry a window had before it docked.
type dockGeom struct {
	x, y, w, h float64
	valid      bool
}

// updateDocks re-places every docked window, and re-fits any maximized window
// to the area they leave. Called whenever the set of docks, their sizes, or the
// viewport changes.
//
// Docked windows must follow the viewport, which is why this is wired to the
// window resize event. A maximized window in WinBox.js does NOT follow it — it
// is sized once when maximized and left there — and that stays true here for
// undocked windows, because it is what the original does. A maximized window is
// re-fitted only when docks are in play, where leaving it stale would mean a
// dock and a maximized window overlapping after any resize.
func updateDocks() {
	for _, d := range stackDock {
		if d.dock == EdgeNone || d.Hidden || d.Min {
			continue
		}
		bx, by, bw, bh := dockBoxBefore(d)
		t := d.dockThickIn(bw, bh)

		switch d.dock {
		case EdgeTop:
			d.resizeRaw(bw, t)
			d.moveRaw(bx, by)
		case EdgeBottom:
			d.resizeRaw(bw, t)
			d.moveRaw(bx, by+bh-t)
		case EdgeLeft:
			d.resizeRaw(t, bh)
			d.moveRaw(bx, by)
		case EdgeRight:
			d.resizeRaw(t, bh)
			d.moveRaw(bx+bw-t, by)
		case EdgeNone:
		}
	}

	if len(stackDock) > 0 {
		x, y, w, h := dockContentBox()
		for _, win := range stackWin {
			if win.Max && win.dock == EdgeNone && !win.Hidden && !win.Min {
				win.resizeRaw(w-win.Left-win.Right, h-win.Top-win.Bottom)
				win.moveRaw(x+win.Left, y+win.Top)
			}
		}
	}

	updateMinStack()
}

// dockHandleFor is the one resize handle a docked window keeps: the one on the
// edge facing the content, which drags the dock thicker or thinner. The other
// seven would drag it off its edge, and are inert (in CSS as well, so the
// cursor does not promise something that will not happen).
func dockHandleFor(e Edge) string {
	switch e {
	case EdgeTop:
		return "s"
	case EdgeBottom:
		return "n"
	case EdgeLeft:
		return "e"
	case EdgeRight:
		return "w"
	}
	return ""
}
