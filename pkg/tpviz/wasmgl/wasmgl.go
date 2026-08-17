//go:build js && wasm

// Package wasmgl pkg/tpviz/wasmgl/wasmgl.go
// The Go/wasm WebGL view for tpviz: the same GPU force-directed graph the
// TypeScript view draws, with the engine (github.com/0magnet/cosmos-go, a Go
// port of cosmos.gl 2.6.3) and the group-boundary overlay both in Go.
//
// # Why this exists next to the TypeScript view rather than replacing it
//
// The existing WebGL view is built on @cosmograph/cosmos 1.6.1, which is
// CC-BY-NC-4.0 — the only non-permissive license in tpviz's dependency tree,
// and it is bundled into pkg/tpviz/legacy/bundle.js and embedded in the binary.
// cosmos.gl 2.x is MIT, so this view retires that dependency once it has been
// shown to match. Until then the two run side by side, selected by the view
// toggle, so they can be compared on the same data.
//
// # Division of labor
//
// The TypeScript side keeps the data assembly (buildData: filters, grouping,
// DMSG gating, service-route colors) and hands the result over as typed
// arrays. Both views are therefore fed from the same function, so a difference
// between them is a difference in the engine and not in the data.
//
// Everything that reads graph state *per frame* lives here. That is only one
// thing — the group-boundary overlay, which reprojects circles through the
// current view transform on every animation frame — but leaving it in
// JavaScript would mean a js call per circle per frame across the wasm
// boundary. Drawing it in Go keeps that loop on one side.
//
// Exposed to the page as globalThis.tpvizGL.
package wasmgl

import (
	"syscall/js"
	"unsafe"

	cosmos "github.com/0magnet/cosmos-go"
)

// view is the module's single graph instance. tpviz shows one WebGL view at a
// time, so there is no registry.
var view *glView

// glView holds the graph, the overlay it draws, and the JS functions that must
// be released when the view is torn down.
type glView struct {
	graph     *cosmos.Graph
	container js.Value

	overlay *overlay

	// onEvent is the single callback the TypeScript side installs to receive
	// clicks, hovers and simulation-end. Passing one function rather than a
	// hook per event keeps the surface small.
	onEvent js.Value

	funcs []js.Func
}

// Register publishes the WebGL view's API on globalThis.tpvizGL. It returns
// immediately — it does NOT block — so the caller owns the program's lifetime:
// the wasm-visor "netview" role (cmd/wasm-visor, tpviz_js.go) parks in
// keepAlive() after calling this. There is no longer a standalone tpviz-gl.wasm;
// the view is served from the one wasm-visor blob (pkg/tpviz serves it out of
// pkg/wasmhv/wasmbin), run in the netview role. The TypeScript side
// (cosmos-go-graph.ts) calls tpvizGL.init/setData/... identically — it can't
// tell whether tpvizGL came from a dedicated module or from the visor blob it is
// already running — which is what lets the same view be a role of the one
// wasm-visor rather than a second wasm to embed.
func Register() {
	api := map[string]interface{}{
		"init":       js.FuncOf(jsInit),
		"setData":    js.FuncOf(jsSetData),
		"setPhysics": js.FuncOf(jsSetPhysics),
		"fit":        js.FuncOf(jsFit),
		"fitIndices": js.FuncOf(jsFitIndices),
		"zoomBy":     js.FuncOf(jsZoomBy),
		"focusIndex": js.FuncOf(jsFocusIndex),
		"selectIdx":  js.FuncOf(jsSelect),
		"pause":      js.FuncOf(jsPause),
		"resume":     js.FuncOf(jsResume),
		"teardown":   js.FuncOf(jsTeardown),
		"stats":      js.FuncOf(jsStats),
		"ready":      true,
	}
	js.Global().Set("tpvizGL", js.ValueOf(api))
	js.Global().Get("console").Call("log", "[tpviz-gl] Go WebGL view loaded")
}

// arg returns the i'th argument, or undefined when it was not supplied.
func arg(args []js.Value, i int) js.Value {
	if i < len(args) {
		return args[i]
	}
	return js.Undefined()
}

// floats copies a JS Float32Array into Go. Reading element by element across
// the boundary costs a call per number, which at ~20k links is the difference
// between a data refresh being free and being visible; one bulk copy of the
// underlying bytes is not.
func floats(v js.Value) []float32 {
	if !v.Truthy() {
		return nil
	}
	n := v.Get("length").Int()
	if n == 0 {
		return nil
	}
	buf := make([]byte, n*4)
	bytes := js.Global().Get("Uint8Array").New(v.Get("buffer"), v.Get("byteOffset"), n*4)
	js.CopyBytesToGo(buf, bytes)
	// The bytes are a Float32Array's, and a []byte from make is suitably
	// aligned, so this reinterprets the copy rather than converting it
	// element by element — at ~20k links that is the difference between a
	// data refresh being free and being visible.
	return unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), n) //nolint:gosec // reinterpreting our own aligned copy
}

