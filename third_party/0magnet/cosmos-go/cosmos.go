//go:build js && wasm

// Package cosmos is a Go/WebAssembly port of cosmos.gl 2.6.3
// (https://github.com/cosmograph-org/cosmos, MIT), the GPU-accelerated
// force-directed graph layout and rendering library. Both the force
// simulation and the rendering run on the GPU in WebGL shaders (carried
// over verbatim from the original); this package drives them through
// syscall/js and compiles with the standard Go toolchain
// (GOOS=js GOARCH=wasm) as well as TinyGo (-target wasm).
package cosmos

import (
	"math"
	"syscall/js"
)

// Graph is the port of the cosmos Graph class.
type Graph struct {
	cfg    *Config
	data   *graphData
	div    js.Value
	canvas js.Value
	ctx    *glCtx

	st       *store
	points   *points
	lines    *lines
	clusters *clusters

	forceGravity      *forceGravity
	forceCenter       *forceCenter
	forceManyBody     manyBody
	forceLinkIncoming *forceLink
	forceLinkOutgoing *forceLink
	forceMouse        *forceMouse
	zoom              *zoomState
	fps               *fpsMonitor

	rafID             js.Value
	rafCb             js.Func
	framesRunning     bool
	isRightClickMouse bool
	currentEvent      js.Value

	findHoveredItemExecutionCount int
	isMouseOnCanvas               bool
	isFirstRenderAfterInit        bool
	fitViewOnInitTimeoutID        js.Value

	isPointPositionsUpdateNeeded    bool
	isPointColorUpdateNeeded        bool
	isPointSizeUpdateNeeded         bool
	isPointShapeUpdateNeeded        bool
	isPointImageIndicesUpdateNeeded bool
	isPointImageSizesUpdateNeeded   bool
	isLinksUpdateNeeded             bool
	isLinkColorUpdateNeeded         bool
	isLinkWidthUpdateNeeded         bool
	isLinkArrowUpdateNeeded         bool
	isPointClusterUpdateNeeded      bool
	isForceManyBodyUpdateNeeded     bool
	isForceLinkUpdateNeeded         bool
	isForceCenterUpdateNeeded       bool

	isDestroyed bool

	funcs []js.Func
}

// New creates a cosmos Graph inside the given container element (a div).
// A nil config uses the defaults.
func New(div js.Value, cfg *Config) (*Graph, error) {
	if cfg == nil {
		cfg = NewConfig()
	}
	g := &Graph{
		cfg:                    cfg,
		data:                   newGraphData(cfg),
		div:                    div,
		st:                     newStore(),
		isFirstRenderAfterInit: true,
	}

	document := js.Global().Get("document")
	canvas := document.Call("createElement", "canvas")
	style := canvas.Get("style")
	style.Call("setProperty", "width", "100%")
	style.Call("setProperty", "height", "100%")
	div.Call("appendChild", canvas)
	g.addAttribution()

	w := canvas.Get("clientWidth").Float()
	h := canvas.Get("clientHeight").Float()
	canvas.Set("width", w*cfg.PixelRatio)
	canvas.Set("height", h*cfg.PixelRatio)
	g.canvas = canvas

	ctx, err := newGLCtx(canvas)
	if err != nil {
		return nil, err
	}
	g.ctx = ctx

	g.st.adjustSpaceSize(cfg.SpaceSize, ctx.maxTextureSize)
	g.st.webglMaxTextureSize = ctx.maxTextureSize
	g.st.updateScreenSize(w, h)

	g.zoom = newZoomState(g.st, cfg)
	g.zoom.onStart = func(sourceEvent js.Value) {
		g.currentEvent = sourceEvent
		if cfg.OnZoomStart != nil {
			cfg.OnZoomStart(sourceEvent.Truthy())
		}
	}
	g.zoom.onZoom = func(sourceEvent js.Value) {
		userDriven := sourceEvent.Truthy()
		if userDriven && sourceEvent.Get("offsetX").Truthy() {
			g.updateMousePosition(sourceEvent)
		}
		g.currentEvent = sourceEvent
		if cfg.OnZoom != nil {
			cfg.OnZoom(userDriven)
		}
	}
	g.zoom.onEnd = func(sourceEvent js.Value) {
		g.currentEvent = sourceEvent
		if cfg.OnZoomEnd != nil {
			cfg.OnZoomEnd(sourceEvent.Truthy())
		}
	}
	g.zoom.dragSubject = func() bool {
		return g.st.hoveredPoint != nil && !g.st.isSpaceKeyPressed
	}
	g.zoom.onDragStart = func(event js.Value) {
		if g.st.hoveredPoint != nil {
			g.st.draggingPointIndex = g.st.hoveredPoint.index
		}
		g.currentEvent = event
		g.updateCanvasCursor()
		if cfg.OnDragStart != nil {
			cfg.OnDragStart(event)
		}
	}
	g.zoom.onDrag = func(event js.Value) {
		g.updateMousePosition(event)
		g.currentEvent = event
		if cfg.OnDrag != nil {
			cfg.OnDrag(event)
		}
	}
	g.zoom.onDragEnd = func(event js.Value) {
		g.st.draggingPointIndex = -1
		g.currentEvent = event
		g.updateCanvasCursor()
		if cfg.OnDragEnd != nil {
			cfg.OnDragEnd(event)
		}
	}
	g.zoom.attach(canvas)
	if !cfg.EnableZoom {
		g.zoom.wheelEnabled = false
	}
	initialZoom := cfg.InitialZoomLevel
	if initialZoom == 0 {
		initialZoom = 1
	}
	g.zoom.scaleTo(initialZoom, 0)

	g.listen(canvas, "mouseenter", func(js.Value) { g.isMouseOnCanvas = true })
	g.listen(canvas, "mouseleave", func(event js.Value) {
		g.isMouseOnCanvas = false
		g.currentEvent = event
		if g.st.hoveredPoint != nil && cfg.OnPointMouseOut != nil {
			cfg.OnPointMouseOut(event)
		}
		if g.st.hoveredLinkIndex >= 0 && cfg.OnLinkMouseOut != nil {
			cfg.OnLinkMouseOut(event)
		}
		g.isRightClickMouse = false
		g.st.hoveredPoint = nil
		g.st.hoveredLinkIndex = -1
		g.updateCanvasCursor()
	})
	g.listen(canvas, "click", g.onClick)
	g.listen(canvas, "mousemove", g.onMouseMove)
	g.listen(canvas, "contextmenu", func(event js.Value) { event.Call("preventDefault") })
	g.listen(document, "keydown", func(event js.Value) {
		if event.Get("code").String() == "Space" {
			g.st.isSpaceKeyPressed = true
		}
	})
	g.listen(document, "keyup", func(event js.Value) {
		if event.Get("code").String() == "Space" {
			g.st.isSpaceKeyPressed = false
		}
	})

	g.st.maxPointSize = ctx.maxPointSize / cfg.PixelRatio

	// initialize simulation state based on EnableSimulation
	g.st.isSimulationRunning = cfg.EnableSimulation

	g.points = newPoints(ctx, cfg, g.st, g.data)
	g.lines = newLines(ctx, cfg, g.st, g.data, g.points)
	if cfg.EnableSimulation {
		g.forceGravity = newForceGravity()
		g.forceCenter = newForceCenter()
		if cfg.UseClassicQuadtree {
			g.forceManyBody = newForceManyBodyQuadtree()
		} else {
			g.forceManyBody = newForceManyBody()
		}
		g.forceLinkIncoming = newForceLink()
		g.forceLinkOutgoing = newForceLink()
		g.forceMouse = newForceMouse()
	}
	g.clusters = newClusters(ctx, cfg, g.st, g.data, g.points)

	g.st.setBackgroundColor(parseRGBA(cfg.BackgroundColor))
	g.st.setHoveredPointRingColor(cfg.HoveredPointRingColor)
	g.st.setFocusedPointRingColor(cfg.FocusedPointRingColor)
	if cfg.FocusedPointIndex >= 0 {
		g.st.focusedPointIndex = cfg.FocusedPointIndex
	}
	g.st.setGreyoutPointColor(cfg.PointGreyoutColor)
	g.st.setHoveredLinkColor(cfg.HoveredLinkColor)
	g.st.updateLinkHoveringEnabled(cfg)

	if cfg.ShowFPSMonitor {
		g.fps = newFPSMonitor(canvas)
	}
	if cfg.RandomSeed != "" {
		g.st.addRandomSeed(cfg.RandomSeed)
	}

	g.rafCb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		now := 0.0
		if len(args) > 0 {
			now = args[0].Float()
		}
		g.onFrame(now)
		return nil
	})

	return g, nil
}

