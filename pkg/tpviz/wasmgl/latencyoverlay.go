//go:build js && wasm

// Package wasmgl pkg/tpviz/wasmgl/latencyoverlay.go c4-vis-latency
// Draws the spherical Voronoi cells over the latency view.
//
// It borrows the compositing approach of overlay.go, a transparent 2D
// canvas above the graph's own, redrawn only when the view transform
// moves. It does not borrow the geometry: those are independent circles
// around groups, while these are a tessellation, so cells share edges,
// cover the sphere and each belongs to exactly one visor. That means
// filled polygons, and it means a cell is only meaningful next to its
// neighbors, so the whole set is drawn or none of it is.
package wasmgl

import (
	"math"
	"syscall/js"
)

// latCell is one projected cell: a flat ring of graph-space coordinates
// and the color of the visor that owns it.
type latCell struct {
	site  int
	ring  []float64
	color string
}

type latOverlay struct {
	view   *glView
	canvas js.Value
	ctx    js.Value
	tick   js.Func

	cells   []latCell
	running bool
	rafID   js.Value

	lastKey string
	haveKey bool
}

func newLatOverlay(v *glView) *latOverlay {
	o := &latOverlay{view: v}
	o.tick = js.FuncOf(func(js.Value, []js.Value) interface{} {
		o.frame()
		return nil
	})
	v.funcs = append(v.funcs, o.tick)
	return o
}

func (o *latOverlay) ensureCanvas() bool {
	if o.canvas.Truthy() {
		return true
	}
	doc := js.Global().Get("document")
	c := doc.Call("createElement", "canvas")
	c.Set("id", "tpviz-gl-latency-cells")
	// Below the boundary overlay so a group ring stays readable on top of
	// a cell fill, and transparent to the pointer so neither eats events.
	c.Get("style").Set("cssText",
		"position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none;z-index:4")
	o.view.container.Call("appendChild", c)
	o.canvas = c
	o.ctx = c.Call("getContext", "2d")
	return o.ctx.Truthy()
}

// setCells replaces the tessellation. Input is the `cells` array from
// latencyState.project(), plus a parallel color per site.
func (o *latOverlay) setCells(v js.Value, colors js.Value) {
	o.cells = o.cells[:0]
	if v.Truthy() {
		for i := 0; i < v.Length(); i++ {
			c := v.Index(i)
			ringJS := c.Get("ring")
			ring := make([]float64, ringJS.Length())
			for j := range ring {
				ring[j] = ringJS.Index(j).Float()
			}
			site := c.Get("site").Int()
			col := "#4a9eff"
			if colors.Truthy() && site < colors.Length() {
				if s := colors.Index(site); s.Type() == js.TypeString {
					col = s.String()
				}
			}
			o.cells = append(o.cells, latCell{site: site, ring: ring, color: col})
		}
	}
	o.haveKey = false
	if len(o.cells) == 0 {
		o.stop()
		o.clear()
		return
	}
	o.start()
}

func (o *latOverlay) start() {
	if o.running || len(o.cells) == 0 {
		return
	}
	o.running = true
	o.rafID = js.Global().Call("requestAnimationFrame", o.tick)
}

func (o *latOverlay) stop() {
	if !o.running {
		return
	}
	o.running = false
	if o.rafID.Truthy() {
		js.Global().Call("cancelAnimationFrame", o.rafID)
		o.rafID = js.Undefined()
	}
}

func (o *latOverlay) clear() {
	if !o.ctx.Truthy() {
		return
	}
	o.ctx.Call("clearRect", 0, 0, o.canvas.Get("width").Float(), o.canvas.Get("height").Float())
}

func (o *latOverlay) frame() {
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
	ctx.Set("lineJoin", "round")

	for i := range o.cells {
		c := &o.cells[i]
		if len(c.ring) < 6 {
			continue
		}
		ctx.Call("beginPath")
		started := false
		bad := false
		for j := 0; j+1 < len(c.ring); j += 2 {
			p := o.view.graph.SpaceToScreenPosition([2]float64{c.ring[j], c.ring[j+1]})
			if math.IsNaN(p[0]) || math.IsNaN(p[1]) || math.IsInf(p[0], 0) || math.IsInf(p[1], 0) {
				bad = true
				break
			}
			if !started {
				ctx.Call("moveTo", p[0], p[1])
				started = true
				continue
			}
			ctx.Call("lineTo", p[0], p[1])
		}
		if bad || !started {
			continue
		}
		ctx.Call("closePath")
		// A light fill so overlapping labels and points stay readable, and
		// a stronger edge because the edge is the information: it is the
		// set of places equidistant between two visors.
		ctx.Set("globalAlpha", 0.10)
		ctx.Set("fillStyle", c.color)
		ctx.Call("fill")
		ctx.Set("globalAlpha", 0.45)
		ctx.Set("lineWidth", 1)
		ctx.Set("strokeStyle", c.color)
		ctx.Call("stroke")
	}
	ctx.Set("globalAlpha", 1)
}

// jsSetLatencyCells hands the projected tessellation to the overlay.
// Passing an empty array turns the cells off, which is how the view
// toggle leaves latency mode.
func jsSetLatencyCells(_ js.Value, args []js.Value) interface{} {
	if view == nil || view.latCells == nil {
		return nil
	}
	view.latCells.setCells(arg(args, 0), arg(args, 1))
	return nil
}
