//go:build js && wasm

package ui

import (
	"strings"
	"syscall/js"
)

// App is the main application
type App struct {
	canvas  *Canvas
	input   *InputHandler
	graph   *Graph
	view    *View
	opts    *RenderOptions
	fetcher *DataFetcher

	// Interaction state - canvas panning
	dragging    bool
	dragStartX  float64
	dragStartY  float64
	dragOffsetX float64
	dragOffsetY float64

	// Interaction state - node dragging
	dragNode           *Node
	isDragThresholdMet bool

	selectedNode *Node
	hoveredNode  *Node

	// Edge visibility - matches JS showNodeEdgesOnly behavior
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
	physicsAdapter    *PhysicsAdapter

	// Rendering
	needsRedraw bool
	frameCount  int

	// Visible world bounds (for culling, recalculated each frame)
	visMinX, visMinY, visMaxX, visMaxY float64

	// Auto-refresh
	autoRefresh      bool
	refreshCountdown int
	refreshInterval  int
	lastRefreshFrame int

	// Local visor
	localVisorPK   string
	localVisorData *LocalVisorData

	// DMSG data
	dmsgData    *DMSGData
	dmsgEntries map[string]bool

	// Apps data
	appsData []AppState

	// Clustering options
	ipGroupsData     *IPGroupsData
	ipGroupsEnabled  bool
	clusterByIP      bool
	clusterByCountry bool
	clusterColors    []string

	// Group boundaries for rendering
	countryBoundaries          map[string]GroupBoundary
	ipGroupBoundaries          map[int]GroupBoundary
	boundaryEnforcementEnabled bool

	// Satellite orbit state (for unknown-country nodes)
	satelliteOrbits  map[string]*SatelliteOrbit
	orbitCenter      Point
	orbitBaseRadius  float64
	satelliteEnabled bool

	// Display options
	showFlags       bool
	showDMSGServers bool

	// Globe view
	globeRenderer *GlobeRenderer
	globeActive   bool

	// Pick modes (matching TypeScript state.ts)
	pingPickMode     bool
	localTpPickMode  bool
	tpsPickMode      string // "target" or "remote"

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

	graph := NewGraph()
	view := NewView(canvas.Width(), canvas.Height())
	opts := NewRenderOptions()

	app := &App{
		canvas:            canvas,
		input:             input,
		graph:             graph,
		view:              view,
		opts:              opts,
		fetcher:           NewDataFetcher(),
		physicsEnabled:    true,
		physicsAdapter:    NewPhysicsAdapter(),
		searchMatches:     make(map[string]bool),
		dmsgEntries:       make(map[string]bool),
		clusterByIP:       true,
		clusterByCountry:  true,
		showFlags:         true,
		showDMSGServers:   true,
		countryBoundaries: make(map[string]GroupBoundary),
		ipGroupBoundaries: make(map[int]GroupBoundary),
		clusterColors:     DefaultClusterColors,
		globeActive:       true, // Globe is default view
	}

	// Initialize globe renderer
	app.globeRenderer = NewGlobeRenderer(canvas, graph, view, opts)
	app.globeRenderer.SetActive(true)

	return app
}