func (g *Graph) listen(target js.Value, event string, fn func(js.Value)) {
	f := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		fn(ev)
		return nil
	})
	g.funcs = append(g.funcs, f)
	target.Call("addEventListener", event, f)
}

// Progress is the simulation progress from 0 to 1.
func (g *Graph) Progress() float64 { return g.st.simulationProgress }

// IsSimulationRunning reports whether the simulation is running.
func (g *Graph) IsSimulationRunning() bool { return g.st.isSimulationRunning }

// MaxPointSize is the maximum gl.POINTS size the hardware can render.
func (g *Graph) MaxPointSize() float64 { return g.st.maxPointSize }

// Config returns the active configuration. Simulation parameters and most
// rendering parameters are read live every frame; data-derived parameters
// (default colors/sizes/widths/arrows, curve geometry) need the matching
// Update* method to be re-applied.
func (g *Graph) Config() *Config { return g.cfg }

// SetPointPositions sets the point positions as [x1, y1, x2, y2, ...].
// Pass dontRescale to skip position rescaling for this call only.
func (g *Graph) SetPointPositions(pointPositions []float32, dontRescale ...bool) {
	if g.isDestroyed {
		return
	}
	g.data.inputPointPositions = pointPositions
	g.points.hasSkipRescale = len(dontRescale) > 0
	g.points.shouldSkipRescale = len(dontRescale) > 0 && dontRescale[0]
	g.isPointPositionsUpdateNeeded = true
	g.isLinksUpdateNeeded = true
	g.isPointColorUpdateNeeded = true
	g.isPointSizeUpdateNeeded = true
	g.isPointShapeUpdateNeeded = true
	g.isPointImageIndicesUpdateNeeded = true
	g.isPointImageSizesUpdateNeeded = true
	g.isPointClusterUpdateNeeded = true
	g.isForceManyBodyUpdateNeeded = true
	g.isForceLinkUpdateNeeded = true
	g.isForceCenterUpdateNeeded = true
}

// SetPointColors sets the point colors as [r, g, b, a, ...] with RGB
// components 0..255 and alpha 0..1.
func (g *Graph) SetPointColors(pointColors []float32) {
	g.data.inputPointColors = pointColors
	g.isPointColorUpdateNeeded = true
}

// GetPointColors returns the current point colors (normalized 0..1 RGBA).
func (g *Graph) GetPointColors() []float32 { return g.data.pointColors }

// SetPointSizes sets the point sizes as [size1, size2, ...].
func (g *Graph) SetPointSizes(pointSizes []float32) {
	g.data.inputPointSizes = pointSizes
	g.isPointSizeUpdateNeeded = true
}

// GetPointSizes returns the current point sizes.
func (g *Graph) GetPointSizes() []float32 { return g.data.pointSizes }

// SetPointShapes sets the point shapes (see the Shape* constants).
func (g *Graph) SetPointShapes(pointShapes []float32) {
	g.data.inputPointShapes = pointShapes
	g.isPointShapeUpdateNeeded = true
}

// SetImageData sets point images from an array of JS ImageData objects.
func (g *Graph) SetImageData(imageData []js.Value) {
	if g.isDestroyed {
		return
	}
	g.points.createAtlas(imageData)
}

