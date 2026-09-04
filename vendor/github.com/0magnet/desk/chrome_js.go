//go:build js && wasm

package desk

import (
	"fmt"
	"syscall/js"
)

// Window chrome, drawn rather than sampled.
//
// The compositor normally leaves the frame alone: the title, the buttons and
// the border are DOM, DOM renders text better than anything here will, and
// hiding only the pane keeps all of it. That is the right arrangement when the
// desk is on a page.
//
// It is the wrong one when something wants the compositor's canvas AS A
// TEXTURE. A texture of a desk whose frames are still DOM is a texture of
// windows with no title bars — the frames are simply not in the canvas. And
// they cannot be put there by sampling, because sampling DOM is the thing that
// is not possible; that is the whole reason the compositor exists.
//
// So they are REDRAWN. A title bar is a filled rectangle, a string, and three
// or four small glyphs, all of which Canvas2D draws directly — and a canvas is
// a texture source. Nothing is read back out of the DOM except the title text
// and whether the window has focus.
//
// This is a reproduction of winbox's chrome and not the chrome itself, so the
// two can drift. That is the honest cost of the arrangement, and it is why
// DrawChrome is off unless a caller asks: a page that is only compositing for
// speed keeps the real thing.

// chromeKey is what a rasterized title bar depends on. Anything not in here can
// change without the cache being wrong.
type chromeKey struct {
	title   string
	focused bool
	w, h    int // device pixels
}

// chromeCache holds one rasterized title bar per window.
type chromeCache struct {
	canvas js.Value
	key    chromeKey
}

// The colors the chrome is drawn in. They match winbox's stylesheet closely
// enough that switching DrawChrome on does not restyle the desk; exactly is not
// possible, since one is CSS and the other is a canvas.
const (
	chromeBarIdle    = "#232936"
	chromeBarFocus   = "#2f3949"
	chromeText       = "#d3d7cf"
	chromeTextIdle   = "#9aa4b2"
	chromeButton     = "#c9d0d8"
	chromeBodyBase   = "#1b1f27"
	chromeBorderTint = "#39414f"
)

// chromeFor returns a canvas holding this window's title bar, rasterizing it
// only when something visible about it has changed.
//
// The cache is the difference between drawing text once and drawing it sixty
// times a second. A title bar changes when the window is renamed, focused,
// unfocused or resized, and none of those happen per frame.
func (c *compositor) chromeFor(id, title string, focused bool, r rect) js.Value {
	w := int(r.W*c.dpr + 0.5)
	h := int(r.H*c.dpr + 0.5)
	if w <= 0 || h <= 0 {
		return js.Value{}
	}
	key := chromeKey{title: title, focused: focused, w: w, h: h}
	if got, ok := c.chrome[id]; ok && got.key == key && got.canvas.Truthy() {
		return got.canvas
	}

	doc := js.Global().Get("document")
	cv := doc.Call("createElement", "canvas")
	cv.Set("width", w)
	cv.Set("height", h)
	ctx := cv.Call("getContext", "2d")
	if !ctx.Truthy() {
		return js.Value{}
	}
	drawChromeBar(ctx, float64(w), float64(h), c.dpr, title, focused)
	c.chrome[id] = chromeCache{canvas: cv, key: key}
	return cv
}

// drawChromeBar paints one title bar. Kept apart from the caching so the
// drawing can be read as drawing.
func drawChromeBar(ctx js.Value, w, h, dpr float64, title string, focused bool) {
	bar := chromeBarIdle
	text := chromeTextIdle
	if focused {
		bar, text = chromeBarFocus, chromeText
	}
	ctx.Set("fillStyle", bar)
	ctx.Call("fillRect", 0, 0, w, h)

	// A hairline under the bar, which is what separates it from the body when
	// both are dark.
	ctx.Set("fillStyle", chromeBorderTint)
	ctx.Call("fillRect", 0, h-dpr, w, dpr)

	ctx.Set("fillStyle", text)
	ctx.Set("font", fmt.Sprintf("%.0fpx ui-monospace, SFMono-Regular, Menlo, monospace", 13*dpr))
	ctx.Set("textBaseline", "middle")
	// The title is clipped to what is left after the buttons, rather than
	// being allowed to run under them.
	ctx.Call("save")
	ctx.Call("beginPath")
	ctx.Call("rect", 10*dpr, 0, w-buttonRoom(dpr)-14*dpr, h)
	ctx.Call("clip")
	ctx.Call("fillText", title, 10*dpr, h/2)
	ctx.Call("restore")

	drawChromeButtons(ctx, w, h, dpr, focused)
}

// buttonRoom is how much of the right-hand end the buttons occupy.
func buttonRoom(dpr float64) float64 { return 84 * dpr }

// drawChromeButtons draws minimize, maximize and close as glyphs.
//
// Drawn rather than lettered: winbox's are background images, and three
// strokes are both closer to them and cheaper than loading anything.
func drawChromeButtons(ctx js.Value, w, h, dpr float64, focused bool) {
	col := chromeButton
	if !focused {
		col = chromeTextIdle
	}
	ctx.Set("strokeStyle", col)
	ctx.Set("lineWidth", 1.3*dpr)
	ctx.Set("lineCap", "round")

	cy := h / 2
	size := 5.0 * dpr
	// Right to left: close, maximize, minimize, on a 26px pitch.
	x := w - 16*dpr

	// close: a cross
	ctx.Call("beginPath")
	ctx.Call("moveTo", x-size, cy-size)
	ctx.Call("lineTo", x+size, cy+size)
	ctx.Call("moveTo", x+size, cy-size)
	ctx.Call("lineTo", x-size, cy+size)
	ctx.Call("stroke")

	// maximize: a square
	x -= 26 * dpr
	ctx.Call("strokeRect", x-size, cy-size, 2*size, 2*size)

	// minimize: a rule
	x -= 26 * dpr
	ctx.Call("beginPath")
	ctx.Call("moveTo", x-size, cy+size)
	ctx.Call("lineTo", x+size, cy+size)
	ctx.Call("stroke")
}

// dropChrome forgets a closed window's rasterized bar, for the same reason
// dropTexture forgets its pane: a desk that has opened and closed a hundred
// windows should not be holding a hundred canvases.
func (c *compositor) dropChrome(id string) { delete(c.chrome, id) }
