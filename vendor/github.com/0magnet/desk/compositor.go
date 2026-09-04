package desk

// This file carries no build tag — the package doc is in desk.go, which is
// js/wasm-only — because none of it touches the DOM, and that is the point.
// Everything the compositor decides (what can be drawn, in what order, where
// each quad lands) is settled here, where `go test` can reach it. Node has
// neither canvas nor WebGL, so the drawing half in compositor_js.go cannot be
// tested at all; keeping it down to "bind this, upload that" is the only way
// any of this gets covered.
//
// Why a WebGL path exists at all, and why it looks like this.
//
// A pane is a DOM subtree, and the DOM cannot be sampled into a WebGL texture:
// there is no drawImage(element), and the SVG foreignObject trick is
// tainted-canvas-ridden, asynchronous and does not carry live content. So
// compositing arbitrary panes in WebGL would mean reimplementing layout, text
// and input in GL — that is a UI toolkit, and it is not what this package is.
//
// But some panes already render into a canvas. An xterm-go terminal with
// EnableWebGL() owns one, and every glyph it shows is in it. A canvas IS a
// texture source: texImage2D takes an HTMLCanvasElement directly, no readback,
// no copy through JS. So the tractable design is to let a pane say "I am a
// canvas" — the TexturePane interface in desk.go — and draw those panes as
// textured quads, with the window frame as flat geometry around them.
//
// The DOM windows stay where they are underneath, at opacity 0. They keep
// handling drags, resizes, focus and keyboard, so the compositor is a repaint
// and never an input path; turning it off is one style property per window.
// That is also the fallback: anything the compositor cannot draw is simply left
// visible, and a window that has nothing to be drawn from is never blanked.
//
// The GL canvas is a single layer above every window and below the panel. That
// one fact decides the drawing rule below: a composited window paints over all
// DOM windows regardless of its own z-index, so the composited set has to be
// the TOP of the stack, unbroken. See planFrame.

// rect is a rectangle in CSS pixels. Which origin depends on where it came
// from: window rectangles arrive in viewport coordinates, because that is what
// getBoundingClientRect reports and winbox windows are position:fixed, and
// planFrame translates them into desk-local coordinates, which is what the GL
// canvas covers.
type rect struct {
	X, Y, W, H float64
}

func (r rect) empty() bool { return r.W <= 0 || r.H <= 0 }

// defaultHeader is winbox's title bar height, used when a window does not
// report its own. Kept as a constant rather than measured: the compositor draws
// the bar as a plain quad, so being a pixel out is a cosmetic error, while
// measuring it per frame is a forced layout per window.
const defaultHeader = 35

// winState is everything the compositor needs to know about one window. It is
// deliberately plain data — no js.Value — so the decisions below can be made
// and tested without a browser.
type winState struct {
	ID   string
	Rect rect // viewport coordinates, as the DOM reports them
	Z    int  // winbox's z-index; larger is nearer the front

	Header, Border float64 // window chrome, zero meaning winbox's defaults

	Focused bool
	Hidden  bool // minimized, or hidden outright: draws nothing either way

	// TexW and TexH are the pane canvas's backing store size. Only their ratio
	// is used, so it does not matter that they are device pixels while
	// everything else here is CSS pixels.
	//
	// Zero on either axis means there is nothing to sample: either the pane is
	// not canvas-backed at all, or its canvas has not been sized yet. Both
	// cases are handled the same way, which is why they are not distinguished:
	// the window falls back to the DOM for as long as it stays that way, and
	// starts being composited on the first frame where it does not.
	TexW, TexH float64
}

// drawable reports whether this window can be drawn as a textured quad.
func (w winState) drawable() bool {
	return !w.Hidden && !w.Rect.empty() && w.TexW > 0 && w.TexH > 0
}

// quadDraw is one window's worth of work, in desk-local CSS pixels.
type quadDraw struct {
	ID      string
	Frame   rect // the whole window: drawn as a solid quad, the border and body
	Title   rect // the title bar, a second solid quad in a different color
	Body    rect // where the pane's texture goes
	Focused bool
}

// framePlan is what to draw this frame, and what to leave to the DOM.
type framePlan struct {
	// Draws is back to front. There is no depth buffer and the quads are
	// blended, so the order is the only thing keeping overlapping windows
	// looking stacked.
	Draws []quadDraw

	// DOM names the windows that must stay visible as DOM this frame. It is
	// not "everything not in Draws" as far as the caller is concerned — it is
	// the list to unhide, and a window that has never been hidden appearing in
	// it is harmless.
	DOM []string
}