// SetPointImageIndices sets which image each point uses (-1 = none).
func (g *Graph) SetPointImageIndices(imageIndices []float32) {
	g.data.inputPointImageIndices = imageIndices
	g.isPointImageIndicesUpdateNeeded = true
}

// SetPointImageSizes sets the sizes of the point images.
func (g *Graph) SetPointImageSizes(imageSizes []float32) {
	g.data.inputPointImageSizes = imageSizes
	g.isPointImageSizesUpdateNeeded = true
}

// SetLinks sets the links as [source1, target1, source2, target2, ...]
// point indices.
func (g *Graph) SetLinks(links []float32) {
	g.data.inputLinks = links
	g.isLinksUpdateNeeded = true
	g.isLinkColorUpdateNeeded = true
	g.isLinkWidthUpdateNeeded = true
	g.isLinkArrowUpdateNeeded = true
	g.isForceLinkUpdateNeeded = true
}

// SetLinkColors sets the link colors as [r, g, b, a, ...] with RGB
// components 0..255 and alpha 0..1.
func (g *Graph) SetLinkColors(linkColors []float32) {
	g.data.inputLinkColors = linkColors
	g.isLinkColorUpdateNeeded = true
}

// GetLinkColors returns the current link colors (normalized 0..1 RGBA).
func (g *Graph) GetLinkColors() []float32 { return g.data.linkColors }

// SetLinkWidths sets the link widths.
func (g *Graph) SetLinkWidths(linkWidths []float32) {
	g.data.inputLinkWidths = linkWidths
	g.isLinkWidthUpdateNeeded = true
}

// GetLinkWidths returns the current link widths.
func (g *Graph) GetLinkWidths() []float32 { return g.data.linkWidths }

// SetLinkArrows sets whether each link has an arrow.
func (g *Graph) SetLinkArrows(linkArrows []bool) {
	g.data.linkArrowsBool = linkArrows
	g.isLinkArrowUpdateNeeded = true
}

// SetLinkStrength sets the per-link spring strength.
func (g *Graph) SetLinkStrength(linkStrength []float32) {
	g.data.inputLinkStrength = linkStrength
	g.isForceLinkUpdateNeeded = true
}

// SetPointClusters maps points to cluster indices (-1 = no cluster).
func (g *Graph) SetPointClusters(pointClusters []int) {
	g.data.inputPointClusters = pointClusters
	g.isPointClusterUpdateNeeded = true
}

// SetClusterPositions sets fixed cluster positions as [x1, y1, ...]
// (negative coordinates = position the cluster at its center of mass).
func (g *Graph) SetClusterPositions(clusterPositions []float32) {
	g.data.inputClusterPositions = clusterPositions
	g.isPointClusterUpdateNeeded = true
}

// SetPointClusterStrength sets per-point cluster force coefficients.
func (g *Graph) SetPointClusterStrength(clusterStrength []float32) {
	g.data.inputClusterStrength = clusterStrength
	g.isPointClusterUpdateNeeded = true
}

// SetPinnedPoints sets which points are pinned (fixed) in position.
// Pass nil or an empty slice to unpin all points.
func (g *Graph) SetPinnedPoints(pinnedIndices []int) {
	if g.isDestroyed {
		return
	}
	if len(pinnedIndices) == 0 {
		g.data.inputPinnedPoints = nil
	} else {
		g.data.inputPinnedPoints = pinnedIndices
	}
	g.points.updatePinnedStatus()
}

// Render renders the graph and starts the render loop. The optional alpha
// sets the simulation energy (0 stops after one frame; omitted keeps the
// current value).
func (g *Graph) Render(simulationAlpha ...float64) {
	if g.isDestroyed {
		return
	}
	g.data.update()
	if g.data.pointsNumber() == 0 && g.data.linksNumber() == 0 {
		g.stopFrames()
		bg := g.st.backgroundColor
		g.ctx.clearTarget(nil, bg[0], bg[1], bg[2], bg[3])
		return
	}

	// if InitialZoomLevel is set there is no need to fit the view
	if g.isFirstRenderAfterInit && g.cfg.FitViewOnInit && g.cfg.InitialZoomLevel == 0 {
		g.fitViewOnInitTimeoutID = setTimeout(func() {
			if g.cfg.FitViewByPointIndices != nil {
				g.FitViewByPointIndices(g.cfg.FitViewByPointIndices, g.cfg.FitViewDuration, g.cfg.FitViewPadding)
			} else if g.cfg.FitViewByPointsInRect != nil {
				positions := make([]float64, 0, len(g.cfg.FitViewByPointsInRect)*2)
				for _, p := range g.cfg.FitViewByPointsInRect {
					positions = append(positions, p[0], p[1])
				}
				g.setZoomTransformByPointPositions(positions, g.cfg.FitViewDuration, math.NaN(), g.cfg.FitViewPadding)
			} else {
				g.FitView(g.cfg.FitViewDuration, g.cfg.FitViewPadding)
			}
		}, g.cfg.FitViewDelay)
	}
	alpha := g.st.alpha
	if len(simulationAlpha) > 0 {
		alpha = simulationAlpha[0]
	}
	g.update(alpha)
	g.startFrames()

	g.isFirstRenderAfterInit = false
}

// ZoomToPointByIndex centers the view on a point and zooms in.
// Defaults matching the original: duration 700, scale 3, canZoomOut true.
func (g *Graph) ZoomToPointByIndex(index int, duration float64, scale float64, canZoomOut bool) {
	if g.isDestroyed || index < 0 {
		return
	}
	pixels := g.points.currentPositionFbo.readPixels()
	if index*4+1 >= len(pixels) {
		return
	}
	posX := float64(pixels[index*4])
	posY := float64(pixels[index*4+1])
	distance := g.zoom.getDistanceToPoint([2]float64{posX, posY})
	zoomLevel := scale
	if !canZoomOut {
		zoomLevel = math.Max(g.GetZoomLevel(), scale)
	}
	if distance < math.Min(g.st.screenSize[0], g.st.screenSize[1]) {
		g.setZoomTransformByPointPositions([]float64{posX, posY}, duration, zoomLevel, 0.1)
	} else {
		transform := g.zoom.getTransform([][2]float64{{posX, posY}}, zoomLevel, true, 0.1)
		middle := g.zoom.getMiddlePointTransform([2]float64{posX, posY})
		g.zoom.transformChain(
			[]zoomTransform{middle, transform},
			[]float64{duration / 2, duration / 2},
			[]func(float64) float64{easeQuadIn, easeQuadOut},
		)
	}
}

