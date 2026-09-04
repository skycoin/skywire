//go:build js && wasm

package desk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"syscall/js"

	winbox "github.com/0magnet/winbox-go"
)

// The drawing half. Everything that decides anything is in compositor.go, which
// has no build tag and is tested; what is left here is binding, uploading and
// the fallback path, none of which Node can execute — it has no canvas, let
// alone WebGL. Read compositor.go first: it says why any of this exists.

// WebGL2 constants. Spelled out rather than read back from the context, which
// is what every Go WebGL binding does — they are fixed by the spec.
const (
	glColorBufferBit   = 0x00004000
	glTriangleStrip    = 0x0005
	glArrayBuffer      = 0x8892
	glStaticDraw       = 0x88E4
	glFloat            = 0x1406
	glVertexShader     = 0x8B31
	glFragmentShader   = 0x8B30
	glCompileStatus    = 0x8B81
	glLinkStatus       = 0x8B82
	glTexture2D        = 0x0DE1
	glTexture0         = 0x84C0
	glRGBA             = 0x1908
	glUnsignedByte     = 0x1401
	glTextureMinFilter = 0x2801
	glTextureMagFilter = 0x2800
	glTextureWrapS     = 0x2802
	glTextureWrapT     = 0x2803
	glClampToEdge      = 0x812F
	glLinear           = 0x2601
	glBlend            = 0x0BE2
	glSrcAlpha         = 0x0302
	glOneMinusSrcAlpha = 0x0303
)

// One program draws everything. Today that is only textured pane quads — the
// window chrome stays DOM, because a quad cannot carry a title or a close
// button — but u_textured keeps the untextured branch, since a shader whose
// uniform is never exercised is one that breaks quietly for whoever adds the
// next draw call.
//
// The vertex buffer is a unit quad and never changes: the rectangle arrives as
// a uniform in clip space, already converted by clipRect, which is why that
// conversion could be a plain function with a test rather than a shader.
const compositorVert = `#version 300 es
layout (location = 0) in vec2 a_unit;

uniform vec4 u_clip; // x0,y0 top left and x1,y1 bottom right, in clip space

out vec2 v_uv;

void main() {
  gl_Position = vec4(mix(u_clip.xy, u_clip.zw, a_unit), 0.0, 1.0);
  v_uv = a_unit;
}`

// v_uv is a_unit unflipped. texImage2D leaves UNPACK_FLIP_Y_WEBGL alone here,
// so the canvas's top row is at t=0, and a_unit.y is 0 at the top of the quad:
// the two agree. Flipping either one is the classic way to get an upside-down
// terminal, and it can only be confirmed in a browser.
const compositorFrag = `#version 300 es
precision mediump float;

in vec2 v_uv;

uniform sampler2D u_tex;
uniform vec4 u_color;
uniform bool u_textured;

out vec4 outColor;

void main() {
  outColor = u_textured ? texture(u_tex, v_uv) : u_color;
}`

// liveWindow is a launched window, tracked whether or not compositing is on.
//
// Every window is tracked, not only the canvas-backed ones, because the drawing
// order depends on the ones that cannot be drawn: see planFrame. Tracking costs
// an append and a delete per window when compositing is off, which is why it is
// unconditional rather than something EnableCompositing has to retrofit onto
// windows that already exist.
type liveWindow struct {
	id     string
	win    *winbox.WinBox
	pane   Pane
	hidden bool // we have set opacity 0 on it, and owe it a restore
}

var (
	windows []*liveWindow
	windowN int
	comp    *compositor
)

// trackWindow records a launched window. Called from LaunchOpts.
func trackWindow(win *winbox.WinBox, pane Pane) *liveWindow {
	mu.Lock()
	defer mu.Unlock()
	windowN++
	lw := &liveWindow{id: fmt.Sprintf("w%d", windowN), win: win, pane: pane}
	windows = append(windows, lw)
	return lw
}