// Run starts the main loop
func (a *App) Run() {
	a.needsRedraw = true
	a.setupSidebarCallbacks()

	a.frameCallback = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		a.update()
		a.draw()
		a.needsRedraw = false
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
		query := GetValue("search")
		a.handleSearch(query)
		return nil
	})
	el := getElement("search")
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

	// Focus local visor button
	a.jsCallbacks = append(a.jsCallbacks, RegisterGlobalFunc("focusLocalVisor", func(args []js.Value) {
		if a.localVisorPK != "" {
			a.FocusNode(a.localVisorPK)
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

	// Filter checkboxes
	filterIDs := []string{
		"show-stcpr", "show-sudph", "show-dmsg",
		"show-online", "show-offline", "show-unknown",
		"show-services", "show-flags", "toggle-physics",
	}
	for _, id := range filterIDs {
		fid := id
		a.jsCallbacks = append(a.jsCallbacks, OnEvent(fid, "change", func() {
			a.syncFilters()
			a.needsRedraw = true
		}))
	}

	// Fit to screen button
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("btn-fit", "click", func() {
		a.FitToScreen()
		a.needsRedraw = true
	}))

	// Search input
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("search", "input", func() {
		query := GetValue("search")
		if len(query) >= 4 {
			a.searchAndFocus(query)
		}
	}))

	// Cluster by Country checkbox - apply circle packing
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("cluster-country", "change", func() {
		a.clusterByCountry = GetChecked("cluster-country")
		if a.dataLoaded {
			a.applyCirclePackingLayout()
			a.physicsEnabled = true
			a.stabilizationLeft = 50
		}
		a.needsRedraw = true
	}))

	// Cluster by IP checkbox - apply circle packing
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("cluster-ip", "change", func() {
		a.clusterByIP = GetChecked("cluster-ip")
		if a.dataLoaded {
			a.applyCirclePackingLayout()
			a.physicsEnabled = true
			a.stabilizationLeft = 50
		}
		a.needsRedraw = true
	}))

	// Show DMSG servers checkbox
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("show-dmsg-servers", "change", func() {
		a.showDMSGServers = GetChecked("show-dmsg-servers")
		a.needsRedraw = true
	}))

	// Initialize filter state from checkboxes
	a.syncFilters()
	a.clusterByCountry = GetChecked("cluster-country")
	a.clusterByIP = GetChecked("cluster-ip")
	a.showDMSGServers = GetChecked("show-dmsg-servers")
	a.showFlags = GetChecked("show-flags")

	// View toggle buttons (Globe/Flat)
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("view-globe", "click", func() {
		a.globeActive = true
		if a.globeRenderer != nil {
			a.globeRenderer.SetActive(true)
		}
		viewGlobeBtn := getElement("view-globe")
		viewFlatBtn := getElement("view-flat")
		if !viewGlobeBtn.IsNull() {
			viewGlobeBtn.Get("classList").Call("add", "active")
		}
		if !viewFlatBtn.IsNull() {
			viewFlatBtn.Get("classList").Call("remove", "active")
		}
		a.needsRedraw = true
	}))
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("view-flat", "click", func() {
		a.globeActive = false
		if a.globeRenderer != nil {
			a.globeRenderer.SetActive(false)
		}
		viewGlobeBtn := getElement("view-globe")
		viewFlatBtn := getElement("view-flat")
		if !viewFlatBtn.IsNull() {
			viewFlatBtn.Get("classList").Call("add", "active")
		}
		if !viewGlobeBtn.IsNull() {
			viewGlobeBtn.Get("classList").Call("remove", "active")
		}
		a.needsRedraw = true
	}))

	// Start JavaScript-based intervals for more reliable timing
	// Local visor polling every 1 second
	localVisorInterval := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		if a.dataLoaded {
			go a.pollLocalVisor()
		}
		return nil
	})
	js.Global().Call("setInterval", localVisorInterval, 1000)
	a.jsCallbacks = append(a.jsCallbacks, localVisorInterval)

	// Auto-refresh countdown every 1 second
	countdownInterval := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		if a.autoRefresh && a.refreshInterval > 0 {
			a.refreshCountdown--
			if a.refreshCountdown <= 0 {
				a.refreshCountdown = a.refreshInterval
				go a.LoadData()
			}
			SetText("refresh-text", "Refresh in "+itoa(a.refreshCountdown)+"s")
			SetText("cache-info", "Auto-refresh in: "+itoa(a.refreshCountdown)+"s")
		}
		return nil
	})
	js.Global().Call("setInterval", countdownInterval, 1000)
	a.jsCallbacks = append(a.jsCallbacks, countdownInterval)

	// Boundary enforcement interval every 30ms (matching TypeScript grouping.ts)
	// This keeps nodes inside their cluster circles continuously
	// Also animates satellites for unknown-country nodes
	boundaryInterval := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		if a.dataLoaded && a.boundaryEnforcementEnabled && (a.clusterByCountry || a.clusterByIP) {
			a.enforceBoundaries()
			a.needsRedraw = true
		}
		// Animate satellites (unknown-country nodes orbiting clusters)
		if a.dataLoaded && a.satelliteEnabled {
			a.animateSatellites()
		}
		return nil
	})
	js.Global().Call("setInterval", boundaryInterval, 30)
	a.jsCallbacks = append(a.jsCallbacks, boundaryInterval)

	// ── App control functions (matching TypeScript apps.ts) ──

	// appStart - start an application
	a.jsCallbacks = append(a.jsCallbacks, RegisterGlobalFunc("appStart", func(args []js.Value) {
		if len(args) > 0 {
			name := args[0].String()
			go func() {
				err := a.fetcher.StartApp(name)
				if err != nil {
					a.showAppMessage("Failed to start "+name+": "+err.Error(), "error")
				} else {
					a.showAppMessage(name+" started", "success")
					a.refreshApps()
				}
			}()
		}
	}))

	// appStop - stop an application
	a.jsCallbacks = append(a.jsCallbacks, RegisterGlobalFunc("appStop", func(args []js.Value) {
		if len(args) > 0 {
			name := args[0].String()
			go func() {
				err := a.fetcher.StopApp(name)
				if err != nil {
					a.showAppMessage("Failed to stop "+name+": "+err.Error(), "error")
				} else {
					a.showAppMessage(name+" stopped", "success")
					a.refreshApps()
				}
			}()
		}
	}))

	// appToggleAutoStart - toggle auto-start for an application
	a.jsCallbacks = append(a.jsCallbacks, RegisterGlobalFunc("appToggleAutoStart", func(args []js.Value) {
		if len(args) > 1 {
			name := args[0].String()
			autoStart := args[1].Bool()
			go func() {
				err := a.fetcher.SetAppAutoStart(name, autoStart)
				if err != nil {
					a.showAppMessage("Failed to update auto-start: "+err.Error(), "error")
				} else {
					state := "disabled"
					if autoStart {
						state = "enabled"
					}
					a.showAppMessage(name+" auto-start "+state, "success")
					a.refreshApps()
				}
			}()
		}
	}))

	// appSetPK - set server PK for an application
	a.jsCallbacks = append(a.jsCallbacks, RegisterGlobalFunc("appSetPK", func(args []js.Value) {
		if len(args) > 0 {
			name := args[0].String()
			pkInput := getElement("pk-" + name)
			if !pkInput.IsNull() && !pkInput.IsUndefined() {
				pk := pkInput.Get("value").String()
				if pk != "" {
					go func() {
						err := a.fetcher.SetAppPK(name, pk)
						if err != nil {
							a.showAppMessage("Failed to set PK: "+err.Error(), "error")
						} else {
							a.showAppMessage(name+" server PK updated", "success")
							a.refreshApps()
						}
					}()
				}
			}
		}
	}))

	// ── Ping functions (matching TypeScript ping.ts) ──

	// performPing - perform a network ping
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("ping-btn", "click", func() {
		go a.performPing()
	}))

	// Ping pick mode button
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("ping-pick-btn", "click", func() {
		a.pingPickMode = !a.pingPickMode
		if a.pingPickMode {
			js.Global().Get("document").Get("body").Get("style").Set("cursor", "crosshair")
		} else {
			js.Global().Get("document").Get("body").Get("style").Set("cursor", "")
		}
	}))

	// Ping DMSG checkbox - update local route visibility
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("ping-use-dmsg", "change", func() {
		a.updateLocalRouteVisibility()
	}))

	// ── Local transport functions (matching TypeScript tps.ts localCreateTransport) ──

	// localCreateTransport - create a transport from the local visor
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("local-tp-create-btn", "click", func() {
		go a.localCreateTransport()
	}))

	// Local transport pick mode button
	a.jsCallbacks = append(a.jsCallbacks, OnEvent("local-tp-pick-btn", "click", func() {
		a.localTpPickMode = !a.localTpPickMode
		if a.localTpPickMode {
			js.Global().Get("document").Get("body").Get("style").Set("cursor", "crosshair")
		} else {
			js.Global().Get("document").Get("body").Get("style").Set("cursor", "")
		}
	}))
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
	if a.input.IsKeyJustPressed("g") || a.input.IsKeyJustPressed("G") {
		a.toggleGlobeView()
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("Escape") {
		a.deselectNode()
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("i") || a.input.IsKeyJustPressed("I") {
		if a.ipGroupsEnabled {
			a.clusterByIP = !a.clusterByIP
			el := getElement("cluster-ip")
			if !el.IsNull() && !el.IsUndefined() {
				el.Set("checked", a.clusterByIP)
			}
			a.applyCirclePackingLayout()
			a.needsRedraw = true
		}
	}

	// Mouse wheel zoom
	if a.input.WheelDelta != 0 {
		a.view.Zoom(a.input.WheelDelta, a.input.MouseX, a.input.MouseY)
		a.needsRedraw = true
	}

	// Globe view mouse handling
	if a.globeActive && a.globeRenderer != nil {
		if a.input.MouseDown && a.dragging {
			dx := a.input.MouseX - a.dragStartX
			dy := a.input.MouseY - a.dragStartY
			a.globeRenderer.Rotate(dx*0.5, dy*0.5)
			a.dragStartX = a.input.MouseX
			a.dragStartY = a.input.MouseY
			a.needsRedraw = true
		}
		return // Skip flat view update logic when in globe mode
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
			a.dragNode = node
			a.isDragThresholdMet = false
			a.input.DragStartX = a.input.MouseX
			a.input.DragStartY = a.input.MouseY
		} else {
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
				a.dragNode.IsPinned = true
			} else {
				// Check pick modes first (matching TypeScript event handling)
				if a.handlePingNodeClick(a.dragNode.ID) {
					// Ping pick mode handled the click
				} else if a.handleLocalTpNodeClick(a.dragNode.ID) {
					// Local transport pick mode handled the click
				} else if a.selectedNode == a.dragNode {
					a.deselectNode()
				} else {
					a.selectNode(a.dragNode)
				}
			}
			a.dragNode = nil
			a.isDragThresholdMet = false
			a.needsRedraw = true
		} else if a.dragging {
			// Check if this was a click (no significant movement) on empty space
			dx := a.input.MouseX - a.dragStartX
			dy := a.input.MouseY - a.dragStartY
			if dx*dx+dy*dy < 25 { // Less than 5 pixels movement = click
				// Click on empty space - deselect (matching TypeScript hideNodeInfo on empty click)
				a.deselectNode()
				a.needsRedraw = true
			}
		}
		a.dragging = false
	}

	// Physics stabilization
	if a.physicsEnabled && a.dataLoaded && a.stabilizationLeft > 0 {
		a.runPhysics()
		a.enforceBoundaries()
		a.stabilizationLeft--
		a.needsRedraw = true
		if a.stabilizationLeft == 0 {
			// First fit after initial stabilization (matching TypeScript stabilizationIterationsDone)
			a.view.FitToGraph(a.graph, 50)
			a.physicsEnabled = false

			// Schedule a second fit after 100ms for grouping to settle
			// (matching TypeScript grouping.ts setTimeout -> fit() pattern)
			if a.clusterByCountry || a.clusterByIP {
				js.Global().Call("setTimeout", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
					a.view.FitToGraph(a.graph, 50)
					a.needsRedraw = true
					return nil
				}), 100)
			}
		}
	}
}