// Zoom zooms the view to the given zoom level.
func (g *Graph) Zoom(value float64, duration float64) { g.SetZoomLevel(value, duration) }

// SetZoomLevel zooms the view to the given zoom level.
func (g *Graph) SetZoomLevel(value float64, duration float64) {
	g.zoom.scaleTo(value, duration)
}

// GetZoomLevel returns the zoom level of the view.
func (g *Graph) GetZoomLevel() float64 { return g.zoom.eventTransform.k }

// GetPointPositions returns the current coordinates of all points as
// [x1, y1, x2, y2, ...].
func (g *Graph) GetPointPositions() []float64 {
	if g.isDestroyed || g.data.pointsNumber() == 0 || g.points.currentPositionFbo == nil {
		return nil
	}
	pixels := g.points.currentPositionFbo.readPixels()
	n := g.data.pointsNumber()
	positions := make([]float64, n*2)
	for i := 0; i < n && i*4+1 < len(pixels); i++ {
		positions[i*2] = float64(pixels[i*4])
		positions[i*2+1] = float64(pixels[i*4+1])
	}
	return positions
}

// GetClusterPositions returns the current coordinates of the clusters as
// [x1, y1, x2, y2, ...].
func (g *Graph) GetClusterPositions() []float64 {
	if g.isDestroyed || g.data.pointClusters == nil || !g.clusters.created {
		return nil
	}
	g.clusters.calculateCentermass()
	pixels := g.clusters.centermassFbo.readPixels()
	positions := make([]float64, g.clusters.clusterCount*2)
	for i := 0; i < g.clusters.clusterCount && i*4+2 < len(pixels); i++ {
		sumX := float64(pixels[i*4])
		sumY := float64(pixels[i*4+1])
		sumN := float64(pixels[i*4+2])
		if sumN != 0 {
			positions[i*2] = sumX / sumN
			positions[i*2+1] = sumY / sumN
		}
	}
	return positions
}

// FitView centers and zooms the view to fit all points.
func (g *Graph) FitView(duration float64, padding float64) {
	g.setZoomTransformByPointPositions(g.GetPointPositions(), duration, math.NaN(), padding)
}

// FitViewByPointIndices centers and zooms the view to fit the given points.
func (g *Graph) FitViewByPointIndices(indices []int, duration float64, padding float64) {
	positionsArray := g.GetPointPositions()
	positions := make([]float64, 0, len(indices)*2)
	for _, index := range indices {
		if index*2+1 < len(positionsArray) {
			positions = append(positions, positionsArray[index*2], positionsArray[index*2+1])
		}
	}
	g.setZoomTransformByPointPositions(positions, duration, math.NaN(), padding)
}

// FitViewByPointPositions centers and zooms the view to fit the given
// positions [x1, y1, ...].
func (g *Graph) FitViewByPointPositions(positions []float64, duration float64, padding float64) {
	g.setZoomTransformByPointPositions(positions, duration, math.NaN(), padding)
}

// GetPointsInRect returns the indices of points inside a rectangular area
// [[left, top], [right, bottom]] in screen coordinates.
func (g *Graph) GetPointsInRect(selection [2][2]float64) []int {
	if g.isDestroyed || g.points.selectedFbo == nil {
		return nil
	}
	h := g.st.screenSize[1]
	g.st.selectedArea = [2][2]float64{
		{selection[0][0], h - selection[1][1]},
		{selection[1][0], h - selection[0][1]},
	}
	g.points.findPointsOnAreaSelection()
	return g.readSelectedIndices()
}

// GetPointsInPolygon returns the indices of points inside a polygon area
// [[x1, y1], [x2, y2], ...] in screen coordinates.
func (g *Graph) GetPointsInPolygon(polygonPath [][2]float64) []int {
	if g.isDestroyed || len(polygonPath) < 3 || g.points.selectedFbo == nil {
		return nil
	}
	h := g.st.screenSize[1]
	converted := make([][2]float64, len(polygonPath))
	for i, p := range polygonPath {
		converted[i] = [2]float64{p[0], h - p[1]}
	}
	g.points.updatePolygonPath(converted)
	g.points.findPointsOnPolygonSelection()
	return g.readSelectedIndices()
}

func (g *Graph) readSelectedIndices() []int {
	pixels := g.points.selectedFbo.readPixels()
	var indices []int
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			indices = append(indices, i/4)
		}
	}
	return indices
}

// SelectPointsInRect selects points inside a rectangular area
// (ok=false clears the selection).
func (g *Graph) SelectPointsInRect(selection [2][2]float64, ok bool) {
	if g.isDestroyed {
		return
	}
	if ok {
		g.st.selectedIndices = g.GetPointsInRect(selection)
		g.st.hasSelection = true
	} else {
		g.st.selectedIndices = nil
		g.st.hasSelection = false
	}
	g.points.updateGreyoutStatus()
}

// SelectPointsInPolygon selects points inside a polygon area
// (nil path clears the selection).
func (g *Graph) SelectPointsInPolygon(polygonPath [][2]float64) {
	if g.isDestroyed {
		return
	}
	if polygonPath == nil {
		g.st.selectedIndices = nil
		g.st.hasSelection = false
	} else {
		if len(polygonPath) < 3 {
			consoleWarn("Polygon path requires at least 3 points to form a polygon.")
			return
		}
		g.st.selectedIndices = g.GetPointsInPolygon(polygonPath)
		g.st.hasSelection = true
	}
	g.points.updateGreyoutStatus()
}