// untrackWindow forgets a window that has closed, and restores whatever the
// compositor did to it — a window that closes while hidden for GL leaves its
// opacity behind otherwise, which matters if anything else is holding the DOM.
func untrackWindow(lw *liveWindow) {
	mu.Lock()
	out := windows[:0]
	for _, w := range windows {
		if w != lw {
			out = append(out, w)
		}
	}
	windows = out
	c := comp
	mu.Unlock()

	lw.show()
	if c != nil {
		c.dropTexture(lw.id)
		c.dropTexture(lw.id + ":chrome")
		c.dropChrome(lw.id)
	}
}

func liveWindows() []*liveWindow {
	mu.Lock()
	defer mu.Unlock()
	out := make([]*liveWindow, len(windows))
	copy(out, windows)
	return out
}

func (lw *liveWindow) show() {
	if !lw.hidden {
		return
	}
	lw.hidden = false
	if el := lw.hideTarget(); el.Truthy() {
		el.Get("style").Set("opacity", "")
	}
}

// hideTarget is the element the compositor makes invisible, and it is the
// window's BODY rather than the window.
//
// This was the whole window at first, and in a browser the result was a
// terminal floating with no title bar, no close button and no border: the
// compositor draws a pane's canvas at the content box, so hiding the frame
// hides chrome that nothing then redraws. The frame is DOM — a title, some
// buttons, a border — and DOM is exactly what cannot be sampled into a
// texture, so drawing it was never on the table.
//
// Hiding only the body is also the more honest split. What the compositor took
// over is the pane; the window around it was never its business.
func (lw *liveWindow) hideTarget() js.Value {
	if lw.win == nil || !lw.win.DOM.Truthy() {
		return js.Value{}
	}
	// With the chrome drawn into the canvas, the WHOLE window is redundant on
	// the DOM and hiding only the body would leave a real title bar sitting on
	// top of a drawn one.
	mu.Lock()
	whole := comp != nil && comp.drawChrome
	mu.Unlock()
	if whole {
		return lw.win.DOM
	}
	// Checked rather than assumed, because DOM is whatever the caller put
	// there — winbox's element in a browser, and a plain object in a test.
	if qs := lw.win.DOM.Get("querySelector"); qs.Type() == js.TypeFunction {
		if body := lw.win.DOM.Call("querySelector", ".wb-body"); body.Truthy() {
			return body
		}
	}
	return lw.win.DOM
}

// hide makes the DOM window invisible without taking it out of the page.
//
// opacity rather than visibility or display: an element at opacity 0 still hit
// tests, so dragging, resizing, focus and the keyboard all keep working exactly
// as they did, and the compositor never has to reimplement any of it. It is
// also one property to put back, which is what makes the fallback cheap enough
// to take on any frame that goes wrong.
func (lw *liveWindow) hide() {
	if lw.hidden {
		return
	}
	lw.hidden = true
	if el := lw.hideTarget(); el.Truthy() {
		el.Get("style").Set("opacity", "0")
	}
}

// compositor draws the canvas-backed windows into one WebGL2 canvas.
type compositor struct {
	canvas js.Value
	gl     js.Value
	prog   js.Value

	uClip, uColor, uTextured, uTex js.Value

	textures map[string]js.Value
	chrome   map[string]chromeCache

	// drawChrome redraws window frames into the canvas instead of leaving
	// them to the DOM. See chrome_js.go: it exists so the canvas can be a
	// complete picture of the desk, which is what a caller texturing it needs.
	drawChrome bool
	background [4]float32

	cssW, cssH float64
	dpr        float64
	running    bool
}

// The requestAnimationFrame callback is created once for the page and reused by
// every compositor. A js.Func cannot be released from inside its own call —
// that is a "call to released function" panic on the next tick — and a
// compositor that fails mid-frame has to be able to stop itself, so the func
// outlives the compositor rather than belonging to it.
var (
	rafFunc  js.Func
	rafReady bool
)