// planFrame decides what the compositor draws. view is the desk's own rectangle
// in viewport coordinates: window positions are viewport-relative and the GL
// canvas only covers the desk, so everything is shifted by it.
//
// The rule is "the topmost unbroken run of drawable windows". The GL canvas is
// one layer above every DOM window, so a composited window paints over a DOM
// one whatever their z-indexes say. Compositing a window with a DOM window
// above it would therefore put the wrong one in front — visibly, and worst for
// the window the user just clicked on. Stopping at the first window that cannot
// be drawn keeps the stack honest at the cost of compositing less, which is the
// right way round: too little compositing looks like nothing happened, too much
// looks broken.
//
// Hidden windows are skipped rather than treated as a break. A minimized window
// paints nothing, so it cannot get the order wrong.
func planFrame(view rect, ws []winState) framePlan {
	sorted := sortByZ(ws)

	// Walk down from the front. cut is the index of the first window that is
	// composited; everything below it stays on the DOM path.
	cut := len(sorted)
	for i := len(sorted) - 1; i >= 0; i-- {
		w := sorted[i]
		if w.Hidden {
			continue // paints nothing, so it cannot break the run
		}
		if !w.drawable() {
			break
		}
		cut = i
	}

	plan := framePlan{}
	for i, w := range sorted {
		// The Hidden test is not redundant with cut: a hidden window inside the
		// run was stepped over above rather than counted into it, and it has to
		// come back here so that whatever winbox is doing with a minimized
		// window keeps happening.
		if i < cut || w.Hidden || !w.drawable() {
			plan.DOM = append(plan.DOM, w.ID)
			continue
		}
		plan.Draws = append(plan.Draws, drawFor(view, w))
	}
	return plan
}

// drawFor turns one window into quads.
func drawFor(view rect, w winState) quadDraw {
	frame := rect{X: w.Rect.X - view.X, Y: w.Rect.Y - view.Y, W: w.Rect.W, H: w.Rect.H}
	header := w.Header
	if header <= 0 {
		header = defaultHeader
	}
	if header > frame.H {
		header = frame.H
	}
	return quadDraw{
		ID:      w.ID,
		Frame:   frame,
		Title:   rect{X: frame.X, Y: frame.Y, W: frame.W, H: header},
		Body:    fitRect(contentBox(frame, header, w.Border), w.TexW, w.TexH),
		Focused: w.Focused,
	}
}

// sortByZ orders windows back to front. Insertion sort, and stable: the window
// count here is single digits, and equal z-indexes have to keep the order they
// were tracked in or two windows that have never been focused swap places from
// frame to frame and flicker.
func sortByZ(ws []winState) []winState {
	out := make([]winState, len(ws))
	copy(out, ws)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Z > out[j].Z; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// contentBox is the window rectangle minus its chrome — where a pane's element
// sits, and so where its texture goes.
func contentBox(r rect, header, border float64) rect {
	c := rect{
		X: r.X + border,
		Y: r.Y + header,
		W: r.W - 2*border,
		H: r.H - header - border,
	}
	if c.W < 0 {
		c.W = 0
	}
	if c.H < 0 {
		c.H = 0
	}
	return c
}

// fitRect centers a texture in dst without stretching it.
//
// Letterboxing rather than filling because the texture is usually a terminal:
// stretching it by the few pixels the canvas lags the window by during a drag
// resize makes every glyph in it shimmer, which is far more obvious than a
// couple of empty pixels at the edge. A texture of unknown size gets dst
// unchanged, since there is no aspect to preserve.
func fitRect(dst rect, texW, texH float64) rect {
	if dst.empty() || texW <= 0 || texH <= 0 {
		return dst
	}
	scale := dst.W / texW
	if s := dst.H / texH; s < scale {
		scale = s
	}
	w, h := texW*scale, texH*scale
	return rect{
		X: dst.X + (dst.W-w)/2,
		Y: dst.Y + (dst.H-h)/2,
		W: w,
		H: h,
	}
}

// clipRect converts a desk-pixel rectangle into the clip-space rectangle the
// vertex shader interpolates across: x0,y0 is the top left corner and x1,y1 the
// bottom right. Clip space is -1..1 with y up, desk pixels are 0..size with y
// down, hence the flip on y and not on x.
//
// float32 because that is what a uniform takes, and because doing the divide in
// float64 first keeps a 4096-pixel desk from losing a pixel to rounding.
func clipRect(r rect, viewW, viewH float64) [4]float32 {
	if viewW <= 0 || viewH <= 0 {
		return [4]float32{}
	}
	return [4]float32{
		float32(r.X/viewW*2 - 1),
		float32(1 - r.Y/viewH*2),
		float32((r.X+r.W)/viewW*2 - 1),
		float32(1 - (r.Y+r.H)/viewH*2),
	}
}