// SelectPointByIndex selects a point; optionally its adjacent points too.
func (g *Graph) SelectPointByIndex(index int, selectAdjacentPoints bool) {
	if selectAdjacentPoints {
		adjacent := g.data.getAdjacentIndices(index)
		g.SelectPointsByIndices(append([]int{index}, adjacent...))
	} else {
		g.SelectPointsByIndices([]int{index})
	}
}

// SelectPointsByIndices selects multiple points (nil clears the selection).
func (g *Graph) SelectPointsByIndices(indices []int) {
	if indices == nil {
		g.st.selectedIndices = nil
		g.st.hasSelection = false
	} else {
		g.st.selectedIndices = indices
		g.st.hasSelection = true
	}
	g.points.updateGreyoutStatus()
}

// UnselectPoints unselects all points.
func (g *Graph) UnselectPoints() {
	g.st.selectedIndices = nil
	g.st.hasSelection = false
	g.points.updateGreyoutStatus()
}

// GetSelectedIndices returns the currently selected point indices
// (nil = no selection).
func (g *Graph) GetSelectedIndices() []int {
	if !g.st.hasSelection {
		return nil
	}
	return g.st.selectedIndices
}

// GetAdjacentIndices returns the indices adjacent to a point.
func (g *Graph) GetAdjacentIndices(index int) []int {
	return g.data.getAdjacentIndices(index)
}

// SpaceToScreenPosition converts X, Y from space to screen coordinates.
func (g *Graph) SpaceToScreenPosition(spacePosition [2]float64) [2]float64 {
	return g.zoom.convertSpaceToScreenPosition(spacePosition)
}

// ScreenToSpacePosition converts X, Y from screen to space coordinates.
func (g *Graph) ScreenToSpacePosition(screenPosition [2]float64) [2]float64 {
	return g.zoom.convertScreenToSpacePosition(screenPosition)
}

// SpaceToScreenRadius converts a radius from space to screen units.
func (g *Graph) SpaceToScreenRadius(spaceRadius float64) float64 {
	return g.zoom.convertSpaceToScreenRadius(spaceRadius)
}

// GetPointRadiusByIndex returns the point radius by index (NaN if unknown).
func (g *Graph) GetPointRadiusByIndex(index int) float64 {
	if index >= 0 && index < len(g.data.pointSizes) {
		return float64(g.data.pointSizes[index])
	}
	return math.NaN()
}

// SetFocusedPointByIndex highlights a ring around the point
// (negative index resets focus).
func (g *Graph) SetFocusedPointByIndex(index int) {
	if index < 0 {
		g.st.focusedPointIndex = -1
	} else {
		g.st.focusedPointIndex = index
	}
}

// TrackPointPositionsByIndices tracks point positions on each tick.
func (g *Graph) TrackPointPositionsByIndices(indices []int) {
	g.points.trackPointsByIndices(indices)
}

// GetTrackedPointPositionsMap returns the tracked point positions.
func (g *Graph) GetTrackedPointPositionsMap() map[int][2]float64 {
	return g.points.getTrackedPositionsMap()
}

// GetTrackedPointPositionsArray returns tracked positions as
// [x1, y1, x2, y2, ...] ordered by the tracked indices.
func (g *Graph) GetTrackedPointPositionsArray() []float64 {
	return g.points.getTrackedPositionsArray()
}

// GetSampledPoints returns a spatially sampled subset of the points
// visible on screen with their indices and positions.
func (g *Graph) GetSampledPoints() (indices []int, positions []float64) {
	return g.points.getSampledPoints()
}

// Start starts the simulation with the given alpha (default 1).
func (g *Graph) Start(alpha ...float64) {
	if g.isDestroyed || g.data.pointsNumber() == 0 {
		return
	}
	a := 1.0
	if len(alpha) > 0 {
		a = alpha[0]
	}
	g.st.isSimulationRunning = true
	g.st.simulationProgress = 0
	g.st.alpha = a
	if g.cfg.OnSimulationStart != nil {
		g.cfg.OnSimulationStart()
	}
}

// Stop stops the simulation and resets its state.
func (g *Graph) Stop() {
	g.st.isSimulationRunning = false
	g.st.simulationProgress = 0
	g.st.alpha = 0
	if g.cfg.OnSimulationEnd != nil {
		g.cfg.OnSimulationEnd()
	}
}

// Pause pauses the simulation, preserving its current state.
func (g *Graph) Pause() {
	g.st.isSimulationRunning = false
	if g.cfg.OnSimulationPause != nil {
		g.cfg.OnSimulationPause()
	}
}

// Unpause resumes a paused simulation.
func (g *Graph) Unpause() {
	g.st.isSimulationRunning = true
	if g.cfg.OnSimulationUnpause != nil {
		g.cfg.OnSimulationUnpause()
	}
}

// Step runs one step of the simulation manually, even when paused.
func (g *Graph) Step() {
	if g.isDestroyed || !g.cfg.EnableSimulation || g.st.pointsTextureSize == 0 {
		return
	}
	g.runSimulationStep(true)
}

// Destroy destroys the Graph instance.
func (g *Graph) Destroy() {
	if g.isDestroyed {
		return
	}
	if g.fitViewOnInitTimeoutID.Truthy() {
		js.Global().Call("clearTimeout", g.fitViewOnInitTimeoutID)
	}
	g.stopFrames()
	if g.fps != nil {
		g.fps.destroy()
		g.fps = nil
	}
	g.points.destroy()
	g.lines.destroy()
	g.clusters.destroy()
	if g.forceCenter != nil {
		g.forceCenter.destroy()
	}
	if g.forceLinkIncoming != nil {
		g.forceLinkIncoming.destroy()
	}
	if g.forceLinkOutgoing != nil {
		g.forceLinkOutgoing.destroy()
	}
	if g.forceManyBody != nil {
		g.forceManyBody.destroy()
	}
	bg := g.st.backgroundColor
	g.ctx.clearTarget(nil, bg[0], bg[1], bg[2], bg[3])
	if g.canvas.Get("parentNode").Truthy() {
		g.canvas.Get("parentNode").Call("removeChild", g.canvas)
	}
	for _, f := range g.funcs {
		f.Release()
	}
	g.funcs = nil
	g.isDestroyed = true
}