// EnableCompositing switches the desk to the WebGL path, and reports why it
// could not if it could not.
//
// It is off by default and every caller may ignore the error: failing here
// leaves the desk exactly as it was, which is a working DOM desktop. There is
// nothing to undo because nothing has been changed yet.
// CompositingOptions tunes what the compositor takes over.
type CompositingOptions struct {
	// DrawChrome redraws window frames into the GL canvas — see chrome_js.go.
	//
	// Off by default, and that default is the right one for a desk on a page:
	// the DOM draws text better than a canvas will, and hiding only the pane
	// keeps the real title bars, the real buttons and their hover states.
	//
	// Turn it on when the CANVAS is the product rather than the page — when
	// something is going to sample it as a texture, where a frame left on the
	// DOM is a frame that is simply not in the picture.
	DrawChrome bool

	// Background fills the desktop, instead of leaving it transparent.
	//
	// Transparent is right for a compositor that overlays a page: what is
	// behind the windows is the page, and clearing to a color would hide it.
	// It is wrong for a canvas that is about to become a texture, and wrong in
	// a way that is confusing rather than ugly — a desk with no background is
	// a window floating in nothing, so when the quad turns there is no visible
	// surface for it to turn ON. The windows appear to pivot about an axis
	// that is not through them, which is exactly what they are doing: the axis
	// is through the middle of the DESK, and without a background the desk is
	// invisible.
	//
	// RGBA, straight rather than premultiplied, in 0..1. The zero value leaves
	// it transparent.
	Background [4]float32
}

// Canvas is the compositor's canvas, or a zero Value when compositing is off.
//
// It is exported for one purpose: a caller that wants to draw the desk itself,
// by sampling this as a texture. Everything needed for that to be a complete
// desk is behind CompositingOptions.DrawChrome.
func Canvas() js.Value {
	mu.Lock()
	defer mu.Unlock()
	if comp == nil {
		return js.Value{}
	}
	return comp.canvas
}

// EnableCompositing switches the desk to the WebGL path with the defaults.
func EnableCompositing() error { return EnableCompositingOpts(CompositingOptions{}) }

// EnableCompositingOpts is EnableCompositing with the knobs.
func EnableCompositingOpts(opt CompositingOptions) error {
	mu.Lock()
	already := comp != nil
	restore := false
	if already {
		// Already running: honor a change of options rather than ignoring
		// it, since the caller that flips DrawChrome is usually the one that
		// just decided to texture the canvas.
		comp.drawChrome = opt.DrawChrome
		comp.background = opt.Background
		// The drawn copy is about to stop being drawn, so the real one has to
		// come back — otherwise the desk keeps a panel that is present,
		// clickable and invisible. NOTED HERE, DONE BELOW: see the unlock.
		restore = !opt.DrawChrome
	}
	mu.Unlock()
	if already {
		// OUTSIDE THE LOCK, and it has to be. restorePanelDOM reaches
		// rootElement, which takes this same mutex to read the root — and a Go
		// mutex is not reentrant, so calling it from in here deadlocked the
		// goroutine against itself. On wasm that is the ONE thread, so the page
		// stopped: no panic, no console output, nothing on stderr, just a tab
		// that never paints again.
		//
		// It fired on exactly one transition — DrawChrome true to false while
		// compositing was already running — which is leaving a desk that was
		// being drawn as a texture. Every way out of that mode froze; going in
		// was fine, because that path sets DrawChrome true and never gets here.
		if restore {
			restorePanel()
		}
		return nil
	}

	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return errors.New("desk: no document to composite into")
	}

	canvas := doc.Call("createElement", "canvas")
	gl := canvas.Call("getContext", "webgl2", map[string]any{
		"alpha":              true,
		"antialias":          false,
		"depth":              false,
		"premultipliedAlpha": true,
		// The desk is drawn from scratch every frame, so nothing is gained by
		// keeping the last one, and on tiled GPUs keeping it costs a resolve.
		"preserveDrawingBuffer": false,
	})
	if !gl.Truthy() {
		return errors.New("desk: no webgl2 context; staying on the DOM path")
	}

	c := &compositor{canvas: canvas, gl: gl, textures: map[string]js.Value{},
		chrome: map[string]chromeCache{}, dpr: 1}
	if err := c.buildProgram(); err != nil {
		return err
	}

	// Above every window and below the panel. winbox hands out z-indexes from
	// 11 upwards and the panel sits at 100000; putting the GL layer between
	// them is what lets a composited window paint over a DOM one, and is the
	// reason planFrame composites the top of the stack rather than the bottom.
	st := canvas.Get("style")
	st.Set("position", "absolute")
	st.Set("left", "0")
	st.Set("top", "0")
	st.Set("width", "100%")
	st.Set("height", "100%")
	st.Set("z-index", "90000")
	st.Set("pointer-events", "none") // the DOM underneath is still the input layer
	rootElement().Call("appendChild", canvas)

	gl.Call("enable", glBlend)
	gl.Call("blendFunc", glSrcAlpha, glOneMinusSrcAlpha)

	c.drawChrome = opt.DrawChrome
	c.background = opt.Background
	c.running = true
	mu.Lock()
	comp = c
	mu.Unlock()

	if !rafReady {
		rafFunc = js.FuncOf(func(js.Value, []js.Value) any {
			mu.Lock()
			cur := comp
			mu.Unlock()
			if cur == nil || !cur.running {
				return nil
			}
			cur.frame()
			js.Global().Call("requestAnimationFrame", rafFunc)
			return nil
		})
		rafReady = true
	}
	js.Global().Call("requestAnimationFrame", rafFunc)
	return nil
}

