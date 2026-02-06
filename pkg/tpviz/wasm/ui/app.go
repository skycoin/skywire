//go:build js && wasm

package ui

import (
	"math"
	"strings"
	"syscall/js"
)

// Colors - matching vis-network JavaScript UI
const (
	ColorBackground = "#1a1a2e"
	// Node colors (same as JS getNodeColor)
	ColorOnlineBg      = "#00d9a5"
	ColorOnlineBorder  = "#00b386"
	ColorOfflineBg     = "#e94560"
	ColorOfflineBorder = "#ffffff"
	ColorUnknownBg     = "#ffd166"
	ColorUnknownBorder = "#ccaa52"
	// Local visor (cyan with magenta border)
	ColorLocalVisorBg     = "#00ffff"
	ColorLocalVisorBorder = "#ff00ff"
	// Service colors (matching JS)
	ColorVPNBg       = "#9f6efc"
	ColorVPNBorder   = "#7c3aed"
	ColorProxyBg     = "#ffa500"
	ColorProxyBorder = "#cc8400"
	// Selection/hover
	ColorSelected = "#e94560"
	ColorHovered  = "#ff6b6b"
	// Transport edge colors (from JS colors object)
	ColorSTCPR = "#00d9a5" // stcpr
	ColorSUDPH = "#00b4d8" // sudph
	ColorDMSG  = "#ffd166" // dmsg
	// Local edge color (cyan, from LOCAL_EDGE_COLOR)
	ColorLocalEdge = "#00ffff"
	// Dim colors
	ColorEdgeDim       = "rgba(100, 100, 100, 0.3)"
	ColorText          = "#ffffff"
	ColorTextDim       = "#aaaaaa"
	ColorTooltipBg     = "rgba(22, 33, 62, 0.95)"
	ColorTooltipBorder = "#0f3460"
)

// App is the main application
type App struct {
	canvas  *Canvas
	input   *InputHandler
	graph   *Graph
	view    *View
	opts    *RenderOptions
	fetcher *DataFetcher

	// Interaction state — canvas panning
	dragging    bool
	dragStartX  float64
	dragStartY  float64
	dragOffsetX float64
	dragOffsetY float64

	// Interaction state — node dragging
	dragNode           *Node
	isDragThresholdMet bool

	selectedNode *Node
	hoveredNode  *Node

	// Edge visibility — matches JS showNodeEdgesOnly behavior
	edgeVisibilityNode string

	// Search state
	searchQuery   string
	searchMatches map[string]bool

	// Data state
	dataLoaded bool
	loadError  string

	// Physics
	physicsEnabled    bool
	stabilizationLeft int

	// Rendering
	needsRedraw bool
	frameCount  int

	// Visible world bounds (for culling, recalculated each frame)
	visMinX, visMinY, visMaxX, visMaxY float64

	// Auto-refresh
	autoRefresh      bool
	refreshCountdown int // seconds until next refresh
	refreshInterval  int // seconds between refreshes
	lastRefreshFrame int

	// Local visor
	localVisorPK string

	// Animation frame callback
	frameCallback js.Func

	// Stored JS callbacks for cleanup
	jsCallbacks []js.Func
}

// NewApp creates a new application
func NewApp(canvasID string) *App {
	canvas := NewCanvas(canvasID)
	if canvas == nil {
		return nil
	}

	input := NewInputHandler(canvasID)

	app := &App{
		canvas:         canvas,
		input:          input,
		graph:          NewGraph(),
		view:           NewView(canvas.Width(), canvas.Height()),
		opts:           NewRenderOptions(),
		fetcher:        NewDataFetcher(),
		physicsEnabled: true,
		searchMatches:  make(map[string]bool),
	}

	return app
}

// Run starts the main loop
func (a *App) Run() {
	a.needsRedraw = true
	a.setupSidebarCallbacks()

	a.frameCallback = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		a.update()
		if a.needsRedraw {
			a.draw()
			a.needsRedraw = false
		}
		a.input.Update()
		js.Global().Call("requestAnimationFrame", a.frameCallback)
		return nil
	})

	js.Global().Call("requestAnimationFrame", a.frameCallback)
}