// create applies pending data changes (the v2 create()).
func (g *Graph) create() {
	if g.isPointPositionsUpdateNeeded {
		g.points.updatePositions()
	}
	if g.isPointColorUpdateNeeded {
		g.points.updateColor()
	}
	if g.isPointSizeUpdateNeeded {
		g.points.updateSize()
	}
	if g.isPointShapeUpdateNeeded {
		g.points.updateShape()
	}
	if g.isPointImageIndicesUpdateNeeded {
		g.points.updateImageIndices()
	}
	if g.isPointImageSizesUpdateNeeded {
		g.points.updateImageSizes()
	}

	if g.isLinksUpdateNeeded {
		g.lines.updatePointsBuffer()
	}
	if g.isLinkColorUpdateNeeded {
		g.lines.updateColor()
	}
	if g.isLinkWidthUpdateNeeded {
		g.lines.updateWidth()
	}
	if g.isLinkArrowUpdateNeeded {
		g.lines.updateArrow()
	}

	if g.isForceManyBodyUpdateNeeded && g.forceManyBody != nil {
		g.forceManyBody.create(g.ctx, g.st)
	}
	if g.isForceLinkUpdateNeeded {
		if g.forceLinkIncoming != nil {
			g.forceLinkIncoming.create(g.ctx, g.st, g.data, linkIncoming)
		}
		if g.forceLinkOutgoing != nil {
			g.forceLinkOutgoing.create(g.ctx, g.st, g.data, linkOutgoing)
		}
	}
	if g.isForceCenterUpdateNeeded && g.forceCenter != nil {
		g.forceCenter.create(g.ctx)
	}
	if g.isPointClusterUpdateNeeded {
		g.clusters.create()
	}

	g.isPointPositionsUpdateNeeded = false
	g.isPointColorUpdateNeeded = false
	g.isPointSizeUpdateNeeded = false
	g.isPointShapeUpdateNeeded = false
	g.isPointImageIndicesUpdateNeeded = false
	g.isPointImageSizesUpdateNeeded = false
	g.isLinksUpdateNeeded = false
	g.isLinkColorUpdateNeeded = false
	g.isLinkWidthUpdateNeeded = false
	g.isLinkArrowUpdateNeeded = false
	g.isPointClusterUpdateNeeded = false
	g.isForceManyBodyUpdateNeeded = false
	g.isForceLinkUpdateNeeded = false
	g.isForceCenterUpdateNeeded = false
}

func (g *Graph) update(simulationAlpha float64) {
	g.st.pointsTextureSize = int(math.Ceil(math.Sqrt(float64(g.data.pointsNumber()))))
	g.st.linksTextureSize = int(math.Ceil(math.Sqrt(float64(g.data.linksNumber() * 2))))
	g.create()
	if err := g.initPrograms(); err != nil {
		consoleWarn(err.Error())
	}
	g.st.hoveredPoint = nil
	g.st.alpha = simulationAlpha
}

// runSimulationStep runs one step of the simulation (forces, position
// updates, alpha decay). forceExecution runs the step even when paused.
func (g *Graph) runSimulationStep(forceExecution bool) {
	cfg, st := g.cfg, g.st
	if !cfg.EnableSimulation {
		return
	}

	// right-click repulsion (runs regardless of isSimulationRunning)
	if g.isRightClickMouse && cfg.EnableRightClickRepulsion {
		g.forceMouse.run()
		g.points.updatePosition()
	}

	shouldRunSimulation := forceExecution ||
		(st.isSimulationRunning && !(g.zoom.isRunning && !cfg.EnableSimulationDuringZoom))

	if shouldRunSimulation {
		if cfg.SimulationGravity != 0 {
			g.forceGravity.run()
			g.points.updatePosition()
		}

		if cfg.SimulationCenter != 0 {
			g.forceCenter.run()
			g.points.updatePosition()
		}

		g.forceManyBody.run()
		g.points.updatePosition()

		if st.linksTextureSize > 0 {
			g.forceLinkIncoming.run()
			g.points.updatePosition()
			g.forceLinkOutgoing.run()
			g.points.updatePosition()
		}

		if g.data.pointClusters != nil || g.data.clusterPositions != nil {
			g.clusters.run()
			g.points.updatePosition()
		}

		st.alpha += st.addAlpha(cfg.SimulationDecay)
		if g.isRightClickMouse && cfg.EnableRightClickRepulsion {
			st.alpha = math.Max(st.alpha, 0.1)
		}
		st.simulationProgress = math.Sqrt(math.Min(1, alphaMin/st.alpha))

		if cfg.OnSimulationTick != nil {
			index := -1
			var position [2]float64
			if st.hoveredPoint != nil {
				index = st.hoveredPoint.index
				position = st.hoveredPoint.position
			}
			cfg.OnSimulationTick(st.alpha, index, position)
		}
	}

	// track points (runs regardless of simulation state)
	g.points.trackPoints()
}