// DisableCompositing puts every window back on the DOM path. Safe to call when
// compositing was never on.
// restorePanelDOM is called wherever the drawn copy stops being drawn.
func restorePanelDOM() { hidePanelDOM(false) }

// restorePanel is the indirection the deadlock test hooks. It exists so the
// rule that broke here — this must not be called while the package mutex is
// held — can be asserted in a test that FAILS rather than one that hangs: the
// real function reaches rootElement, which takes that mutex, so a test using it
// would reproduce the deadlock instead of reporting it.
var restorePanel = restorePanelDOM

func DisableCompositing() {
	restorePanelDOM()
	mu.Lock()
	c := comp
	comp = nil
	mu.Unlock()
	if c == nil {
		return
	}
	c.running = false
	for _, w := range liveWindows() {
		w.show()
	}
	// Dropping the canvas is enough for the browser to reclaim the context and
	// everything in it, but not until it collects; a desk that toggles
	// compositing while several terminals are open is holding a texture per
	// window until then, and those are megabytes each.
	for id := range c.textures {
		c.dropTexture(id)
	}
	if c.canvas.Truthy() {
		c.canvas.Call("remove")
	}
}

// Compositing reports whether the WebGL path is running.
func Compositing() bool {
	mu.Lock()
	defer mu.Unlock()
	return comp != nil
}

// fail gives up on WebGL for good and hands the desk back to the DOM.
//
// Every path into the compositor goes through frame's recover, so a shader that
// links but does not run, a context lost on a laptop waking up, or a pane
// handing back something that is not a canvas all end here rather than as a
// desktop full of invisible windows.
func (c *compositor) fail(reason string) {
	// Nothing in here may throw its way out. fail is called from inside frame's
	// recover, and a panic raised from a deferred function that has already
	// recovered is not recoverable again — it takes the page with it, which is
	// a spectacular way for a fallback path to end.
	defer func() {
		if r := recover(); r != nil {
			_ = r // dropped on purpose: there is nowhere left to report it
		}
	}()

	js.Global().Get("console").Call("warn", "desk: compositing off: "+reason)
	DisableCompositing()
}

