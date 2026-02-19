//go:build js && wasm

package ui

import (
	"math"
	"math/rand"
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
	localVisorPK   string
	localVisorData *LocalVisorData

	// DMSG data
	dmsgData        *DMSGData
	dmsgEntries     map[string]bool
	showDMSGServers bool

	// IP Groups for clustering
	ipGroupsData    *IPGroupsData
	ipGroupsEnabled bool
	clusterByIP     bool
	clusterColors   []string // Colors for different IP groups

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
		canvas:          canvas,
		input:           input,
		graph:           NewGraph(),
		view:            NewView(canvas.Width(), canvas.Height()),
		opts:            NewRenderOptions(),
		fetcher:         NewDataFetcher(),
		physicsEnabled:  true,
		searchMatches:   make(map[string]bool),
		dmsgEntries:     make(map[string]bool),
		showDMSGServers: true,
		clusterByIP:     false,
		// Distinct colors for IP group clusters (matching TypeScript palette)
		clusterColors: []string{
			"#00d9a5", "#00b4d8", "#ffd166", "#e94560", "#9f6efc",
			"#ff6b6b", "#4ecdc4", "#ffe66d", "#95e1d3", "#f38181",
			"#aa96da", "#fcbad3", "#a8d8ea", "#dcedc1", "#ffd3b6",
		},
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

	// Cluster by IP checkbox
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("cluster-ip", "change", func() {
		a.clusterByIP = GetChecked("cluster-ip")
		a.needsRedraw = true
	}))

	// Show DMSG servers checkbox
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("show-dmsg-servers", "change", func() {
		a.showDMSGServers = GetChecked("show-dmsg-servers")
		a.needsRedraw = true
	}))

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
	// Toggle IP clustering with 'i' key
	if a.input.IsKeyJustPressed("i") || a.input.IsKeyJustPressed("I") {
		if a.ipGroupsEnabled {
			a.clusterByIP = !a.clusterByIP
			el := getElement("cluster-ip")
			if !el.IsNull() && !el.IsUndefined() {
				el.Set("checked", a.clusterByIP)
			}
			a.needsRedraw = true
		}
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

		// Hide DMSG connections if DMSG servers are not shown
		if edge.IsDMSGConnection && !a.showDMSGServers {
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

		// Don't filter DMSG server nodes by status
		if !fromNode.IsDMSGServer && !a.opts.ShowNodeStatus(fromNode.Status) {
			continue
		}
		if !toNode.IsDMSGServer && !a.opts.ShowNodeStatus(toNode.Status) {
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
		dashed := false

		// Route path edges - dashed magenta
		if edge.IsRoutePath {
			edgeColor = ColorRoutePath
			lineWidth = 2.0
			opacity = 0.7
			dashed = true
		}

		// DMSG connection edges - red, thin
		if edge.IsDMSGConnection {
			edgeColor = ColorDMSGConnection
			lineWidth = 0.5
			opacity = 0.4
		}

		// Local visor edges
		if edge.IsLocalEdge || fromNode.IsLocalVisor || toNode.IsLocalVisor {
			edgeColor = ColorLocalEdge
			lineWidth = 3.0
			opacity = 1.0
		}

		// Local-only transports (dashed cyan)
		if edge.IsLocalOnly {
			dashed = true
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

		// Add shadow/glow for local edges
		if edge.IsLocalEdge || fromNode.IsLocalVisor || toNode.IsLocalVisor {
			a.canvas.SetShadow(ColorLocalEdge, 8, 0, 0)
		}

		if dashed {
			a.canvas.DashedQuadraticCurve(x1, y1, x2, y2, lineWidth, edgeColor, 8, 4)
		} else {
			a.canvas.QuadraticCurve(x1, y1, x2, y2, lineWidth, edgeColor)
		}

		a.canvas.ClearShadow()
		a.canvas.ResetGlobalAlpha()
	}
}

func (a *App) drawNodeCircles() {
	hasSearch := len(a.searchMatches) > 0

	for _, node := range a.graph.Nodes {
		// Skip DMSG server nodes based on visibility setting
		if node.IsDMSGServer && !a.showDMSGServers {
			continue
		}

		// Don't filter DMSG server nodes by status
		if !node.IsDMSGServer && !a.opts.ShowNodeStatus(node.Status) {
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
		} else if node.IsDMSGServer {
			borderWidth = 2.0
		} else if node.Status == StatusOffline {
			borderWidth = 3.0
		}

		// Shadow/glow effects for special nodes
		if node.IsLocalVisor {
			// Cyan glow for local visor
			a.canvas.SetShadow(ColorLocalVisorBg, 15, 0, 0)
			a.canvas.FillCircle(x, y, size, bgColor)
			a.canvas.ClearShadow()
		} else if node.IsSelected || node.IsHovered {
			// Red glow for selected/hovered
			a.canvas.SetShadow(ColorSelected, 10, 0, 0)
			a.canvas.FillCircle(x, y, size, bgColor)
			a.canvas.ClearShadow()
		} else if node.IsDMSGServer {
			// Purple glow for DMSG servers
			a.canvas.SetShadow(ColorDMSGServerBg, 8, 0, 0)
			a.canvas.FillCircle(x, y, size, bgColor)
			a.canvas.ClearShadow()
		} else {
			// Normal nodes - slight shadow for depth
			a.canvas.SetShadow("rgba(0,0,0,0.3)", 3, 1, 1)
			a.canvas.FillCircle(x, y, size, bgColor)
			a.canvas.ClearShadow()
		}

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

// DMSG server colors
const (
	ColorDMSGServerBg     = "#9f6efc" // Purple for DMSG servers
	ColorDMSGServerBorder = "#7c3aed"
	ColorDMSGConnection   = "#e94560" // Red for DMSG connections
	ColorRoutePath        = "#ff00ff" // Magenta for route paths
)

func (a *App) nodeColors(node *Node) (bg, border string) {
	// Selection/hover highlight changes fill color (matching JS vis-network behavior)
	if node.IsSelected || node.IsHovered {
		if node.IsLocalVisor {
			return ColorLocalVisorBg, "#ffffff"
		}
		if node.IsDMSGServer {
			return ColorDMSGServerBg, "#ffffff"
		}
		return ColorSelected, ColorHovered
	}

	if node.IsLocalVisor {
		return ColorLocalVisorBg, ColorLocalVisorBorder
	}

	if node.IsDMSGServer {
		return ColorDMSGServerBg, ColorDMSGServerBorder
	}

	if node.IsRouteDestination {
		return ColorRoutePath, "#ffffff"
	}

	// IP group clustering - use group color if enabled
	if a.clusterByIP && a.ipGroupsEnabled && node.IPGroup > 0 {
		colorIdx := node.IPGroup % len(a.clusterColors)
		groupColor := a.clusterColors[colorIdx]
		return groupColor, darkenColor(groupColor)
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

// darkenColor returns a darker version of a hex color for borders
func darkenColor(hexColor string) string {
	// Simple approach: just return a fixed darker version
	// In practice, you'd parse the hex and reduce RGB values
	switch hexColor {
	case "#00d9a5":
		return "#00b386"
	case "#00b4d8":
		return "#0096b4"
	case "#ffd166":
		return "#ccaa52"
	case "#e94560":
		return "#c13a50"
	case "#9f6efc":
		return "#7c3aed"
	default:
		return "#666666"
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
		// More iterations for initial layout to properly separate nodes
		a.stabilizationLeft = 200
		a.physicsEnabled = true
		a.view.FitToGraph(a.graph, 50)
	} else {
		// Short stabilization on refresh to settle new nodes, then disable again
		a.stabilizationLeft = 50
		a.physicsEnabled = true
	}

	// Update sidebar
	a.updateSidebarStats()
	a.updateVisorList()

	// Fetch supplementary data (non-blocking failures)
	a.fetchHealthInfo()
	a.fetchLocalVisor()
	a.fetchTPSStatus()
	a.fetchDMSG()
	a.fetchIPGroups()
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
	a.localVisorData = lv
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

	// Calculate total traffic
	var totalSent, totalRecv int64
	for _, tp := range lv.Transports {
		totalSent += tp.SentBytes
		totalRecv += tp.RecvBytes
	}
	SetText("local-visor-sent", formatBytes(totalSent))
	SetText("local-visor-recv", formatBytes(totalRecv))

	// Update transport list
	a.updateLocalTransportList()

	// Add route path edges
	a.addRoutePathEdges()

	// Mark local visor node in graph
	if node, ok := a.graph.Nodes[lv.PubKey]; ok {
		node.IsLocalVisor = true
		if node.Size < 20 {
			node.Size = 20
		}
		a.needsRedraw = true
	}

	// Mark local edges
	a.updateLocalEdgeStyling()
}

func (a *App) updateLocalTransportList() {
	if a.localVisorData == nil {
		return
	}

	var html strings.Builder

	if len(a.localVisorData.Transports) == 0 {
		html.WriteString(`<div style="color:#555;font-size:0.8em;padding:8px;text-align:center;">No transports established</div>`)
	} else {
		for _, tp := range a.localVisorData.Transports {
			// Transport type color
			tpColor := ColorUnknownBg
			switch tp.Type {
			case "stcpr":
				tpColor = ColorSTCPR
			case "sudph":
				tpColor = ColorSUDPH
			case "dmsg":
				tpColor = ColorDMSG
			}

			html.WriteString(`<div style="padding:3px 0;border-bottom:1px solid #0f3460;cursor:pointer;font-size:0.75em;" onclick="focusVisor('`)
			html.WriteString(tp.RemotePK)
			html.WriteString(`')">`)
			html.WriteString(`<span style="color:` + tpColor + `;font-weight:bold;">` + tp.Type + `</span>`)
			html.WriteString(` → ` + shortPK(tp.RemotePK) + `...`)
			html.WriteString(`<span style="color:#666;float:right;">↑` + formatBytes(tp.SentBytes) + ` ↓` + formatBytes(tp.RecvBytes) + `</span>`)
			html.WriteString(`</div>`)
		}
	}

	SetHTML("local-transport-list", html.String())
}

func (a *App) addRoutePathEdges() {
	if a.localVisorData == nil {
		return
	}

	for _, route := range a.localVisorData.Routes {
		if route.NextHopPK != "" && route.DstPK != "" && route.NextHopPK != route.DstPK {
			// Create route destination node if it doesn't exist
			if _, exists := a.graph.Nodes[route.DstPK]; !exists {
				node := &Node{
					ID:                 route.DstPK,
					Size:               10,
					Status:             StatusUnknown,
					Label:              shortPK(route.DstPK),
					IsRouteDestination: true,
				}
				a.graph.AddNode(node)
				// Position near the next hop if it exists
				if hopNode, ok := a.graph.Nodes[route.NextHopPK]; ok {
					node.X = hopNode.X + (rand.Float64()-0.5)*100
					node.Y = hopNode.Y + (rand.Float64()-0.5)*100
				}
			}

			// Create route path edge
			edgeID := "route-" + shortPK(route.NextHopPK) + "-" + shortPK(route.DstPK)
			if _, exists := a.graph.Edges[edgeID]; !exists {
				edge := &Edge{
					ID:          edgeID,
					From:        route.NextHopPK,
					To:          route.DstPK,
					Type:        TransportDMSG, // Use DMSG type for route paths
					IsRoutePath: true,
				}
				a.graph.AddEdge(edge)
			}
		}
	}

	a.needsRedraw = true
}

func (a *App) updateLocalEdgeStyling() {
	if a.localVisorData == nil || !a.localVisorData.Connected {
		return
	}

	localPK := a.localVisorData.PubKey
	localRemotes := make(map[string]bool)
	for _, tp := range a.localVisorData.Transports {
		localRemotes[tp.RemotePK] = true
	}

	for _, edge := range a.graph.Edges {
		involvesLocal := edge.From == localPK || edge.To == localPK
		remotePK := edge.From
		if edge.From == localPK {
			remotePK = edge.To
		}

		if involvesLocal && localRemotes[remotePK] {
			edge.IsLocalEdge = true
		}
	}
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return itoa(int(bytes)) + "B"
	} else if bytes < 1024*1024 {
		return ftoa(float64(bytes)/1024) + "KB"
	} else if bytes < 1024*1024*1024 {
		return ftoa(float64(bytes)/(1024*1024)) + "MB"
	}
	return ftoa(float64(bytes)/(1024*1024*1024)) + "GB"
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

func (a *App) fetchDMSG() {
	dmsg, err := a.fetcher.FetchDMSG()
	if err != nil {
		return
	}

	a.dmsgData = dmsg

	// Build entries lookup
	a.dmsgEntries = make(map[string]bool)
	for _, pk := range dmsg.Entries {
		a.dmsgEntries[pk] = true
	}

	// Update DMSG section in sidebar
	SetVisible("section-dmsg", true)
	SetText("dmsg-server-count", itoa(len(dmsg.Servers)))
	SetText("dmsg-entry-count", itoa(dmsg.EntriesCount))

	// Calculate total sessions
	totalSessions := 0
	for _, srv := range dmsg.Servers {
		totalSessions += srv.AvailableSessions
	}
	SetText("dmsg-total-sessions", itoa(totalSessions))

	// Add DMSG servers to graph
	a.addDMSGServersToGraph()
	a.updateDMSGServerList()
}

func (a *App) addDMSGServersToGraph() {
	if a.dmsgData == nil || !a.showDMSGServers {
		return
	}

	// Find max clients for size scaling
	maxClients := 1
	for _, srv := range a.dmsgData.Servers {
		if len(srv.Clients) > maxClients {
			maxClients = len(srv.Clients)
		}
	}

	// Build set of existing visor node IDs for edge targets
	visorNodeIDs := make(map[string]bool)
	for id := range a.graph.Nodes {
		if !strings.HasPrefix(id, "dmsg-srv-") {
			visorNodeIDs[id] = true
		}
	}

	for _, srv := range a.dmsgData.Servers {
		if srv.PK == "" {
			continue
		}

		nodeID := "dmsg-srv-" + srv.PK
		clientCount := len(srv.Clients)
		sessions := srv.AvailableSessions

		// Size formula matching visor nodes
		size := 5.0 + (float64(clientCount)/float64(maxClients))*25.0
		if size < 8 {
			size = 8
		}

		// Check if node already exists
		existingNode, exists := a.graph.Nodes[nodeID]
		if exists {
			// Update existing node
			existingNode.DMSGSessions = sessions
			existingNode.DMSGClients = clientCount
			existingNode.Size = size
		} else {
			// Create new DMSG server node
			node := &Node{
				ID:           nodeID,
				Size:         size,
				Status:       StatusUnknown,
				Country:      srv.Country,
				Label:        shortPK(srv.PK),
				IsDMSGServer: true,
				DMSGSessions: sessions,
				DMSGClients:  clientCount,
			}
			a.graph.AddNode(node)

			// Position near center initially
			node.X = (rand.Float64() - 0.5) * 200
			node.Y = (rand.Float64() - 0.5) * 200
		}

		// Add edges to connected clients that exist in graph
		for _, clientPK := range srv.Clients {
			if visorNodeIDs[clientPK] {
				edgeID := "dmsg-conn-" + shortPK(srv.PK) + "-" + shortPK(clientPK)
				if _, exists := a.graph.Edges[edgeID]; !exists {
					edge := &Edge{
						ID:               edgeID,
						From:             nodeID,
						To:               clientPK,
						Type:             TransportDMSG,
						IsDMSGConnection: true,
					}
					a.graph.AddEdge(edge)
				}
			}
		}
	}

	a.needsRedraw = true
}

func (a *App) updateDMSGServerList() {
	if a.dmsgData == nil {
		return
	}

	var html strings.Builder
	html.WriteString(`<table style="width:100%;border-collapse:collapse;">`)
	html.WriteString(`<tr style="color:#888;font-size:0.9em;border-bottom:1px solid #0f3460;">`)
	html.WriteString(`<td style="padding:2px 0;">Server</td>`)
	html.WriteString(`<td style="text-align:right;padding:2px 4px;">Sess</td>`)
	html.WriteString(`<td style="text-align:right;padding:2px 0;">Clients</td>`)
	html.WriteString(`</tr>`)

	for _, srv := range a.dmsgData.Servers {
		sessions := srv.AvailableSessions
		clients := len(srv.Clients)
		shortPK := shortPK(srv.PK)
		nodeID := "dmsg-srv-" + srv.PK

		// Session color based on availability
		sessionColor := ColorOfflineBg
		if sessions > 50 {
			sessionColor = ColorOnlineBg
		} else if sessions > 10 {
			sessionColor = ColorUnknownBg
		}

		// Country flag emoji if available
		flag := countryToFlag(srv.Country)

		html.WriteString(`<tr style="border-bottom:1px solid #0f3460;">`)
		html.WriteString(`<td style="padding:2px 0;">`)
		html.WriteString(`<span title="` + srv.PK + `" style="cursor:pointer;color:#9f6efc;" onclick="focusVisor('` + nodeID + `')">`)
		html.WriteString(flag + " " + shortPK)
		html.WriteString(`</span></td>`)
		html.WriteString(`<td style="text-align:right;padding:2px 4px;color:` + sessionColor + `;">`)
		html.WriteString(itoa(sessions))
		html.WriteString(`</td>`)
		html.WriteString(`<td style="text-align:right;padding:2px 0;color:#6ec8ff;">`)
		html.WriteString(itoa(clients))
		html.WriteString(`</td></tr>`)
	}
	html.WriteString(`</table>`)

	SetHTML("dmsg-server-list", html.String())
}

// countryToFlag converts a country code to a flag emoji
func countryToFlag(country string) string {
	if len(country) != 2 {
		return ""
	}
	country = strings.ToUpper(country)
	// Convert country code to regional indicator symbols
	// A = 🇦, B = 🇧, etc. (Unicode regional indicators start at 0x1F1E6)
	r1 := rune(country[0]) - 'A' + 0x1F1E6
	r2 := rune(country[1]) - 'A' + 0x1F1E6
	return string([]rune{r1, r2})
}

func (a *App) fetchIPGroups() {
	ipGroups, err := a.fetcher.FetchIPGroups()
	if err != nil {
		return
	}

	a.ipGroupsData = ipGroups
	a.ipGroupsEnabled = ipGroups.Enabled && ipGroups.TotalGroups > 1

	// Update sidebar
	if a.ipGroupsEnabled {
		SetVisible("section-ip-groups", true)
		SetText("ip-groups-count", itoa(ipGroups.TotalGroups))
	}

	// Assign IP groups to nodes
	for pk, groupNum := range ipGroups.Groups {
		if node, ok := a.graph.Nodes[pk]; ok {
			node.IPGroup = groupNum
		}
	}

	a.needsRedraw = true
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
	// Barnes-Hut style physics parameters matching vis-network defaults
	// Key: stronger repulsion and longer spring length to prevent overlap
	const (
		gravitationalConstant = -8000.0 // Stronger repulsion (was -3000)
		springConstant        = 0.0005  // Weaker springs (was 0.001)
		springLength          = 300.0   // Longer springs (was 200)
		damping               = 0.85    // More damping for stability
		minVelocity           = 0.1
		maxVelocity           = 40.0
		centralGravity        = 0.15    // Weaker central pull (was 0.3)
		minNodeDistance       = 50.0    // Minimum distance between nodes
	)

	nodeCount := len(a.graph.Nodes)
	if nodeCount == 0 {
		return
	}

	for _, node := range a.graph.Nodes {
		if node.IsPinned {
			continue
		}

		fx, fy := 0.0, 0.0

		// Repulsive force from all other nodes (Barnes-Hut approximation)
		for _, other := range a.graph.Nodes {
			if other.ID == node.ID {
				continue
			}

			dx := node.X - other.X
			dy := node.Y - other.Y
			distSq := dx*dx + dy*dy

			// Add minimum distance to prevent extreme overlap forces
			if distSq < minNodeDistance*minNodeDistance {
				distSq = minNodeDistance * minNodeDistance
				// Add extra random jitter to separate overlapping nodes
				dx += (rand.Float64() - 0.5) * 10
				dy += (rand.Float64() - 0.5) * 10
			}

			dist := math.Sqrt(distSq)
			if dist < 1 {
				dist = 1
			}

			// Repulsion force inversely proportional to distance squared
			force := -gravitationalConstant / distSq
			fx += (dx / dist) * force
			fy += (dy / dist) * force
		}

		// Spring force from connected edges
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
				// Spring force proportional to displacement from rest length
				displacement := dist - springLength
				force := springConstant * displacement
				fx += (dx / dist) * force
				fy += (dy / dist) * force
			}
		}

		// Central gravity - pulls nodes toward center
		dist := math.Sqrt(node.X*node.X + node.Y*node.Y)
		if dist > 0 {
			// Weaker central gravity for more spread
			gravityForce := centralGravity * math.Log(dist+1)
			fx -= (node.X / dist) * gravityForce
			fy -= (node.Y / dist) * gravityForce
		}

		// Update velocity with damping
		node.VX = (node.VX + fx) * damping
		node.VY = (node.VY + fy) * damping

		// Clamp velocity
		speed := math.Sqrt(node.VX*node.VX + node.VY*node.VY)
		if speed > maxVelocity {
			scale := maxVelocity / speed
			node.VX *= scale
			node.VY *= scale
		}

		// Apply velocity if significant
		if math.Abs(node.VX) > minVelocity || math.Abs(node.VY) > minVelocity {
			node.X += node.VX
			node.Y += node.VY
		}
	}
}