// setupSidebarCallbacks registers event listeners for sidebar elements
func (a *App) setupSidebarCallbacks() {
	// Close button for node info panel
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("node-info-close", "click", func() {
		a.deselectNode()
		a.needsRedraw = true
	}))

	// Search input
	searchFn := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		query := GetValue("search-input")
		a.handleSearch(query)
		return nil
	})
	el := getElement("search-input")
	if !el.IsNull() && !el.IsUndefined() {
		el.Call("addEventListener", "input", searchFn)
	}
	a.jsCallbacks = append(a.jsCallbacks, searchFn)

	// Visor list click handler
	a.jsCallbacks = append(a.jsCallbacks, RegisterGlobalFunc("focusVisor", func(args []js.Value) {
		if len(args) > 0 {
			a.FocusNode(args[0].String())
		}
	}))

	// Refresh button
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("refresh-now", "click", func() {
		go a.LoadData()
	}))

	// Zoom controls
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("zoom-in", "click", func() {
		a.view.Zoom(3, a.canvas.Width()/2, a.canvas.Height()/2)
		a.needsRedraw = true
	}))
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("zoom-out", "click", func() {
		a.view.Zoom(-3, a.canvas.Width()/2, a.canvas.Height()/2)
		a.needsRedraw = true
	}))
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("zoom-fit", "click", func() {
		a.FitToScreen()
		a.needsRedraw = true
	}))

	// Filter checkboxes — event-driven instead of polling every frame
	filterIDs := []string{
		"filter-stcpr", "filter-sudph", "filter-dmsg",
		"filter-online", "filter-offline", "filter-unknown",
		"filter-labels", "filter-services",
	}
	for _, id := range filterIDs {
		fid := id // capture
		a.jsCallbacks = append(a.jsCallbacks, OnEvent(fid, "change", func() {
			a.syncFilters()
			a.needsRedraw = true
		}))
	}
	// Initialize filter state from checkboxes
	a.syncFilters()
}