// frame draws one frame. Its recover is the last line of the fallback: no
// matter what a js call throws, the windows come back.
func (c *compositor) frame() {
	defer func() {
		if r := recover(); r != nil {
			c.fail(fmt.Sprint(r))
		}
	}()

	if c.gl.Call("isContextLost").Bool() {
		c.fail("the webgl context was lost")
		return
	}

	dk := deskRect(rootElement())
	view := rect{X: dk.left, Y: dk.top, W: dk.w, H: dk.h}
	c.resize(view)

	live := liveWindows()
	states := make([]winState, 0, len(live))
	canvases := make(map[string]js.Value, len(live))
	titles := make(map[string]string, len(live))
	byID := make(map[string]*liveWindow, len(live))
	for _, lw := range live {
		st, canvas := lw.state()
		states = append(states, st)
		byID[st.ID] = lw
		if canvas.Truthy() {
			canvases[st.ID] = canvas
		}
		if c.drawChrome {
			titles[st.ID] = lw.title()
		}
	}

	plan := planFrame(view, states)

	// Restore before hiding. The other order leaves a frame where a window has
	// been hidden for GL but the window it replaced has not been shown yet, and
	// on the frame compositing gives up that is a visible flash of nothing.
	for _, id := range plan.DOM {
		if lw := byID[id]; lw != nil {
			lw.show()
		}
	}

	gl := c.gl
	bg := c.background
	gl.Call("clearColor", float64(bg[0]), float64(bg[1]), float64(bg[2]), float64(bg[3]))
	gl.Call("clear", glColorBufferBit)
	gl.Call("useProgram", c.prog)

	for _, d := range plan.Draws {
		lw := byID[d.ID]
		if lw == nil {
			continue
		}
		// THE CHROME. By default it is not drawn: the pane is pixels and
		// becomes a texture, the frame is text and controls and stays DOM,
		// which keeps real title bars and keeps them hit-testing. Painting it
		// as flat quads was the first attempt and it produced a terminal with
		// a blank bar where its title had been — a rectangle of color cannot
		// carry text.
		//
		// With DrawChrome it IS drawn, because a caller sampling this canvas
		// needs the frames to be in it, and it is drawn properly: a flat quad
		// for the background, and a title bar RASTERIZED with Canvas2D — text,
		// buttons and all — in chrome_js.go.
		if c.drawChrome {
			c.solid(d.Frame, colorBody)
			if bar := c.chromeFor(d.ID, titles[d.ID], d.Focused, d.Title); bar.Truthy() {
				c.textured(d.ID+":chrome", d.Title, bar)
			}
		}
		if !c.textured(d.ID, d.Body, canvases[d.ID]) {
			// The texture could not be uploaded after all. Leaving the window
			// on the DOM for this frame shows the pane over the chrome quad
			// already drawn, which is untidy for one frame and never blank.
			lw.show()
			continue
		}
		lw.hide()
	}

	// The panel LAST, so it is over the windows — which is what its z-index of
	// 100000 says on the page, and what a panel is for.
	if c.drawChrome {
		hidePanelDOM(true)
		snap := c.readPanel(view)
		if bar := c.panelChrome(snap); bar.Truthy() {
			c.textured(panelChromeID, snap.r, bar)
		}
	}
}

// state reads what planFrame needs out of one window.
//
// The rectangle comes from getBoundingClientRect rather than winbox's stored
// X, Y, Width and Height, because those are not the whole truth: moveRaw and
// resizeRaw — the splitscreen and snap paths — deliberately do not update them.
// The element's own box is right for every path, including the CSS transitions
// winbox animates maximizing with, and one forced layout per window per frame
// is affordable at this window count.
func (lw *liveWindow) state() (winState, js.Value) {
	st := winState{ID: lw.id}
	w := lw.win
	if w == nil || !w.DOM.Truthy() {
		st.Hidden = true
		return st, js.Value{}
	}
	st.Z = w.Index
	st.Header = w.Header
	st.Focused = w.Focused
	st.Hidden = w.Hidden || w.Min || !w.DOM.Get("isConnected").Bool()
	if st.Hidden {
		return st, js.Value{}
	}

	r := w.DOM.Call("getBoundingClientRect")
	st.Rect = rect{
		X: r.Get("left").Float(),
		Y: r.Get("top").Float(),
		W: r.Get("width").Float(),
		H: r.Get("height").Float(),
	}

	tp, ok := lw.pane.(TexturePane)
	if !ok {
		return st, js.Value{} // a DOM pane; it keeps the DOM path
	}
	canvas := tp.Canvas()
	if !canvas.Truthy() {
		return st, js.Value{} // canvas-backed, but not yet
	}
	st.TexW = canvas.Get("width").Float()
	st.TexH = canvas.Get("height").Float()
	return st, canvas
}