// syncFilters reads checkbox states into render options
func (a *App) syncFilters() {
	a.opts.ShowSTCPR = GetChecked("show-stcpr")
	a.opts.ShowSUDPH = GetChecked("show-sudph")
	a.opts.ShowDMSG = GetChecked("show-dmsg")
	a.opts.ShowOnline = GetChecked("show-online")
	a.opts.ShowOffline = GetChecked("show-offline")
	a.opts.ShowUnknown = GetChecked("show-unknown")
	a.opts.HighlightServices = GetChecked("show-services")
	a.showFlags = GetChecked("show-flags")

	if GetChecked("toggle-physics") {
		if !a.physicsEnabled && a.dataLoaded {
			a.physicsEnabled = true
			a.stabilizationLeft = 50
		}
	} else {
		a.physicsEnabled = false
		a.stabilizationLeft = 0
	}
}

// searchAndFocus searches for a node by PK prefix and focuses on it
func (a *App) searchAndFocus(query string) {
	query = strings.ToLower(query)
	for _, node := range a.graph.Nodes {
		if strings.HasPrefix(strings.ToLower(node.ID), query) {
			a.FocusNode(node.ID)
			return
		}
	}
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
	el := getElement("selected-info")
	if !el.IsNull() && !el.IsUndefined() {
		el.Get("classList").Call("remove", "visible")
	}
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

func (a *App) FitToScreen() {
	a.view.FitToGraph(a.graph, 50)
}

// runPhysics runs one iteration of the physics simulation using the vis-network
// compatible Barnes-Hut physics engine
func (a *App) runPhysics() {
	if a.physicsAdapter == nil {
		return
	}

	nodeCount := len(a.graph.Nodes)
	if nodeCount == 0 {
		return
	}

	// Update the physics engine with current graph state
	a.physicsAdapter.UpdateFromGraph(a.graph)

	// Configure physics based on clustering mode
	// Matches TypeScript events.ts lines 160-170
	clustering := a.clusterByCountry || a.clusterByIP
	a.physicsAdapter.Configure(clustering)

	// Run one physics step
	a.physicsAdapter.Step()
}

// ── Ping functions (matching TypeScript ping.ts) ──

// performPing performs a network ping to a remote visor (matching TypeScript ping.ts performPing)
func (a *App) performPing() {
	pkInput := getElement("ping-pk")
	if pkInput.IsNull() || pkInput.IsUndefined() {
		return
	}
	pk := strings.TrimSpace(pkInput.Get("value").String())

	useDmsgCheckbox := getElement("ping-use-dmsg")
	useDmsg := true
	if !useDmsgCheckbox.IsNull() && !useDmsgCheckbox.IsUndefined() {
		useDmsg = useDmsgCheckbox.Get("checked").Bool()
	}

	triesInput := getElement("ping-tries")
	tries := 3
	if !triesInput.IsNull() && !triesInput.IsUndefined() {
		triesStr := triesInput.Get("value").String()
		if t, err := parseInt(triesStr); err == nil && t > 0 {
			tries = t
		}
	}

	localRouteCheckbox := getElement("ping-local-route")
	localRoute := false
	if !localRouteCheckbox.IsNull() && !localRouteCheckbox.IsUndefined() {
		localRoute = localRouteCheckbox.Get("checked").Bool()
	}

	mode := "dmsg"
	if !useDmsg {
		mode = "route"
	}

	resultEl := getElement("ping-result")
	if resultEl.IsNull() || resultEl.IsUndefined() {
		return
	}

	// Validate PK
	if pk == "" || len(pk) != 66 {
		resultEl.Get("style").Set("display", "block")
		resultEl.Get("style").Set("background", "#e94560")
		resultEl.Get("style").Set("color", "#fff")
		resultEl.Set("textContent", "Invalid PK (must be 66 chars)")
		return
	}

	// Show loading state
	resultEl.Get("style").Set("display", "block")
	resultEl.Get("style").Set("background", "#0f3460")
	resultEl.Get("style").Set("color", "#00d9a5")

	modeText := strings.ToUpper(mode)
	if mode == "route" && localRoute {
		modeText = "ROUTE (local calc)"
	}
	resultEl.Set("textContent", "Pinging via "+modeText+"...")

	// Perform ping
	resp, err := a.fetcher.Ping(pk, mode, tries, localRoute)
	if err != nil {
		resultEl.Get("style").Set("background", "#e94560")
		resultEl.Get("style").Set("color", "#fff")
		resultEl.Set("textContent", "Failed: "+err.Error())
		return
	}

	a.updatePingResult(resp, mode)
}

// handlePingNodeClick handles node click when in ping pick mode (matching TypeScript ping.ts handlePingNodeClick)
func (a *App) handlePingNodeClick(nodeID string) bool {
	if !a.pingPickMode {
		return false
	}

	// Strip dmsg-srv- prefix if clicking a DMSG server node
	pk := nodeID
	if strings.HasPrefix(nodeID, "dmsg-srv-") {
		pk = nodeID[9:]
	}

	a.setPingPK(pk)
	return true
}

// setPingPK sets the ping target PK from the graph selection (matching TypeScript ping.ts setPingPK)
func (a *App) setPingPK(pk string) {
	pkInput := getElement("ping-pk")
	if !pkInput.IsNull() && !pkInput.IsUndefined() {
		pkInput.Set("value", pk)
	}
	a.pingPickMode = false
	js.Global().Get("document").Get("body").Get("style").Set("cursor", "")
}

// ── Local transport functions (matching TypeScript tps.ts) ──

// localCreateTransport creates a transport from the local visor (matching TypeScript tps.ts localCreateTransport)
func (a *App) localCreateTransport() {
	remoteInput := getElement("local-tp-remote-pk")
	if remoteInput.IsNull() || remoteInput.IsUndefined() {
		return
	}
	remotePK := strings.TrimSpace(remoteInput.Get("value").String())

	typeSelect := getElement("local-tp-type")
	tpType := "stcpr"
	if !typeSelect.IsNull() && !typeSelect.IsUndefined() {
		tpType = typeSelect.Get("value").String()
	}

	if remotePK == "" {
		a.updateLocalTPResult("error", "Remote PK required")
		return
	}

	a.updateLocalTPResult("warning", "Creating transport...")

	// Disable button
	createBtn := getElement("local-tp-create-btn")
	if !createBtn.IsNull() && !createBtn.IsUndefined() {
		createBtn.Set("disabled", true)
		createBtn.Set("textContent", "...")
	}

	resp, err := a.fetcher.LocalAddTransport(remotePK, tpType)

	// Re-enable button
	btn := getElement("local-tp-create-btn")
	if !btn.IsNull() && !btn.IsUndefined() {
		btn.Set("disabled", false)
		btn.Set("textContent", "Create")
	}

	if err != nil {
		a.updateLocalTPResult("error", "Error: "+err.Error())
		return
	}

	a.updateLocalTPResult("success", "Created: "+strings.ToUpper(resp.Type)+" → "+resp.RemotePK[:16]+"...")

	// Clear input after success
	input := getElement("local-tp-remote-pk")
	if !input.IsNull() && !input.IsUndefined() {
		input.Set("value", "")
	}

	// Refresh local visor data
	go a.pollLocalVisor()
}

// updateLocalTPResult updates the local transport result element (matching TypeScript tps.ts updateLocalTPResult)
func (a *App) updateLocalTPResult(style, message string) {
	el := getElement("local-tp-result")
	if el.IsNull() || el.IsUndefined() {
		return
	}

	el.Get("style").Set("display", "block")
	switch style {
	case "error":
		el.Get("style").Set("background", "rgba(233,69,96,0.2)")
		el.Get("style").Set("color", "#e94560")
	case "warning":
		el.Get("style").Set("background", "rgba(255,209,102,0.2)")
		el.Get("style").Set("color", "#ffd166")
	default: // success
		el.Get("style").Set("background", "rgba(0,217,165,0.2)")
		el.Get("style").Set("color", "#00d9a5")
	}
	el.Set("textContent", message)
}

// handleLocalTpNodeClick handles node click when in local transport pick mode
func (a *App) handleLocalTpNodeClick(nodeID string) bool {
	if !a.localTpPickMode {
		return false
	}

	// Strip dmsg-srv- prefix if clicking a DMSG server node
	pk := nodeID
	if strings.HasPrefix(nodeID, "dmsg-srv-") {
		pk = nodeID[9:]
	}

	input := getElement("local-tp-remote-pk")
	if !input.IsNull() && !input.IsUndefined() {
		input.Set("value", pk)
	}
	a.localTpPickMode = false
	js.Global().Get("document").Get("body").Get("style").Set("cursor", "")
	return true
}

// ── App management functions ──

// refreshApps refreshes the apps list
func (a *App) refreshApps() {
	apps, err := a.fetcher.FetchApps()
	if err != nil {
		return
	}
	a.appsData = apps
	a.updateAppsSection()
}

// toggleGlobeView toggles between globe and flat views
func (a *App) toggleGlobeView() {
	a.globeActive = !a.globeActive
	if a.globeRenderer != nil {
		a.globeRenderer.SetActive(a.globeActive)
	}

	// Update UI buttons
	viewGlobeBtn := getElement("view-globe")
	viewFlatBtn := getElement("view-flat")

	if !viewGlobeBtn.IsNull() && !viewFlatBtn.IsNull() {
		if a.globeActive {
			viewGlobeBtn.Get("classList").Call("add", "active")
			viewFlatBtn.Get("classList").Call("remove", "active")
		} else {
			viewFlatBtn.Get("classList").Call("add", "active")
			viewGlobeBtn.Get("classList").Call("remove", "active")
		}
	}
}

// parseInt parses an integer from a string
func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