func (a *App) update() {
	a.frameCount++

	// Check canvas size changes (throttled)
	if a.frameCount%30 == 0 {
		container := js.Global().Get("document").Call("getElementById", "canvas-container")
		if !container.IsNull() && !container.IsUndefined() {
			newWidth := container.Get("clientWidth").Int()
			newHeight := container.Get("clientHeight").Int()
			if newWidth > 0 && newHeight > 0 {
				if float64(newWidth) != a.canvas.Width() || float64(newHeight) != a.canvas.Height() {
					a.canvas.Resize(newWidth, newHeight)
					a.view.Width = float64(newWidth)
					a.view.Height = float64(newHeight)
					a.needsRedraw = true
				}
			}
		}
	}

	// Handle keyboard
	if a.input.IsKeyJustPressed("f") || a.input.IsKeyJustPressed("F") {
		a.FitToScreen()
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("r") || a.input.IsKeyJustPressed("R") {
		go a.LoadData()
	}
	if a.input.IsKeyJustPressed("p") || a.input.IsKeyJustPressed("P") {
		if !a.physicsEnabled {
			a.physicsEnabled = true
			a.stabilizationLeft = 100
		} else {
			a.physicsEnabled = false
			a.stabilizationLeft = 0
		}
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("l") || a.input.IsKeyJustPressed("L") {
		a.opts.ShowLabels = !a.opts.ShowLabels
		el := getElement("filter-labels")
		if !el.IsNull() && !el.IsUndefined() {
			el.Set("checked", a.opts.ShowLabels)
		}
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("Escape") {
		a.deselectNode()
		a.needsRedraw = true
	}

	// Mouse wheel zoom
	if a.input.WheelDelta != 0 {
		a.view.Zoom(a.input.WheelDelta, a.input.MouseX, a.input.MouseY)
		a.needsRedraw = true
	}

	// World coordinates of mouse
	worldX, worldY := a.view.ScreenToWorld(a.input.MouseX, a.input.MouseY)

	// Hover detection
	newHovered := a.graph.GetNodeAt(worldX, worldY, a.view.Scale)
	if newHovered != a.hoveredNode {
		if a.hoveredNode != nil {
			a.hoveredNode.IsHovered = false
		}
		if newHovered != nil {
			newHovered.IsHovered = true
		}
		a.hoveredNode = newHovered
		a.opts.HoveredNode = newHovered
		// Edge visibility: hover controls edges when nothing is selected
		if a.selectedNode == nil {
			if newHovered != nil {
				a.edgeVisibilityNode = newHovered.ID
			} else {
				a.edgeVisibilityNode = ""
			}
		}
		a.needsRedraw = true
	}

	// Mouse click
	if a.input.MouseJustPressed {
		node := a.graph.GetNodeAt(worldX, worldY, a.view.Scale)
		if node != nil {
			// Start potential node drag — don't select yet (click vs drag)
			a.dragNode = node
			a.isDragThresholdMet = false
			a.input.DragStartX = a.input.MouseX
			a.input.DragStartY = a.input.MouseY
		} else {
			// Canvas pan
			a.dragging = true
			a.dragStartX = a.input.MouseX
			a.dragStartY = a.input.MouseY
			a.dragOffsetX = a.view.OffsetX
			a.dragOffsetY = a.view.OffsetY
		}
	}

	// Node dragging
	if a.input.MouseDown && a.dragNode != nil {
		dx := a.input.MouseX - a.input.DragStartX
		dy := a.input.MouseY - a.input.DragStartY
		if !a.isDragThresholdMet && (dx*dx+dy*dy) > 25 {
			a.isDragThresholdMet = true
		}
		if a.isDragThresholdMet {
			a.dragNode.X = worldX
			a.dragNode.Y = worldY
			a.needsRedraw = true
		}
	}

	// Canvas panning
	if a.input.MouseDown && a.dragging {
		a.view.OffsetX = a.dragOffsetX + (a.input.MouseX - a.dragStartX)
		a.view.OffsetY = a.dragOffsetY + (a.input.MouseY - a.dragStartY)
		a.needsRedraw = true
	}

	if a.input.MouseJustUp {
		if a.dragNode != nil {
			if a.isDragThresholdMet {
				// Finished dragging — pin node
				a.dragNode.IsPinned = true
			} else {
				// Click — select/toggle
				if a.selectedNode == a.dragNode {
					a.deselectNode()
				} else {
					a.selectNode(a.dragNode)
				}
			}
			a.dragNode = nil
			a.isDragThresholdMet = false
			a.needsRedraw = true
		}
		a.dragging = false
	}

	// Auto-refresh countdown (update every ~60 frames ≈ 1 second)
	if a.autoRefresh && a.refreshInterval > 0 && a.frameCount%60 == 0 {
		a.refreshCountdown--
		if a.refreshCountdown <= 0 {
			a.refreshCountdown = a.refreshInterval
			go a.LoadData()
		}
		SetText("refresh-text", "Refresh in "+itoa(a.refreshCountdown)+"s")
	}

	// Physics stabilization — disable physics after iterations complete (matches JS behavior)
	if a.physicsEnabled && a.dataLoaded && a.stabilizationLeft > 0 {
		a.runPhysics()
		a.stabilizationLeft--
		a.needsRedraw = true
		if a.stabilizationLeft == 0 {
			a.view.FitToGraph(a.graph, 50)
			a.physicsEnabled = false
		}
	}
}

// syncFilters reads checkbox states into render options
func (a *App) syncFilters() {
	a.opts.ShowSTCPR = GetChecked("filter-stcpr")
	a.opts.ShowSUDPH = GetChecked("filter-sudph")
	a.opts.ShowDMSG = GetChecked("filter-dmsg")
	a.opts.ShowOnline = GetChecked("filter-online")
	a.opts.ShowOffline = GetChecked("filter-offline")
	a.opts.ShowUnknown = GetChecked("filter-unknown")
	a.opts.ShowLabels = GetChecked("filter-labels")
	a.opts.HighlightServices = GetChecked("filter-services")
}

// selectNode sets the selected node and updates the sidebar
func (a *App) selectNode(node *Node) {
	if a.selectedNode != nil {
		a.selectedNode.IsSelected = false
	}
	node.IsSelected = true
	a.selectedNode = node
	a.opts.SelectedNode = node
	a.edgeVisibilityNode = node.ID
	a.updateNodeInfo(node)
}

// deselectNode clears the selection
func (a *App) deselectNode() {
	if a.selectedNode != nil {
		a.selectedNode.IsSelected = false
	}
	a.selectedNode = nil
	a.opts.SelectedNode = nil
	a.edgeVisibilityNode = ""
	SetVisible("node-info-panel", false)
}

func (a *App) updateNodeInfo(node *Node) {
	SetVisible("node-info-panel", true)
	SetText("node-info-pk", node.ID)

	statusText, statusColor := "Unknown", ColorUnknownBg
	switch node.Status {
	case StatusOnline:
		statusText, statusColor = "Online", ColorOnlineBg
	case StatusOffline:
		statusText, statusColor = "Offline", ColorOfflineBg
	}
	SetText("node-info-status", statusText)
	dot := getElement("node-info-status-dot")
	if !dot.IsNull() && !dot.IsUndefined() {
		dot.Get("style").Set("background", statusColor)
	}

	SetText("node-info-transports", itoa(node.ConnectionCount))
	SetText("node-info-stcpr", itoa(node.STCPRCount))
	SetText("node-info-sudph", itoa(node.SUDPHCount))
	SetText("node-info-dmsg", itoa(node.DMSGCount))

	country := node.Country
	if country == "" {
		country = "-"
	}
	SetText("node-info-country", country)

	version := node.Version
	if version == "" {
		version = "-"
	}
	SetText("node-info-version", version)

	svcText := "-"
	switch node.Service {
	case ServiceVPN:
		svcText = "VPN"
	case ServiceProxy:
		svcText = "Proxy"
	}
	if node.IsLocalVisor {
		svcText = "Local Visor"
	}
	SetText("node-info-services", svcText)
}

func (a *App) handleSearch(query string) {
	a.searchQuery = strings.ToLower(query)
	a.searchMatches = make(map[string]bool)

	if len(a.searchQuery) >= 4 {
		for _, node := range a.graph.Nodes {
			if strings.HasPrefix(strings.ToLower(node.ID), a.searchQuery) {
				a.searchMatches[node.ID] = true
			}
		}
		if len(a.searchMatches) == 1 {
			for pk := range a.searchMatches {
				a.FocusNode(pk)
			}
		}
	}
	a.needsRedraw = true
}

// FocusNode centers the view on a node and selects it
func (a *App) FocusNode(pk string) {
	node, ok := a.graph.Nodes[pk]
	if !ok {
		return
	}

	a.selectNode(node)
	if a.view.Scale < 0.5 {
		a.view.Scale = 0.8
	}
	a.view.OffsetX = a.view.Width/2 - node.X*a.view.Scale
	a.view.OffsetY = a.view.Height/2 - node.Y*a.view.Scale
	a.needsRedraw = true
}

// --- Drawing ---

func (a *App) draw() {
	a.canvas.Clear(ColorBackground)

	if !a.dataLoaded {
		msg := "Loading transport data..."
		if a.loadError != "" {
			msg = "Error: " + a.loadError
		}
		a.canvas.Text(msg, a.canvas.Width()/2-100, a.canvas.Height()/2, ColorText, "14px sans-serif")
		return
	}

	// Calculate visible world bounds for culling
	pad := 50.0 / a.view.Scale
	a.visMinX = -a.view.OffsetX/a.view.Scale - pad
	a.visMinY = -a.view.OffsetY/a.view.Scale - pad
	a.visMaxX = (a.canvas.Width()-a.view.OffsetX)/a.view.Scale + pad
	a.visMaxY = (a.canvas.Height()-a.view.OffsetY)/a.view.Scale + pad

	// Phase 1: World-space rendering (canvas transform handles zoom scaling)
	// This is how vis-network renders — line widths, node sizes all in world units,
	// so they naturally get thinner/smaller when zoomed out → softer appearance
	a.canvas.Save()
	a.canvas.Translate(a.view.OffsetX, a.view.OffsetY)
	a.canvas.SetScale(a.view.Scale, a.view.Scale)

	a.drawEdges()
	a.drawNodeCircles()

	a.canvas.Restore()

	// Phase 2: Screen-space rendering (text, overlays — constant size regardless of zoom)
	a.drawNodeLabels()
	a.drawTooltip()
}

func (a *App) drawEdges() {
	hasSearch := len(a.searchMatches) > 0

	for _, edge := range a.graph.Edges {
		if edge.Hidden || !a.opts.ShowEdgeType(edge.Type) {
			continue
		}

		fromNode, ok := a.graph.Nodes[edge.From]
		if !ok {
			continue
		}
		toNode, ok := a.graph.Nodes[edge.To]
		if !ok {
			continue
		}

		if !a.opts.ShowNodeStatus(fromNode.Status) || !a.opts.ShowNodeStatus(toNode.Status) {
			continue
		}

		// Edge hiding: when a node is hovered/selected, hide unconnected edges entirely (JS showNodeEdgesOnly)
		if a.edgeVisibilityNode != "" && !hasSearch {
			if edge.From != a.edgeVisibilityNode && edge.To != a.edgeVisibilityNode {
				continue
			}
		}

		// World-space coordinates
		x1, y1 := fromNode.X, fromNode.Y
		x2, y2 := toNode.X, toNode.Y

		// Cull off-screen edges using world bounds
		if (x1 < a.visMinX && x2 < a.visMinX) || (x1 > a.visMaxX && x2 > a.visMaxX) ||
			(y1 < a.visMinY && y2 < a.visMinY) || (y1 > a.visMaxY && y2 > a.visMaxY) {
			continue
		}

		edgeColor := a.edgeColor(edge.Type)
		lineWidth := 1.0 // world units — gets thinner when zoomed out (matching vis-network)
		opacity := 0.6

		// Local visor edges
		isLocalEdge := fromNode.IsLocalVisor || toNode.IsLocalVisor
		if isLocalEdge {
			edgeColor = ColorLocalEdge
			lineWidth = 3.0
			opacity = 1.0
		}

		// Search highlighting
		if hasSearch {
			fromMatch := a.searchMatches[edge.From]
			toMatch := a.searchMatches[edge.To]
			if !fromMatch && !toMatch {
				edgeColor = ColorEdgeDim
				opacity = 0.15
			} else {
				opacity = 1.0
				lineWidth = 2.0
			}
		}

		// Connected edges get full opacity when a node is focused
		if a.edgeVisibilityNode != "" && !hasSearch {
			lineWidth = 2.0
			opacity = 1.0
		}

		a.canvas.SetGlobalAlpha(opacity)
		a.canvas.QuadraticCurve(x1, y1, x2, y2, lineWidth, edgeColor)
		a.canvas.ResetGlobalAlpha()
	}
}

func (a *App) drawNodeCircles() {
	hasSearch := len(a.searchMatches) > 0

	for _, node := range a.graph.Nodes {
		if !a.opts.ShowNodeStatus(node.Status) {
			continue
		}

		x, y := node.X, node.Y
		size := node.Size // world units — canvas transform handles visual scaling

		// Cull using world bounds
		if x+size < a.visMinX || x-size > a.visMaxX ||
			y+size < a.visMinY || y-size > a.visMaxY {
			continue
		}

		// Dim nodes not matching search
		if hasSearch && !a.searchMatches[node.ID] {
			a.canvas.SetGlobalAlpha(0.15)
		}

		bgColor, borderColor := a.nodeColors(node)

		// Border width in world units (matching JS: local=4, offline=3, default=2)
		borderWidth := 2.0
		if node.IsLocalVisor {
			borderWidth = 4.0
		} else if node.Status == StatusOffline {
			borderWidth = 3.0
		}

		// Glow for local visor
		if node.IsLocalVisor {
			a.canvas.SetGlobalAlpha(0.3)
			a.canvas.FillCircle(x, y, size*1.8, ColorLocalVisorBg)
			a.canvas.ResetGlobalAlpha()
			// Re-apply search dim if needed
			if hasSearch && !a.searchMatches[node.ID] {
				a.canvas.SetGlobalAlpha(0.15)
			}
		}

		// Node fill
		a.canvas.FillCircle(x, y, size, bgColor)

		// Border
		a.canvas.StrokeCircle(x, y, size, borderWidth, borderColor)

		if hasSearch && !a.searchMatches[node.ID] {
			a.canvas.ResetGlobalAlpha()
		}
	}
}

// drawNodeLabels renders text labels in screen space (constant size regardless of zoom)
func (a *App) drawNodeLabels() {
	for _, node := range a.graph.Nodes {
		if !a.opts.ShowNodeStatus(node.Status) {
			continue
		}

		sx, sy := a.view.WorldToScreen(node.X, node.Y)
		screenSize := node.Size * a.view.Scale

		// Cull
		if sx < -50 || sx > a.canvas.Width()+50 ||
			sy < -50 || sy > a.canvas.Height()+50 {
			continue
		}

		// Dim labels for search misses
		hasSearch := len(a.searchMatches) > 0
		if hasSearch && !a.searchMatches[node.ID] {
			continue // skip labels for dimmed nodes
		}

		// Local visor always shows "⬢ YOU" label with stroke for readability
		if node.IsLocalVisor {
			label := "\u2b22 YOU"
			font := "bold 13px sans-serif"
			lx, ly := sx+screenSize+4, sy+4
			a.canvas.StrokeText(label, lx, ly, "#000000", 3, font)
			a.canvas.Text(label, lx, ly, ColorLocalVisorBg, font)
		} else if a.opts.ShowLabels && a.view.Scale > 0.3 {
			label := node.Label
			if label == "" {
				label = shortPK(node.ID)
			}
			a.canvas.Text(label, sx+screenSize+4, sy+4, ColorTextDim, "10px sans-serif")
		} else if a.view.Scale > 1.0 {
			// Auto-show labels when zoomed in close
			a.canvas.Text(shortPK(node.ID), sx+screenSize+4, sy+4, ColorTextDim, "9px sans-serif")
		}
	}
}

// drawTooltip renders a tooltip near the hovered node in screen space
func (a *App) drawTooltip() {
	if a.hoveredNode == nil || a.hoveredNode == a.selectedNode {
		return
	}

	node := a.hoveredNode
	sx, sy := a.view.WorldToScreen(node.X, node.Y)

	lines := []string{shortPK(node.ID)}

	switch node.Status {
	case StatusOnline:
		lines = append(lines, "Online")
	case StatusOffline:
		lines = append(lines, "Offline")
	default:
		lines = append(lines, "Unknown")
	}

	lines = append(lines, "Transports: "+itoa(node.ConnectionCount))

	if node.Country != "" {
		lines = append(lines, "Country: "+node.Country)
	}
	if node.Version != "" {
		lines = append(lines, "Version: "+node.Version)
	}

	font := "11px sans-serif"
	lineHeight := 16.0
	padding := 8.0
	tooltipW := 0.0
	for _, line := range lines {
		w := a.canvas.MeasureText(line, font)
		if w > tooltipW {
			tooltipW = w
		}
	}
	tooltipW += padding * 2
	tooltipH := float64(len(lines))*lineHeight + padding*2

	tx := sx + node.Size*a.view.Scale + 10
	ty := sy - tooltipH/2
	if tx+tooltipW > a.canvas.Width()-10 {
		tx = sx - node.Size*a.view.Scale - tooltipW - 10
	}
	if ty < 10 {
		ty = 10
	}
	if ty+tooltipH > a.canvas.Height()-10 {
		ty = a.canvas.Height() - tooltipH - 10
	}

	a.canvas.RoundedRect(tx, ty, tooltipW, tooltipH, 4, ColorTooltipBg)
	a.canvas.StrokeRoundedRect(tx, ty, tooltipW, tooltipH, 4, 1, ColorTooltipBorder)

	textY := ty + padding + 12
	for _, line := range lines {
		a.canvas.Text(line, tx+padding, textY, ColorText, font)
		textY += lineHeight
	}
}

// --- Node colors ---

func (a *App) nodeColors(node *Node) (bg, border string) {
	// Selection/hover highlight changes fill color (matching JS vis-network behavior)
	if node.IsSelected || node.IsHovered {
		if node.IsLocalVisor {
			return ColorLocalVisorBg, "#ffffff"
		}
		return ColorSelected, ColorHovered
	}

	if node.IsLocalVisor {
		return ColorLocalVisorBg, ColorLocalVisorBorder
	}

	if a.opts.HighlightServices && node.HasServices {
		switch node.Service {
		case ServiceVPN:
			return ColorVPNBg, ColorVPNBorder
		case ServiceProxy:
			return ColorProxyBg, ColorProxyBorder
		}
	}

	switch node.Status {
	case StatusOnline:
		return ColorOnlineBg, ColorOnlineBorder
	case StatusOffline:
		return ColorOfflineBg, ColorOfflineBorder
	default:
		return ColorUnknownBg, ColorUnknownBorder
	}
}

func (a *App) edgeColor(t TransportType) string {
	switch t {
	case TransportSTCPR:
		return ColorSTCPR
	case TransportSUDPH:
		return ColorSUDPH
	case TransportDMSG:
		return ColorDMSG
	default:
		return ColorUnknownBg
	}
}

// --- Data loading ---

func (a *App) LoadData() {
	transports, err := a.fetcher.FetchTransports()
	if err != nil {
		a.loadError = err.Error()
		a.needsRedraw = true
		return
	}

	uptimes, _ := a.fetcher.FetchUptimes()
	services, _ := a.fetcher.FetchServices()

	isFirstLoad := !a.dataLoaded

	graph := ProcessTransports(transports, uptimes, services)

	if isFirstLoad {
		graph.RandomizePositions(a.view.Width/a.view.Scale, a.view.Height/a.view.Scale)
	} else {
		// Preserve existing node positions on refresh
		for id, newNode := range graph.Nodes {
			if oldNode, ok := a.graph.Nodes[id]; ok {
				newNode.X = oldNode.X
				newNode.Y = oldNode.Y
				newNode.VX = 0
				newNode.VY = 0
				newNode.IsPinned = oldNode.IsPinned
				newNode.IsSelected = oldNode.IsSelected
			}
		}
	}

	a.graph = graph
	a.dataLoaded = true
	a.loadError = ""
	a.needsRedraw = true

	if isFirstLoad {
		a.stabilizationLeft = 100
		a.physicsEnabled = true
		a.view.FitToGraph(a.graph, 50)
	} else {
		// Short stabilization on refresh to settle new nodes, then disable again
		a.stabilizationLeft = 30
		a.physicsEnabled = true
	}

	// Update sidebar
	a.updateSidebarStats()
	a.updateVisorList()

	// Fetch supplementary data (non-blocking failures)
	a.fetchHealthInfo()
	a.fetchLocalVisor()
	a.fetchTPSStatus()
}

func (a *App) fetchHealthInfo() {
	health, err := a.fetcher.FetchHealth()
	if err != nil {
		return
	}
	if health.AutoRefresh && health.NextRefreshIn > 0 {
		a.autoRefresh = true
		a.refreshInterval = health.NextRefreshIn
		a.refreshCountdown = health.NextRefreshIn
		SetVisible("refresh-bar", true)
		SetText("refresh-text", "Refresh in "+itoa(a.refreshCountdown)+"s")
	}
}

func (a *App) fetchLocalVisor() {
	lv, err := a.fetcher.FetchLocalVisor()
	if err != nil || lv.PubKey == "" {
		return
	}

	a.localVisorPK = lv.PubKey
	SetVisible("section-local-visor", true)
	SetText("local-visor-pk", lv.PubKey)

	if lv.Connected {
		SetText("local-visor-status", "Connected")
		dot := getElement("local-visor-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOnlineBg)
		}
	} else {
		SetText("local-visor-status", "Disconnected")
		dot := getElement("local-visor-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOfflineBg)
		}
	}

	SetText("local-visor-transports", itoa(len(lv.Transports)))
	SetText("local-visor-routes", itoa(len(lv.Routes)))

	// Mark local visor node in graph
	if node, ok := a.graph.Nodes[lv.PubKey]; ok {
		node.IsLocalVisor = true
		if node.Size < 20 {
			node.Size = 20
		}
		a.needsRedraw = true
	}
}

func (a *App) fetchTPSStatus() {
	tps, err := a.fetcher.FetchTPSStatus()
	if err != nil {
		return
	}

	SetVisible("section-tps", true)

	if tps.Running {
		SetText("tps-status", "Running")
		dot := getElement("tps-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOnlineBg)
		}
		if tps.TPSPK != "" {
			SetVisible("tps-pk-row", true)
			SetText("tps-pk", tps.TPSPK)
		}
	} else {
		SetText("tps-status", "Stopped")
		dot := getElement("tps-status-dot")
		if !dot.IsNull() && !dot.IsUndefined() {
			dot.Get("style").Set("background", ColorOfflineBg)
		}
	}
}

func (a *App) updateSidebarStats() {
	online, offline, unknown := a.graph.CountByStatus()
	stcpr, sudph, dmsg := a.graph.CountByType()

	SetText("stat-total-transports", itoa(a.graph.EdgeCount()))
	SetText("stat-total-visors", itoa(a.graph.NodeCount()))
	SetText("stat-online", itoa(online))
	SetText("stat-offline", itoa(offline))
	SetText("stat-unknown", itoa(unknown))
	SetText("stat-stcpr", itoa(stcpr))
	SetText("stat-sudph", itoa(sudph))
	SetText("stat-dmsg", itoa(dmsg))
}

func (a *App) updateVisorList() {
	nodes := a.graph.SortedNodes()
	SetText("visor-list-count", "("+itoa(len(nodes))+")")

	var html strings.Builder
	for _, node := range nodes {
		statusColor := ColorUnknownBg
		switch node.Status {
		case StatusOnline:
			statusColor = ColorOnlineBg
		case StatusOffline:
			statusColor = ColorOfflineBg
		}

		pk := node.ID

		html.WriteString(`<div class="visor-item" onclick="focusVisor('`)
		html.WriteString(pk)
		html.WriteString(`')">`)
		html.WriteString(`<div class="visor-status-dot" style="background:`)
		html.WriteString(statusColor)
		html.WriteString(`"></div>`)
		html.WriteString(`<span class="visor-pk">`)
		html.WriteString(shortPK(pk))
		html.WriteString(`</span>`)

		// Service badges
		if node.HasServices {
			html.WriteString(`<span class="visor-services">`)
			switch node.Service {
			case ServiceVPN:
				html.WriteString(`<span class="service-badge vpn">V</span>`)
			case ServiceProxy:
				html.WriteString(`<span class="service-badge proxy">P</span>`)
			}
			html.WriteString(`</span>`)
		}

		// Transport type count badges
		html.WriteString(`<span class="visor-services">`)
		if node.STCPRCount > 0 {
			html.WriteString(`<span class="tp-badge stcpr">S`)
			html.WriteString(itoa(node.STCPRCount))
			html.WriteString(`</span>`)
		}
		if node.SUDPHCount > 0 {
			html.WriteString(`<span class="tp-badge sudph">U`)
			html.WriteString(itoa(node.SUDPHCount))
			html.WriteString(`</span>`)
		}
		if node.DMSGCount > 0 {
			html.WriteString(`<span class="tp-badge dmsg">D`)
			html.WriteString(itoa(node.DMSGCount))
			html.WriteString(`</span>`)
		}
		html.WriteString(`</span>`)

		// Version
		if node.Version != "" {
			html.WriteString(`<span class="visor-version">`)
			html.WriteString(node.Version)
			html.WriteString(`</span>`)
		}

		if node.Country != "" {
			html.WriteString(`<span class="visor-country">`)
			html.WriteString(node.Country)
			html.WriteString(`</span>`)
		}

		html.WriteString(`<span class="visor-count">`)
		html.WriteString(itoa(node.ConnectionCount))
		html.WriteString(`</span></div>`)
	}

	SetHTML("visor-list", html.String())
}

func (a *App) FitToScreen() {
	a.view.FitToGraph(a.graph, 50)
}

// --- Physics ---

func (a *App) runPhysics() {
	const (
		gravitationalConstant = -3000.0
		springConstant        = 0.001
		springLength          = 200.0
		damping               = 0.9
		minVelocity           = 0.1
		maxVelocity           = 50.0
		centralGravity        = 0.3
	)

	for _, node := range a.graph.Nodes {
		if node.IsPinned {
			continue
		}

		fx, fy := 0.0, 0.0

		for _, other := range a.graph.Nodes {
			if other.ID == node.ID {
				continue
			}

			dx := node.X - other.X
			dy := node.Y - other.Y
			distSq := dx*dx + dy*dy
			if distSq < 1 {
				distSq = 1
			}

			dist := math.Sqrt(distSq)
			if dist < 1 {
				dist = 1
			}

			force := -gravitationalConstant / distSq
			fx += (dx / dist) * force
			fy += (dy / dist) * force
		}

		for _, edge := range a.graph.GetEdgesForNode(node.ID) {
			var other *Node
			if edge.From == node.ID {
				other = a.graph.Nodes[edge.To]
			} else {
				other = a.graph.Nodes[edge.From]
			}
			if other == nil {
				continue
			}

			dx := other.X - node.X
			dy := other.Y - node.Y
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist > 0 {
				displacement := dist - springLength
				force := springConstant * displacement
				fx += (dx / dist) * force
				fy += (dy / dist) * force
			}
		}

		dist := math.Sqrt(node.X*node.X + node.Y*node.Y)
		if dist > 0 {
			fx -= (node.X / dist) * centralGravity * dist
			fy -= (node.Y / dist) * centralGravity * dist
		}

		node.VX = (node.VX + fx) * damping
		node.VY = (node.VY + fy) * damping

		speed := math.Sqrt(node.VX*node.VX + node.VY*node.VY)
		if speed > maxVelocity {
			scale := maxVelocity / speed
			node.VX *= scale
			node.VY *= scale
		}

		if math.Abs(node.VX) > minVelocity || math.Abs(node.VY) > minVelocity {
			node.X += node.VX
			node.Y += node.VY
		}
	}
}
