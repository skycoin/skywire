//go:build js && wasm

// Package main pkg/tpviz/wasmgl/overlay.go
// The group-boundary overlay: the circles and labels drawn around country / IP
// groups when grouping is on.
//
// This is the one thing in the view that reads graph state on every animation
// frame — each circle's centre and radius have to be reprojected through the
// current view transform so the rings stay glued to the points during pan and
// zoom. In the TypeScript view that is a handful of calls into the graph per
// circle per frame, which is free when the graph is JavaScript and is a wasm
// boundary crossing when it is not. Keeping the loop here means the projection
// and the drawing happen on the same side.
package main

import (
	"math"
	"strconv"
	"syscall/js"
)

// spaceSize is cosmos's coordinate space; it must match Config.SpaceSize.
const spaceSize = 8192

// boundaryNodePad pads each ring by roughly a point radius so fixed-size dots
// sitting on the edge of a group stay inside its circle.
const boundaryNodePad = 6.0

// boundary is one group circle in graph space.
type boundary struct {
	x, y, r float64
	color   string
	label   string
}

// overlay draws the boundaries onto a 2D canvas stacked over the graph.
type overlay struct {
	view   *glView
	canvas js.Value
	ctx    js.Value

	circles []boundary

	rafID   js.Value
	tick    js.Func
	running bool

	// lastKey is the view transform the last drawn frame was drawn at. The
	// grouped layout is static, so when the transform has not moved there is
	// nothing to redraw and the frame can be skipped entirely.
	lastKey string
	haveKey bool
}

func newOverlay(v *glView) *overlay {
	o := &overlay{view: v}
	o.tick = js.FuncOf(func(js.Value, []js.Value) interface{} {
		o.frame()
		return nil
	})
	v.funcs = append(v.funcs, o.tick)
	return o
}

// ensureCanvas creates the overlay canvas on first use, on top of the graph's
// own canvas and transparent to the pointer so it never eats an event.
func (o *overlay) ensureCanvas() bool {
	if o.canvas.Truthy() {
		return true
	}
	doc := js.Global().Get("document")
	c := doc.Call("createElement", "canvas")
	c.Set("id", "tpviz-gl-boundaries")
	c.Get("style").Set("cssText",
		"position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none;z-index:5")
	o.view.container.Call("appendChild", c)
	o.canvas = c
	o.ctx = c.Call("getContext", "2d")
	return o.ctx.Truthy()
}

// setBoundaries replaces the circles and starts or stops the loop to match.
func (o *overlay) setBoundaries(v js.Value) {
	o.circles = o.circles[:0]
	if v.Truthy() {
		for i := 0; i < v.Length(); i++ {
			b := v.Index(i)
			o.circles = append(o.circles, boundary{
				x:     b.Get("x").Float(),
				y:     b.Get("y").Float(),
				r:     b.Get("r").Float(),
				color: b.Get("color").String(),
				label: b.Get("label").String(),
			})
		}
	}
	o.haveKey = false
	if len(o.circles) == 0 {
		o.stop()
		o.clear()
		return
	}
	o.start()
}

func (o *overlay) start() {
	if o.running || len(o.circles) == 0 {
		return
	}
	o.running = true
	o.rafID = js.Global().Call("requestAnimationFrame", o.tick)
}

func (o *overlay) stop() {
	if !o.running {
		return
	}
	o.running = false
	if o.rafID.Truthy() {
		js.Global().Call("cancelAnimationFrame", o.rafID)
		o.rafID = js.Undefined()
	}
}

func (o *overlay) clear() {
	if !o.ctx.Truthy() {
		return
	}
	o.ctx.Call("clearRect", 0, 0, o.canvas.Get("width").Float(), o.canvas.Get("height").Float())
}