func (g *Graph) initPrograms() error {
	quadAttr := []attrBinding{{name: "vertexCoord", buffer: func() *buffer { return g.points.quadBuffer }, size: 2}}
	indexAttr := []attrBinding{{name: "pointIndices", buffer: func() *buffer { return g.points.indexesBuffer }, size: 2}}

	if err := g.points.initPrograms(); err != nil {
		return err
	}
	if err := g.lines.initPrograms(); err != nil {
		return err
	}
	if g.forceGravity != nil {
		if err := g.forceGravity.initPrograms(g.ctx, g.cfg, g.st, g.points, quadAttr); err != nil {
			return err
		}
	}
	if g.forceLinkIncoming != nil {
		if err := g.forceLinkIncoming.initPrograms(g.ctx, g.cfg, g.st, g.points, quadAttr); err != nil {
			return err
		}
	}
	if g.forceLinkOutgoing != nil {
		if err := g.forceLinkOutgoing.initPrograms(g.ctx, g.cfg, g.st, g.points, quadAttr); err != nil {
			return err
		}
	}
	if g.forceMouse != nil {
		if err := g.forceMouse.initPrograms(g.ctx, g.cfg, g.st, g.points, quadAttr); err != nil {
			return err
		}
	}
	if g.forceManyBody != nil {
		if err := g.forceManyBody.initPrograms(g.ctx, g.cfg, g.st, g.data, g.points, quadAttr, indexAttr); err != nil {
			return err
		}
	}
	if g.forceCenter != nil {
		if err := g.forceCenter.initPrograms(g.ctx, g.cfg, g.st, g.data, g.points, quadAttr, indexAttr); err != nil {
			return err
		}
	}
	if err := g.clusters.initPrograms(quadAttr, indexAttr); err != nil {
		return err
	}
	return nil
}

func (g *Graph) frame() {
	if g.isDestroyed {
		return
	}
	// check if the simulation should end before scheduling the next frame
	if g.st.alpha < alphaMin && g.st.isSimulationRunning {
		g.end()
	}
	g.rafID = js.Global().Call("requestAnimationFrame", g.rafCb)
}

func (g *Graph) onFrame(now float64) {
	g.renderFrame(now)
	if !g.isDestroyed {
		g.frame()
	}
}

func (g *Graph) renderFrame(now float64) {
	if g.isDestroyed || g.st.pointsTextureSize == 0 {
		return
	}
	if g.fps != nil {
		g.fps.frame(now)
	}
	g.resizeCanvas(false)
	g.zoom.tick(now)
	if !g.zoom.dragActive {
		g.findHoveredItem()
	}

	g.runSimulationStep(false)

	// clear canvas
	bg := g.st.backgroundColor
	g.ctx.clearTarget(nil, bg[0], bg[1], bg[2], bg[3])

	if g.cfg.RenderLinks && g.st.linksTextureSize > 0 {
		g.lines.draw()
	}

	g.points.draw()
	if g.zoom.dragActive {
		// run the drag function twice to prevent the dragged point from
		// suddenly jumping
		g.points.drag()
		g.points.drag()
		g.points.trackPoints()
	}
	g.currentEvent = js.Value{}
}

func (g *Graph) stopFrames() {
	if g.rafID.Truthy() {
		js.Global().Call("cancelAnimationFrame", g.rafID)
		g.rafID = js.Value{}
	}
	g.framesRunning = false
}

func (g *Graph) startFrames() {
	if g.isDestroyed {
		return
	}
	g.stopFrames()
	g.framesRunning = true
	g.frame()
}

// end is called automatically when the simulation completes.
func (g *Graph) end() {
	g.st.isSimulationRunning = false
	g.st.simulationProgress = 1
	if g.cfg.OnSimulationEnd != nil {
		g.cfg.OnSimulationEnd()
	}
}

func (g *Graph) onClick(event js.Value) {
	index := -1
	var position [2]float64
	if g.st.hoveredPoint != nil {
		index = g.st.hoveredPoint.index
		position = g.st.hoveredPoint.position
	}
	if g.cfg.OnClick != nil {
		g.cfg.OnClick(index, position, event)
	}
	if g.st.hoveredPoint != nil {
		if g.cfg.OnPointClick != nil {
			g.cfg.OnPointClick(index, position, event)
		}
	} else if g.st.hoveredLinkIndex >= 0 {
		if g.cfg.OnLinkClick != nil {
			g.cfg.OnLinkClick(g.st.hoveredLinkIndex, event)
		}
	} else if g.cfg.OnBackgroundClick != nil {
		g.cfg.OnBackgroundClick(event)
	}
}

func (g *Graph) updateMousePosition(event js.Value) {
	if !event.Truthy() {
		return
	}
	off := event.Get("offsetX")
	if off.IsUndefined() {
		return
	}
	mouseX := off.Float()
	mouseY := event.Get("offsetY").Float()
	g.st.mousePosition = g.zoom.convertScreenToSpacePosition([2]float64{mouseX, mouseY})
	g.st.screenMousePosition = [2]float64{mouseX, g.st.screenSize[1] - mouseY}
}

func (g *Graph) onMouseMove(event js.Value) {
	g.isMouseOnCanvas = true
	g.currentEvent = event
	g.updateMousePosition(event)
	g.isRightClickMouse = event.Get("which").Int() == 3
	if g.cfg.OnMouseMove != nil {
		index := -1
		var position [2]float64
		if g.st.hoveredPoint != nil {
			index = g.st.hoveredPoint.index
			position = g.st.hoveredPoint.position
		}
		g.cfg.OnMouseMove(index, position, g.currentEvent)
	}
}

func (g *Graph) resizeCanvas(forceResize bool) {
	if g.isDestroyed {
		return
	}
	prevWidth := g.canvas.Get("width").Float()
	prevHeight := g.canvas.Get("height").Float()
	w := g.canvas.Get("clientWidth").Float()
	h := g.canvas.Get("clientHeight").Float()

	if forceResize || prevWidth != w*g.cfg.PixelRatio || prevHeight != h*g.cfg.PixelRatio {
		prevW := g.st.screenSize[0]
		prevH := g.st.screenSize[1]
		k := g.zoom.eventTransform.k
		centerPosition := g.zoom.convertScreenToSpacePosition([2]float64{prevW / 2, prevH / 2})

		g.st.updateScreenSize(w, h)
		g.canvas.Set("width", w*g.cfg.PixelRatio)
		g.canvas.Set("height", h*g.cfg.PixelRatio)
		transform := g.zoom.getTransform([][2]float64{centerPosition}, k, true, 0.1)
		g.zoom.transformTo(transform, 0, nil)
		g.points.updateSampledPointsGrid()
		if g.st.isLinkHoveringEnabled {
			g.lines.updateLinkIndexFbo()
		}
	}
}