// resize matches the drawing buffer to the desk.
//
// Sized in device pixels and scaled back down in CSS, or a terminal's glyphs
// are resampled from a buffer smaller than the screen and the whole point of
// the terminal having a WebGL renderer is thrown away in the last step.
func (c *compositor) resize(view rect) {
	dpr := 1.0
	if v := js.Global().Get("devicePixelRatio"); v.Truthy() {
		dpr = v.Float()
	}
	if view.W == c.cssW && view.H == c.cssH && dpr == c.dpr {
		return
	}
	c.cssW, c.cssH, c.dpr = view.W, view.H, dpr
	w := int(math.Round(view.W * dpr))
	h := int(math.Round(view.H * dpr))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	c.canvas.Set("width", w)
	c.canvas.Set("height", h)
	c.gl.Call("viewport", 0, 0, w, h)
}

// solid draws one flat quad.
//
// It came back for DrawChrome, and for a narrower job than it had before: the
// window's own background, underneath a title bar that is rasterized and a pane
// that is a texture. A flat quad was never able to stand in for chrome — that
// was the bug — but it is exactly right for the color behind one.
func (c *compositor) solid(r rect, col [4]float32) {
	if r.empty() {
		return
	}
	q := clipRect(r, c.cssW, c.cssH)
	gl := c.gl
	gl.Call("uniform4f", c.uClip, float64(q[0]), float64(q[1]), float64(q[2]), float64(q[3]))
	gl.Call("uniform4f", c.uColor, float64(col[0]), float64(col[1]), float64(col[2]), float64(col[3]))
	gl.Call("uniform1i", c.uTextured, 0)
	gl.Call("drawArrays", glTriangleStrip, 0, 4)
}

// colorBody is the window background drawn under the chrome, matching the
// #1b1f27 LaunchOpts gives winbox.
var colorBody = [4]float32{0.106, 0.122, 0.153, 1}

// textured uploads a pane's canvas and draws it, and reports whether it got
// that far. The upload is unconditional and every frame: a terminal's canvas
// changes whenever anything is typed into it and there is no cheap way to ask
// whether it did, so this is the cost of the whole approach — one texImage2D
// per composited window per frame, GPU-side, with no readback.
func (c *compositor) textured(id string, r rect, canvas js.Value) bool {
	if r.empty() || !canvas.Truthy() {
		return false
	}
	gl := c.gl
	tex, ok := c.textures[id]
	if !ok {
		tex = gl.Call("createTexture")
		gl.Call("bindTexture", glTexture2D, tex)
		// LINEAR with no mipmaps and clamped wrapping: a window is drawn at
		// very near 1:1, and anything that needs a mipmap would need one built
		// every frame.
		gl.Call("texParameteri", glTexture2D, glTextureMinFilter, glLinear)
		gl.Call("texParameteri", glTexture2D, glTextureMagFilter, glLinear)
		gl.Call("texParameteri", glTexture2D, glTextureWrapS, glClampToEdge)
		gl.Call("texParameteri", glTexture2D, glTextureWrapT, glClampToEdge)
		c.textures[id] = tex
	} else {
		gl.Call("bindTexture", glTexture2D, tex)
	}
	gl.Call("texImage2D", glTexture2D, 0, glRGBA, glRGBA, glUnsignedByte, canvas)

	q := clipRect(r, c.cssW, c.cssH)
	gl.Call("activeTexture", glTexture0)
	gl.Call("uniform1i", c.uTex, 0)
	gl.Call("uniform1i", c.uTextured, 1)
	gl.Call("uniform4f", c.uClip, float64(q[0]), float64(q[1]), float64(q[2]), float64(q[3]))
	gl.Call("drawArrays", glTriangleStrip, 0, 4)
	return true
}