// frame draws one animation frame and schedules the next.
func (o *overlay) frame() {
	if !o.running {
		return
	}
	defer func() {
		if o.running {
			o.rafID = js.Global().Call("requestAnimationFrame", o.tick)
		}
	}()

	if o.view == nil || o.view.graph == nil || !o.ensureCanvas() {
		return
	}

	// Idle-skip: if the view transform has not changed since the last drawn
	// frame, there is nothing to redraw.
	origin := o.view.graph.SpaceToScreenPosition([2]float64{0, 0})
	key := formatKey(o.view.graph.GetZoomLevel(), origin[0], origin[1])
	if o.haveKey && key == o.lastKey {
		return
	}
	o.lastKey, o.haveKey = key, true

	rect := o.canvas.Call("getBoundingClientRect")
	cssW, cssH := rect.Get("width").Float(), rect.Get("height").Float()
	dpr := js.Global().Get("devicePixelRatio").Float()
	if dpr <= 0 {
		dpr = 1
	}
	w, h := math.Max(1, math.Round(cssW*dpr)), math.Max(1, math.Round(cssH*dpr))
	if o.canvas.Get("width").Float() != w || o.canvas.Get("height").Float() != h {
		o.canvas.Set("width", w)
		o.canvas.Set("height", h)
	}

	ctx := o.ctx
	ctx.Call("setTransform", dpr, 0, 0, dpr, 0, 0)
	ctx.Call("clearRect", 0, 0, cssW, cssH)
	ctx.Set("textAlign", "center")
	ctx.Set("font", "13px system-ui, sans-serif")

	for _, b := range o.circles {
		c := o.view.graph.SpaceToScreenPosition([2]float64{b.x, b.y})
		// Derive the screen radius from the projection of a point on the
		// circle's edge rather than from SpaceToScreenRadius: in cosmos 1.x
		// that returned a value about three times smaller than the point
		// rendering scale, so the ring came out far too small and its own
		// points fell outside it. Projecting the edge uses exactly the same
		// transform as the points, at any zoom.
		e := o.view.graph.SpaceToScreenPosition([2]float64{b.x + b.r, b.y})
		sr := math.Hypot(e[0]-c[0], e[1]-c[1]) + boundaryNodePad
		if math.IsInf(c[0], 0) || math.IsNaN(c[0]) || math.IsInf(c[1], 0) || math.IsNaN(c[1]) || sr < 2 {
			continue
		}
		ctx.Call("beginPath")
		ctx.Call("arc", c[0], c[1], sr, 0, 2*math.Pi)
		ctx.Set("globalAlpha", 0.07)
		ctx.Set("fillStyle", b.color)
		ctx.Call("fill")
		ctx.Set("globalAlpha", 0.55)
		ctx.Set("lineWidth", 1.2)
		ctx.Set("strokeStyle", b.color)
		ctx.Call("stroke")
		if b.label != "" {
			ctx.Set("globalAlpha", 0.9)
			ctx.Set("fillStyle", b.color)
			ctx.Call("fillText", b.label, c[0], c[1]-sr-6)
		}
	}
	ctx.Set("globalAlpha", 1)
}

func (o *overlay) destroy() {
	o.stop()
	if o.canvas.Truthy() {
		if parent := o.canvas.Get("parentNode"); parent.Truthy() {
			parent.Call("removeChild", o.canvas)
		}
		o.canvas, o.ctx = js.Undefined(), js.Undefined()
	}
	o.circles = nil
}

// formatKey renders the view transform as the idle-skip key. Rounding the
// origin to whole pixels means a sub-pixel drift does not force a redraw.
func formatKey(zoom, x, y float64) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', 6, 64) }
	return f(zoom) + ":" + f(math.Round(x)) + ":" + f(math.Round(y))
}

// seedPositions places points near the centre of the space. cosmos 2.x uses
// the positions it is given as-is, where 1.x randomised missing ones; without
// this a free layout starts every point at the origin with nothing to push
// apart.
func seedPositions(n int) []float32 {
	out := make([]float32, 0, n*2)
	// A cheap deterministic scatter: no randomness needed beyond breaking the
	// symmetry the simulation then resolves, and determinism keeps successive
	// runs comparable.
	const span = 0.1
	s := uint32(0x9e3779b9)
	next := func() float32 {
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		return float32(spaceSize) * (0.45 + span*float32(s%1000)/1000.0)
	}
	for i := 0; i < n; i++ {
		out = append(out, next(), next())
	}
	return out
}