func (g *Graph) setZoomTransformByPointPositions(positions []float64, duration float64, scale float64, padding float64) {
	g.resizeCanvas(false)
	pairs := make([][2]float64, len(positions)/2)
	for i := range pairs {
		pairs[i] = [2]float64{positions[i*2], positions[i*2+1]}
	}
	transform := g.zoom.getTransform(pairs, scale, !math.IsNaN(scale), padding)
	g.zoom.transformTo(transform, duration, easeQuadInOut)
}

// EnableZoom / DisableZoom toggle wheel zooming (panning stays available,
// matching the original behavior).
func (g *Graph) EnableZoom() {
	g.cfg.EnableZoom = true
	g.zoom.wheelEnabled = true
}

func (g *Graph) DisableZoom() {
	g.cfg.EnableZoom = false
	g.zoom.wheelEnabled = false
}

func (g *Graph) findHoveredItem() {
	if g.isDestroyed || !g.isMouseOnCanvas {
		return
	}
	if g.findHoveredItemExecutionCount < maxHoverDetectionDelay {
		g.findHoveredItemExecutionCount++
		return
	}
	g.findHoveredItemExecutionCount = 0
	g.findHoveredPoint()

	if g.data.linksNumber() > 0 && g.st.isLinkHoveringEnabled {
		g.findHoveredLine()
	} else if g.st.hoveredLinkIndex >= 0 {
		g.st.hoveredLinkIndex = -1
		if g.cfg.OnLinkMouseOut != nil {
			g.cfg.OnLinkMouseOut(g.currentEvent)
		}
	}

	g.updateCanvasCursor()
}

func (g *Graph) findHoveredPoint() {
	if g.points.hoveredFbo == nil {
		return
	}
	g.points.findHoveredPoint()
	isMouseover := false
	isMouseout := false
	pixels := g.points.hoveredFbo.readPixels()
	pointSize := float64(pixels[1])
	if pointSize != 0 {
		hoveredIndex := int(pixels[0])
		if g.st.hoveredPoint == nil || g.st.hoveredPoint.index != hoveredIndex {
			isMouseover = true
		}
		g.st.hoveredPoint = &hoveredPoint{
			index:    hoveredIndex,
			position: [2]float64{float64(pixels[2]), float64(pixels[3])},
		}
	} else {
		if g.st.hoveredPoint != nil {
			isMouseout = true
		}
		g.st.hoveredPoint = nil
	}

	if isMouseover && g.st.hoveredPoint != nil && g.cfg.OnPointMouseOver != nil {
		g.cfg.OnPointMouseOver(g.st.hoveredPoint.index, g.st.hoveredPoint.position, g.currentEvent)
	}
	if isMouseout && g.cfg.OnPointMouseOut != nil {
		g.cfg.OnPointMouseOut(g.currentEvent)
	}
}

func (g *Graph) findHoveredLine() {
	if g.st.hoveredPoint != nil {
		if g.st.hoveredLinkIndex >= 0 {
			g.st.hoveredLinkIndex = -1
			if g.cfg.OnLinkMouseOut != nil {
				g.cfg.OnLinkMouseOut(g.currentEvent)
			}
		}
		return
	}
	g.lines.findHoveredLine()
	if g.lines.hoveredLineIndexFbo == nil {
		return
	}
	isMouseover := false
	isMouseout := false
	pixels := g.lines.hoveredLineIndexFbo.readPixels()
	hoveredLineIndex := int(pixels[0])
	if hoveredLineIndex >= 0 {
		if g.st.hoveredLinkIndex != hoveredLineIndex {
			isMouseover = true
		}
		g.st.hoveredLinkIndex = hoveredLineIndex
	} else {
		if g.st.hoveredLinkIndex >= 0 {
			isMouseout = true
		}
		g.st.hoveredLinkIndex = -1
	}

	if isMouseover && g.st.hoveredLinkIndex >= 0 && g.cfg.OnLinkMouseOver != nil {
		g.cfg.OnLinkMouseOver(g.st.hoveredLinkIndex)
	}
	if isMouseout && g.cfg.OnLinkMouseOut != nil {
		g.cfg.OnLinkMouseOut(g.currentEvent)
	}
}

func (g *Graph) updateCanvasCursor() {
	style := g.canvas.Get("style")
	switch {
	case g.zoom.dragActive:
		style.Call("setProperty", "cursor", "grabbing")
	case g.st.hoveredPoint != nil:
		if !g.cfg.EnableDrag || g.st.isSpaceKeyPressed {
			style.Call("setProperty", "cursor", g.cfg.HoveredPointCursor)
		} else {
			style.Call("setProperty", "cursor", "grab")
		}
	case g.st.isLinkHoveringEnabled && g.st.hoveredLinkIndex >= 0:
		style.Call("setProperty", "cursor", g.cfg.HoveredLinkCursor)
	default:
		style.Call("removeProperty", "cursor")
	}
}

// addAttribution adds the attribution text (plain text; unlike the
// original no HTML is injected).
func (g *Graph) addAttribution() {
	if g.cfg.Attribution == "" {
		return
	}
	document := js.Global().Get("document")
	el := document.Call("createElement", "div")
	el.Get("style").Set("cssText",
		"user-select: none; position: absolute; bottom: 0; right: 0; "+
			"color: #888; margin: 0 0.6rem 0.6rem 0; font-size: 0.7rem; font-family: inherit;")
	el.Set("textContent", g.cfg.Attribution)
	g.div.Call("appendChild", el)
}

func setTimeout(fn func(), ms float64) js.Value {
	var f js.Func
	f = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		fn()
		f.Release()
		return nil
	})
	return js.Global().Call("setTimeout", f, ms)
}