// dropTexture releases the texture a closed window was using. Without it a desk
// that has opened and closed a hundred terminals is holding a hundred textures
// the size of a window each, which is tens of megabytes of GPU memory that
// nothing will ever sample again.
func (c *compositor) dropTexture(id string) {
	tex, ok := c.textures[id]
	if !ok {
		return
	}
	delete(c.textures, id)
	if c.gl.Truthy() && !c.gl.Call("isContextLost").Bool() {
		c.gl.Call("deleteTexture", tex)
	}
}

// buildProgram compiles the one program and the one quad.
func (c *compositor) buildProgram() error {
	gl := c.gl
	vs, err := c.shader(glVertexShader, compositorVert)
	if err != nil {
		return err
	}
	fs, err := c.shader(glFragmentShader, compositorFrag)
	if err != nil {
		return err
	}
	prog := gl.Call("createProgram")
	gl.Call("attachShader", prog, vs)
	gl.Call("attachShader", prog, fs)
	gl.Call("linkProgram", prog)
	if !gl.Call("getProgramParameter", prog, glLinkStatus).Bool() {
		return fmt.Errorf("desk: linking the compositor program: %s",
			gl.Call("getProgramInfoLog", prog).String())
	}
	gl.Call("deleteShader", vs)
	gl.Call("deleteShader", fs)
	gl.Call("useProgram", prog)

	c.prog = prog
	c.uClip = gl.Call("getUniformLocation", prog, "u_clip")
	c.uColor = gl.Call("getUniformLocation", prog, "u_color")
	c.uTextured = gl.Call("getUniformLocation", prog, "u_textured")
	c.uTex = gl.Call("getUniformLocation", prog, "u_tex")

	// A unit quad as a triangle strip: top left, bottom left, top right,
	// bottom right. Uploaded once and never touched again — the rectangle to
	// draw arrives as a uniform.
	vao := gl.Call("createVertexArray")
	gl.Call("bindVertexArray", vao)
	buf := gl.Call("createBuffer")
	gl.Call("bindBuffer", glArrayBuffer, buf)
	gl.Call("bufferData", glArrayBuffer,
		float32Array([]float32{0, 0, 0, 1, 1, 0, 1, 1}), glStaticDraw)
	gl.Call("enableVertexAttribArray", 0)
	gl.Call("vertexAttribPointer", 0, 2, glFloat, false, 0, 0)
	return nil
}

func (c *compositor) shader(kind int, src string) (js.Value, error) {
	gl := c.gl
	sh := gl.Call("createShader", kind)
	gl.Call("shaderSource", sh, src)
	gl.Call("compileShader", sh)
	if !gl.Call("getShaderParameter", sh, glCompileStatus).Bool() {
		log := gl.Call("getShaderInfoLog", sh).String()
		gl.Call("deleteShader", sh)
		return js.Value{}, fmt.Errorf("desk: compiling the compositor shader: %s", log)
	}
	return sh, nil
}

// float32Array copies floats into a JS Float32Array. syscall/js can only copy
// bytes, so the floats are laid out by hand — the alternative, setting elements
// one at a time across the JS boundary, is a call per number.
func float32Array(vals []float32) js.Value {
	buf := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	u8 := js.Global().Get("Uint8Array").New(len(buf))
	js.CopyBytesToJS(u8, buf)
	return js.Global().Get("Float32Array").New(u8.Get("buffer"), u8.Get("byteOffset"), len(vals))
}