// colors converts an array of CSS color strings — what the TypeScript side
// already produces — into the normalized 0..1 RGBA the GPU wants.
func colors(v js.Value) []float32 {
	if !v.Truthy() {
		return nil
	}
	n := v.Length()
	out := make([]float32, 0, n*4)
	for i := 0; i < n; i++ {
		c := cosmos.GetRgbaColor(v.Index(i).String())
		out = append(out, c[0], c[1], c[2], c[3])
	}
	return out
}

// emit calls the TypeScript-side event handler, if one was installed.
func (v *glView) emit(kind string, index int, x, y float64) {
	if v == nil || !v.onEvent.Truthy() {
		return
	}
	v.onEvent.Invoke(kind, index, x, y)
}

// jsInit creates the graph inside the given container element. It returns
// false when the container is not in the DOM yet, which is how the caller
// learns to retry after the view has been shown.
func jsInit(_ js.Value, args []js.Value) interface{} {
	if view != nil {
		return true
	}
	id := arg(args, 0).String()
	container := js.Global().Get("document").Call("getElementById", id)
	if !container.Truthy() {
		return false
	}

	v := &glView{container: container, onEvent: arg(args, 1)}

	cfg := cosmos.NewConfig()
	// Mirrors the TypeScript view's configuration so the two are comparable.
	// Where v1 took accessor functions (nodeColor, nodeSize, linkColor,
	// linkWidth) v2 takes flat arrays, which arrive through setData.
	cfg.BackgroundColor = "#0b1020"
	cfg.SpaceSize = spaceSize
	cfg.PointGreyoutOpacity = 0.1
	cfg.LinkGreyoutOpacity = 0
	cfg.LinkDefaultArrows = false
	cfg.RenderLinks = true
	cfg.CurvedLinks = false
	// Fade long links so inter-group edges recede and the country/IP clusters
	// read at a glance; short intra-group links stay opaque.
	cfg.LinkVisibilityDistanceRange = [2]float64{40, 250}
	cfg.LinkVisibilityMinTransparency = 0.08
	// Small pixel dots that do not grow on zoom — at ~1k points, big dots
	// overlap into solid blobs.
	cfg.ScalePointsOnZoom = false
	cfg.FitViewOnInit = true
	cfg.RenderHoveredPointRing = false

	// Light gravity + repulsion settle a ~20k-edge hairball into a readable
	// ball in a couple of seconds, on the GPU.
	cfg.SimulationGravity = 0.9
	cfg.SimulationRepulsion = 0.5
	cfg.SimulationLinkSpring = 1.3
	cfg.SimulationLinkDistance = 3
	cfg.SimulationFriction = 0.85
	cfg.SimulationDecay = 2000

	cfg.OnPointClick = func(index int, pos [2]float64, _ js.Value) {
		// Persistent selection, matching the TypeScript view's
		// click-to-isolate: the neighborhood stays highlighted after the
		// pointer leaves, until another point or the background is clicked.
		v.graph.SelectPointByIndex(index, true)
		v.emit("click", index, pos[0], pos[1])
	}
	cfg.OnBackgroundClick = func(js.Value) {
		v.graph.UnselectPoints()
		v.emit("bgclick", -1, 0, 0)
	}
	cfg.OnPointMouseOver = func(index int, _ [2]float64, event js.Value) {
		// Highlight the hovered point's neighborhood — the WebGL analog of
		// the flat view's show-edges-only-on-hover.
		v.graph.SelectPointByIndex(index, true)
		var cx, cy float64
		if event.Truthy() && event.Get("clientX").Type() == js.TypeNumber {
			cx, cy = event.Get("clientX").Float(), event.Get("clientY").Float()
		}
		v.emit("over", index, cx, cy)
	}
	cfg.OnPointMouseOut = func(js.Value) {
		v.emit("out", -1, 0, 0)
	}
	cfg.OnSimulationEnd = func() {
		// Re-frame once the layout settles; the initial fit is too tight while
		// every point still sits clustered near the center.
		v.graph.FitView(400, 0.1)
		v.emit("simend", -1, 0, 0)
	}

	g, err := cosmos.New(container, cfg)
	if err != nil {
		js.Global().Get("console").Call("error", "[tpviz-gl] "+err.Error())
		return false
	}
	v.graph = g
	v.overlay = newOverlay(v)
	view = v
	return true
}

