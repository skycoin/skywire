//go:build js && wasm

package ui

import (
	"math"
	"syscall/js"
)

// Colors - matching vis-network JavaScript UI
const (
	ColorBackground = "#1a1a2e"
	// Node colors (same as JS getNodeColor)
	ColorOnlineBg     = "#00d9a5"
	ColorOnlineBorder = "#00b386"
	ColorOfflineBg    = "#e94560"
	ColorOfflineBorder = "#ffffff"
	ColorUnknownBg    = "#ffd166"
	ColorUnknownBorder = "#ccaa52"
	// Local visor (cyan with magenta border)
	ColorLocalVisorBg     = "#00ffff"
	ColorLocalVisorBorder = "#ff00ff"
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
	ColorEdgeDim = "rgba(100, 100, 100, 0.3)"
	ColorText    = "#ffffff"
	ColorTextDim = "#aaaaaa"
)

// App is the main application
type App struct {
	canvas  *Canvas
	input   *InputHandler
	graph   *Graph
	view    *View
	opts    *RenderOptions
	fetcher *DataFetcher

	// Interaction state
	dragging     bool
	dragStartX   float64
	dragStartY   float64
	dragOffsetX  float64
	dragOffsetY  float64
	selectedNode *Node
	hoveredNode  *Node

	// Data state
	dataLoaded bool
	loadError  string

	// Physics - runs briefly then stops
	physicsEnabled    bool
	stabilizationLeft int // iterations remaining

	// Rendering
	needsRedraw bool

	// Animation frame callback
	frameCallback js.Func
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
	}

	return app
}

