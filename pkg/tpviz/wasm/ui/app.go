//go:build js && wasm

package ui

import (
	"math"
	"syscall/js"
)

// Colors
const (
	ColorBackground  = "#0a0a1e"
	ColorOnline      = "#00d9a5"
	ColorOffline     = "#e94560"
	ColorUnknown     = "#ffd166"
	ColorLocalVisor  = "#ffc800"
	ColorSelected    = "#ffffff"
	ColorHovered     = "#ffc800"
	ColorSTCPR       = "rgba(0, 217, 165, 0.8)"
	ColorSUDPH       = "rgba(233, 69, 96, 0.8)"
	ColorDMSG        = "rgba(100, 149, 237, 0.8)"
	ColorEdgeDim     = "rgba(100, 100, 100, 0.3)"
	ColorText        = "#ffffff"
	ColorTextDim     = "#888888"
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

	// Physics
	physicsEnabled bool

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
	a.frameCallback = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		a.update()
		a.draw()
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
			}
		}
	}

	// Handle keyboard
	if a.input.IsKeyJustPressed("f") || a.input.IsKeyJustPressed("F") {
		a.FitToScreen()
	}
	if a.input.IsKeyJustPressed("r") || a.input.IsKeyJustPressed("R") {
		go a.LoadData()
	}
	if a.input.IsKeyJustPressed("p") || a.input.IsKeyJustPressed("P") {
		a.physicsEnabled = !a.physicsEnabled
	}
	if a.input.IsKeyJustPressed("l") || a.input.IsKeyJustPressed("L") {
		a.opts.ShowLabels = !a.opts.ShowLabels
	}

	// Handle mouse wheel zoom
	if a.input.WheelDelta != 0 {
		a.view.Zoom(a.input.WheelDelta, a.input.MouseX, a.input.MouseY)
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
	}

	// Handle mouse up
	if a.input.MouseJustUp {
		a.dragging = false
	}

	// Run physics
	if a.physicsEnabled && a.dataLoaded {
		a.runPhysics()
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

		// Highlight edges connected to selected node
		if a.selectedNode != nil {
			if edge.From == a.selectedNode.ID || edge.To == a.selectedNode.ID {
				lineWidth = 2.0
			} else {
				edgeColor = ColorEdgeDim
			}
		}

		a.canvas.Line(x1, y1, x2, y2, lineWidth, edgeColor)
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

		nodeColor := a.nodeColor(node)

		// Draw glow for local visor
		if node.IsLocalVisor {
			a.canvas.SetGlobalAlpha(0.3)
			a.canvas.FillCircle(sx, sy, size*2, ColorLocalVisor)
			a.canvas.ResetGlobalAlpha()
		}

		// Draw node
		a.canvas.FillCircle(sx, sy, size, nodeColor)

		// Draw selection/hover ring
		if node.IsSelected {
			a.canvas.StrokeCircle(sx, sy, size+3, 2, ColorSelected)
		} else if node.IsHovered {
			a.canvas.StrokeCircle(sx, sy, size+2, 1, ColorHovered)
		}

		// Draw label if zoomed in
		if a.opts.ShowLabels && a.view.Scale > 0.5 {
			label := node.Label
			if label == "" {
				label = shortPK(node.ID)
			}
			a.canvas.Text(label, sx+size+4, sy+4, ColorText, "11px monospace")
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
	a.canvas.Text("Online: "+itoa(online), x, y, ColorOnline, "12px sans-serif")
	y += 16
	a.canvas.Text("Offline: "+itoa(offline), x, y, ColorOffline, "12px sans-serif")
	y += 16
	a.canvas.Text("Unknown: "+itoa(unknown), x, y, ColorUnknown, "12px sans-serif")

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

func (a *App) nodeColor(node *Node) string {
	if node.IsSelected {
		return ColorSelected
	}
	if node.IsHovered {
		return ColorHovered
	}
	if node.IsLocalVisor {
		return ColorLocalVisor
	}

	switch node.Status {
	case StatusOnline:
		return ColorOnline
	case StatusOffline:
		return ColorOffline
	default:
		return ColorUnknown
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
		return ColorUnknown
	}
}

// LoadData fetches data from the API
func (a *App) LoadData() {
	transports, err := a.fetcher.FetchTransports()
	if err != nil {
		a.loadError = err.Error()
		return
	}

	uptimes, _ := a.fetcher.FetchUptimes()
	services, _ := a.fetcher.FetchServices()

	graph := ProcessTransports(transports, uptimes, services)
	graph.RandomizePositions(a.view.Width/a.view.Scale, a.view.Height/a.view.Scale)

	a.graph = graph
	a.dataLoaded = true
	a.loadError = ""

	a.view.FitToGraph(a.graph, 50)
}

// FitToScreen adjusts the view to fit all nodes
func (a *App) FitToScreen() {
	a.view.FitToGraph(a.graph, 50)
}

// Simple force-directed physics
func (a *App) runPhysics() {
	const (
		repulsion   = 3000.0
		attraction  = 0.005
		damping     = 0.85
		minVelocity = 0.1
	)

	for _, node := range a.graph.Nodes {
		if node.IsPinned {
			continue
		}

		fx, fy := 0.0, 0.0

		// Repulsion from other nodes
		for _, other := range a.graph.Nodes {
			if other.ID == node.ID {
				continue
			}

			dx := node.X - other.X
			dy := node.Y - other.Y
			dist := dx*dx + dy*dy
			if dist < 1 {
				dist = 1
			}

			force := repulsion / dist
			fx += dx * force
			fy += dy * force
		}

		// Attraction along edges
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

			fx += dx * attraction
			fy += dy * attraction
		}

		// Center gravity (weak)
		fx -= node.X * 0.0001
		fy -= node.Y * 0.0001

		// Apply forces
		node.VX = (node.VX + fx) * damping
		node.VY = (node.VY + fy) * damping

		// Update position
		if math.Abs(node.VX) > minVelocity || math.Abs(node.VY) > minVelocity {
			node.X += node.VX
			node.Y += node.VY
		}
	}
}