// jsSetData replaces the graph's contents. The payload is what the TypeScript
// buildData produced, converted to typed arrays:
//
//	positions   Float32Array  x,y per point (empty → seeded near the center)
//	pointColors string[]      CSS color per point
//	pointSizes  Float32Array  pixel radius per point
//	links       Float32Array  source,target index pairs
//	linkColors  string[]      CSS color per link
//	linkWidths  Float32Array  width per link
//	grouped     bool          fixed positions, simulation off
//	boundaries  object[]      {x, y, r, color, label} group circles
func jsSetData(_ js.Value, args []js.Value) interface{} {
	v := view
	if v == nil {
		return false
	}
	p := arg(args, 0)
	if !p.Truthy() {
		return false
	}

	positions := floats(p.Get("positions"))
	pointSizes := floats(p.Get("pointSizes"))
	grouped := p.Get("grouped").Truthy()

	// v1 randomized points that arrived without a position; v2 uses what it is
	// given, so a free layout has to be seeded here or every point starts at
	// the origin and the simulation has nothing to push apart.
	if len(positions) == 0 && len(pointSizes) > 0 {
		positions = seedPositions(len(pointSizes))
	}

	v.graph.Config().EnableSimulation = !grouped
	// Skip the engine's rescale: both position sources are already in cosmos
	// space. Grouped mode arrives pre-packed into it by buildData's toSpace,
	// and the free layout is seeded into it below. Letting the engine rescale
	// moves the points while the group-boundary circles stay at the
	// coordinates they were computed for, so the rings end up drawn somewhere
	// the points no longer are.
	v.graph.SetPointPositions(positions, true)
	v.graph.SetPointColors(colors(p.Get("pointColors")))
	v.graph.SetPointSizes(pointSizes)
	v.graph.SetLinks(floats(p.Get("links")))
	v.graph.SetLinkColors(colors(p.Get("linkColors")))
	v.graph.SetLinkWidths(floats(p.Get("linkWidths")))
	v.graph.Render()

	v.overlay.setBoundaries(p.Get("boundaries"))

	pointColors := colors(p.Get("pointColors"))
	sample := []interface{}{}
	for i := 0; i < 3 && i*4+3 < len(pointColors); i++ {
		sample = append(sample, []interface{}{
			float64(pointColors[i*4]), float64(pointColors[i*4+1]),
			float64(pointColors[i*4+2]), float64(pointColors[i*4+3]),
		})
	}
	lastStats = map[string]interface{}{
		"loaded":      true,
		"points":      len(positions) / 2,
		"colorFloats": len(pointColors),
		"sizes":       len(pointSizes),
		"links":       len(floats(p.Get("links"))) / 2,
		"grouped":     grouped,
		"boundaries":  len(v.overlay.circles),
		"colorSample": sample,
	}
	return true
}

// lastStats records what the most recent setData actually handed the engine,
// so the view can be inspected from the console without guessing.
var lastStats map[string]interface{}

// jsStats reports the shape of the data currently loaded.
func jsStats(js.Value, []js.Value) interface{} {
	if lastStats == nil {
		return js.ValueOf(map[string]interface{}{"loaded": false})
	}
	return js.ValueOf(lastStats)
}

// jsFitIndices frames a subset of the points. tpviz frames the connected
// subset when there is one, so that the ring of isolated visors it keeps for
// parity with the flat view does not shrink the main cluster.
func jsFitIndices(_ js.Value, args []js.Value) interface{} {
	v := view
	if v == nil {
		return nil
	}
	idx := arg(args, 0)
	if !idx.Truthy() || idx.Length() == 0 {
		v.graph.FitView(500, 0.1)
		return nil
	}
	indices := make([]int, idx.Length())
	for i := range indices {
		indices[i] = idx.Index(i).Int()
	}
	v.graph.FitViewByPointIndices(indices, 500, 0.1)
	return nil
}

func jsSetPhysics(_ js.Value, args []js.Value) interface{} {
	v := view
	if v == nil {
		return nil
	}
	if arg(args, 0).Truthy() {
		v.graph.Config().EnableSimulation = true
		v.graph.Start()
	} else {
		v.graph.Pause()
	}
	return nil
}

func jsFit(js.Value, []js.Value) interface{} {
	if view != nil {
		view.graph.FitView(400, 0.1)
	}
	return nil
}

func jsZoomBy(_ js.Value, args []js.Value) interface{} {
	if view != nil {
		view.graph.SetZoomLevel(view.graph.GetZoomLevel()*arg(args, 0).Float(), 200)
	}
	return nil
}

func jsFocusIndex(_ js.Value, args []js.Value) interface{} {
	v := view
	if v == nil {
		return nil
	}
	i := arg(args, 0).Int()
	if i < 0 {
		return nil
	}
	v.graph.ZoomToPointByIndex(i, 500, 5, true)
	v.graph.SelectPointByIndex(i, true)
	return nil
}

func jsSelect(_ js.Value, args []js.Value) interface{} {
	v := view
	if v == nil {
		return nil
	}
	if i := arg(args, 0).Int(); i >= 0 {
		v.graph.SelectPointByIndex(i, true)
	} else {
		v.graph.UnselectPoints()
	}
	return nil
}

func jsPause(js.Value, []js.Value) interface{} {
	if view != nil {
		view.graph.Pause()
		view.overlay.stop()
	}
	return nil
}

func jsResume(js.Value, []js.Value) interface{} {
	if view != nil {
		view.graph.Unpause()
		view.overlay.start()
	}
	return nil
}

// jsTeardown destroys the graph and releases every JS function this module
// installed, so switching views repeatedly does not leak them.
func jsTeardown(js.Value, []js.Value) interface{} {
	v := view
	if v == nil {
		return nil
	}
	v.overlay.destroy()
	v.graph.Destroy()
	for _, f := range v.funcs {
		f.Release()
	}
	view = nil
	return nil
}