// Run starts the main loop
func (a *App) Run() {
	a.needsRedraw = true

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

func (a *App) update() {
	// Check canvas size changes
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

	// Handle keyboard
	if a.input.IsKeyJustPressed("f") || a.input.IsKeyJustPressed("F") {
		a.FitToScreen()
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("r") || a.input.IsKeyJustPressed("R") {
		go a.LoadData()
	}
	if a.input.IsKeyJustPressed("p") || a.input.IsKeyJustPressed("P") {
		a.physicsEnabled = !a.physicsEnabled
		if a.physicsEnabled {
			a.stabilizationLeft = 100
		}
		a.needsRedraw = true
	}
	if a.input.IsKeyJustPressed("l") || a.input.IsKeyJustPressed("L") {
		a.opts.ShowLabels = !a.opts.ShowLabels
		a.needsRedraw = true
	}

	// Handle mouse wheel zoom
	if a.input.WheelDelta != 0 {
		a.view.Zoom(a.input.WheelDelta, a.input.MouseX, a.input.MouseY)
		a.needsRedraw = true
	}

	// Convert mouse to world coordinates
	worldX, worldY := a.view.ScreenToWorld(a.input.MouseX, a.input.MouseY)

	// Update hovered node
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
		a.needsRedraw = true
	}

	// Handle mouse click
	if a.input.MouseJustPressed {
		node := a.graph.GetNodeAt(worldX, worldY, a.view.Scale)
		if node != nil {
			// Select node
			if a.selectedNode != nil {
				a.selectedNode.IsSelected = false
			}
			node.IsSelected = true
			a.selectedNode = node
			a.opts.SelectedNode = node
			a.needsRedraw = true
		} else {
			// Start dragging
			a.dragging = true
			a.dragStartX = a.input.MouseX
			a.dragStartY = a.input.MouseY
			a.dragOffsetX = a.view.OffsetX
			a.dragOffsetY = a.view.OffsetY
		}
	}

	// Handle drag
	if a.input.MouseDown && a.dragging {
		a.view.OffsetX = a.dragOffsetX + (a.input.MouseX - a.dragStartX)
		a.view.OffsetY = a.dragOffsetY + (a.input.MouseY - a.dragStartY)
		a.needsRedraw = true
	}

	// Handle mouse up
	if a.input.MouseJustUp {
		a.dragging = false
	}

	// Run physics only during stabilization
	if a.physicsEnabled && a.dataLoaded && a.stabilizationLeft > 0 {
		a.runPhysics()
		a.stabilizationLeft--
		a.needsRedraw = true

		// Fit to graph after stabilization completes
		if a.stabilizationLeft == 0 {
			a.view.FitToGraph(a.graph, 50)
		}
	}
}

func (a *App) draw() {
	// Clear background
	a.canvas.Clear(ColorBackground)

	if !a.dataLoaded {
		// Draw loading message
		msg := "Loading transport data..."
		if a.loadError != "" {
			msg = "Error: " + a.loadError
		}
		a.canvas.Text(msg, a.canvas.Width()/2-100, a.canvas.Height()/2, ColorText, "14px sans-serif")
		return
	}

	// Draw edges
	a.drawEdges()

	// Draw nodes
	a.drawNodes()

	// Draw UI overlay
	a.drawUI()
}

func (a *App) drawEdges() {
	for _, edge := range a.graph.Edges {
		if edge.Hidden {
			continue
		}

		if !a.opts.ShowEdgeType(edge.Type) {
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

		x1, y1 := a.view.WorldToScreen(fromNode.X, fromNode.Y)
		x2, y2 := a.view.WorldToScreen(toNode.X, toNode.Y)

		// Cull off-screen edges
		margin := 50.0
		w, h := a.canvas.Width(), a.canvas.Height()
		if (x1 < -margin && x2 < -margin) || (x1 > w+margin && x2 > w+margin) ||
			(y1 < -margin && y2 < -margin) || (y1 > h+margin && y2 > h+margin) {
			continue
		}

		edgeColor := a.edgeColor(edge.Type)
		lineWidth := 1.0
		opacity := 0.6 // Default edge opacity (matching JS)

		// Check if this edge connects to local visor
		isLocalEdge := fromNode.IsLocalVisor || toNode.IsLocalVisor
		if isLocalEdge {
			edgeColor = ColorLocalEdge
			lineWidth = 4.0
			opacity = 1.0
		}

		// Highlight edges connected to selected node
		if a.selectedNode != nil {
			if edge.From == a.selectedNode.ID || edge.To == a.selectedNode.ID {
				lineWidth = 2.0
				opacity = 1.0
			} else if !isLocalEdge {
				edgeColor = ColorEdgeDim
				opacity = 0.3
			}
		}

		a.canvas.SetGlobalAlpha(opacity)
		a.canvas.QuadraticCurve(x1, y1, x2, y2, lineWidth, edgeColor)
		a.canvas.ResetGlobalAlpha()
	}
}

func (a *App) drawNodes() {
	for _, node := range a.graph.Nodes {
		if !a.opts.ShowNodeStatus(node.Status) {
			continue
		}

		sx, sy := a.view.WorldToScreen(node.X, node.Y)

		// Cull off-screen nodes
		margin := 50.0
		if sx < -margin || sx > a.canvas.Width()+margin ||
			sy < -margin || sy > a.canvas.Height()+margin {
			continue
		}

		size := node.Size * a.view.Scale
		if size < 3 {
			size = 3
		}

		bgColor, borderColor := a.nodeColors(node)

		// Border width: local=4, offline=3, others=2 (matching JS)
		borderWidth := 2.0
		if node.IsLocalVisor {
			borderWidth = 4.0
		} else if node.Status == StatusOffline {
			borderWidth = 3.0
		}

		// Draw glow for local visor (matching JS shadow effect)
		if node.IsLocalVisor {
			a.canvas.SetGlobalAlpha(0.4)
			a.canvas.FillCircle(sx, sy, size*1.8, ColorLocalVisorBg)
			a.canvas.ResetGlobalAlpha()
		}

		// Draw node fill
		a.canvas.FillCircle(sx, sy, size, bgColor)

		// Draw border
		a.canvas.StrokeCircle(sx, sy, size, borderWidth, borderColor)

		// Draw selection/hover ring
		if node.IsSelected {
			a.canvas.StrokeCircle(sx, sy, size+4, 2, ColorSelected)
		} else if node.IsHovered {
			a.canvas.StrokeCircle(sx, sy, size+3, 1, ColorHovered)
		}

		// Draw label if zoomed in (matching JS font settings)
		if a.opts.ShowLabels && a.view.Scale > 0.5 {
			label := node.Label
			if label == "" {
				label = shortPK(node.ID)
			}
			a.canvas.Text(label, sx+size+4, sy+4, ColorTextDim, "10px sans-serif")
		}
	}
}

func (a *App) drawUI() {
	// Stats in top-right
	y := 20.0
	x := a.canvas.Width() - 120

	online, offline, unknown := a.graph.CountByStatus()
	a.canvas.Text("Nodes: "+itoa(a.graph.NodeCount()), x, y, ColorText, "12px sans-serif")
	y += 16
	a.canvas.Text("Edges: "+itoa(a.graph.VisibleEdgeCount()), x, y, ColorText, "12px sans-serif")
	y += 16
	a.canvas.Text("Online: "+itoa(online), x, y, ColorOnlineBg, "12px sans-serif")
	y += 16
	a.canvas.Text("Offline: "+itoa(offline), x, y, ColorOfflineBg, "12px sans-serif")
	y += 16
	a.canvas.Text("Unknown: "+itoa(unknown), x, y, ColorUnknownBg, "12px sans-serif")

	y += 24
	a.canvas.Text("Zoom: "+ftoa(a.view.Scale)+"x", x, y, ColorTextDim, "11px sans-serif")
	y += 14
	physStatus := "ON"
	if !a.physicsEnabled {
		physStatus = "OFF"
	}
	a.canvas.Text("Physics: "+physStatus, x, y, ColorTextDim, "11px sans-serif")

	// Selected node info in bottom-left
	if a.selectedNode != nil {
		y = a.canvas.Height() - 80
		a.canvas.Text("Selected:", 10, y, ColorText, "12px sans-serif")
		y += 16
		a.canvas.Text(shortPK(a.selectedNode.ID), 10, y, ColorHovered, "11px monospace")
		y += 14
		status := "Unknown"
		switch a.selectedNode.Status {
		case StatusOnline:
			status = "Online"
		case StatusOffline:
			status = "Offline"
		}
		a.canvas.Text("Status: "+status, 10, y, ColorTextDim, "11px sans-serif")
		if a.selectedNode.Country != "" {
			y += 14
			a.canvas.Text("Country: "+a.selectedNode.Country, 10, y, ColorTextDim, "11px sans-serif")
		}
	}
}

// nodeColors returns background and border color for a node (matching JS getNodeColor)
func (a *App) nodeColors(node *Node) (bg, border string) {
	if node.IsLocalVisor {
		return ColorLocalVisorBg, ColorLocalVisorBorder
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

// LoadData fetches data from the API
func (a *App) LoadData() {
	transports, err := a.fetcher.FetchTransports()
	if err != nil {
		a.loadError = err.Error()
		a.needsRedraw = true
		return
	}

	uptimes, _ := a.fetcher.FetchUptimes()
	services, _ := a.fetcher.FetchServices()

	graph := ProcessTransports(transports, uptimes, services)
	graph.RandomizePositions(a.view.Width/a.view.Scale, a.view.Height/a.view.Scale)

	a.graph = graph
	a.dataLoaded = true
	a.loadError = ""
	a.needsRedraw = true

	// Run physics for 100 iterations to stabilize, then stop
	a.stabilizationLeft = 100
	a.physicsEnabled = true

	a.view.FitToGraph(a.graph, 50)
}

// FitToScreen adjusts the view to fit all nodes
func (a *App) FitToScreen() {
	a.view.FitToGraph(a.graph, 50)
}

// Force-directed physics matching vis-network Barnes-Hut parameters
// gravitationalConstant: -3000, springConstant: 0.001, springLength: 200
func (a *App) runPhysics() {
	const (
		// Barnes-Hut parameters from vis-network
		gravitationalConstant = -3000.0 // Repulsion between nodes
		springConstant        = 0.001   // Edge spring strength
		springLength          = 200.0   // Ideal edge length
		damping               = 0.9     // Velocity damping
		minVelocity           = 0.1     // Minimum velocity threshold
		maxVelocity           = 50.0    // Maximum velocity
		centralGravity        = 0.01    // Pull towards center
	)

	// Calculate forces for each node
	for _, node := range a.graph.Nodes {
		if node.IsPinned {
			continue
		}

		fx, fy := 0.0, 0.0

		// Repulsion from other nodes (Barnes-Hut approximation simplified to O(n^2))
		// In real Barnes-Hut, this would use a quad-tree for O(n log n)
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

			// Barnes-Hut repulsion: F = gravitationalConstant / distance^2
			// gravitationalConstant is negative, so this pushes nodes apart
			force := -gravitationalConstant / distSq
			fx += (dx / dist) * force
			fy += (dy / dist) * force
		}

		// Spring forces along edges (Hooke's law)
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
				// Spring force: F = springConstant * (distance - springLength)
				displacement := dist - springLength
				force := springConstant * displacement
				fx += (dx / dist) * force
				fy += (dy / dist) * force
			}
		}

		// Central gravity - pull towards origin
		dist := math.Sqrt(node.X*node.X + node.Y*node.Y)
		if dist > 0 {
			fx -= (node.X / dist) * centralGravity * dist
			fy -= (node.Y / dist) * centralGravity * dist
		}

		// Apply forces with damping
		node.VX = (node.VX + fx) * damping
		node.VY = (node.VY + fy) * damping

		// Clamp velocity
		speed := math.Sqrt(node.VX*node.VX + node.VY*node.VY)
		if speed > maxVelocity {
			scale := maxVelocity / speed
			node.VX *= scale
			node.VY *= scale
		}

		// Update position if velocity is significant
		if math.Abs(node.VX) > minVelocity || math.Abs(node.VY) > minVelocity {
			node.X += node.VX
			node.Y += node.VY
		}
	}
}
