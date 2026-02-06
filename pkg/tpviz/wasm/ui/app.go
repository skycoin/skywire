//go:build js && wasm

package ui

import (
	"image/color"
	"math"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// App implements ebiten.Game interface
type App struct {
	graph   *Graph
	view    *View
	opts    *RenderOptions
	fetcher *DataFetcher

	width, height int

	// Interaction state
	dragging       bool
	dragStartX     float64
	dragStartY     float64
	dragOffsetX    float64
	dragOffsetY    float64
	selectedNode   *Node
	hoveredNode    *Node

	// Data loading state
	dataLoaded bool
	loadError  string
	loadingMu  sync.Mutex

	// Physics
	physicsEnabled bool
}

// NewApp creates a new application
func NewApp(width, height int) *App {
	return &App{
		graph:          NewGraph(),
		view:           NewView(float64(width), float64(height)),
		opts:           NewRenderOptions(),
		fetcher:        NewDataFetcher(),
		width:          width,
		height:         height,
		physicsEnabled: true,
	}
}

// Update implements ebiten.Game
func (a *App) Update() error {
	// Handle keyboard
	if inpututil.IsKeyJustPressed(ebiten.KeyF) {
		a.FitToScreen()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		go a.LoadData()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyP) {
		a.physicsEnabled = !a.physicsEnabled
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyL) {
		a.opts.ShowLabels = !a.opts.ShowLabels
	}

	// Handle mouse
	mx, my := ebiten.CursorPosition()
	worldX, worldY := a.view.ScreenToWorld(float64(mx), float64(my))

	// Mouse wheel zoom
	_, wheelY := ebiten.Wheel()
	if wheelY != 0 {
		a.view.Zoom(wheelY, float64(mx), float64(my))
	}

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

	// Mouse button handling
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		node := a.graph.GetNodeAt(worldX, worldY, a.view.Scale)
		if node != nil {
			// Clear previous selection
			if a.selectedNode != nil {
				a.selectedNode.IsSelected = false
			}
			// Select new node
			node.IsSelected = true
			a.selectedNode = node
			a.opts.SelectedNode = node
		} else {
			// Start dragging
			a.dragging = true
			a.dragStartX = float64(mx)
			a.dragStartY = float64(my)
			a.dragOffsetX = a.view.OffsetX
			a.dragOffsetY = a.view.OffsetY
		}
	}

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		if a.dragging {
			a.view.OffsetX = a.dragOffsetX + (float64(mx) - a.dragStartX)
			a.view.OffsetY = a.dragOffsetY + (float64(my) - a.dragStartY)
		}
	}

	if inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft) {
		a.dragging = false
	}

	// Run physics
	if a.physicsEnabled && a.dataLoaded {
		a.runPhysics()
	}

	return nil
}

// Draw implements ebiten.Game
func (a *App) Draw(screen *ebiten.Image) {
	// Background
	screen.Fill(color.RGBA{10, 10, 30, 255})

	a.loadingMu.Lock()
	loaded := a.dataLoaded
	loadErr := a.loadError
	a.loadingMu.Unlock()

	if !loaded {
		// Draw loading/error message
		msg := "Loading transport data..."
		if loadErr != "" {
			msg = "Error: " + loadErr
		}
		ebitenutil.DebugPrintAt(screen, msg, a.width/2-100, a.height/2)
		return
	}

	// Draw edges
	a.drawEdges(screen)

	// Draw nodes
	a.drawNodes(screen)

	// Draw UI overlay
	a.drawUI(screen)
}

func (a *App) drawEdges(screen *ebiten.Image) {
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
		w, h := float64(a.width), float64(a.height)
		if (x1 < -margin && x2 < -margin) || (x1 > w+margin && x2 > w+margin) ||
			(y1 < -margin && y2 < -margin) || (y1 > h+margin && y2 > h+margin) {
			continue
		}

		edgeColor := a.edgeColor(edge.Type)

		// Highlight edges connected to selected node
		lineWidth := float32(1.0)
		if a.selectedNode != nil {
			if edge.From == a.selectedNode.ID || edge.To == a.selectedNode.ID {
				lineWidth = 2.0
			} else {
				edgeColor.A = 80
			}
		}

		vector.StrokeLine(screen, float32(x1), float32(y1), float32(x2), float32(y2), lineWidth, edgeColor, false)
	}
}

func (a *App) drawNodes(screen *ebiten.Image) {
	for _, node := range a.graph.Nodes {
		if !a.opts.ShowNodeStatus(node.Status) {
			continue
		}

		sx, sy := a.view.WorldToScreen(node.X, node.Y)

		// Cull off-screen nodes
		margin := 50.0
		if sx < -margin || sx > float64(a.width)+margin ||
			sy < -margin || sy > float64(a.height)+margin {
			continue
		}

		size := node.Size * a.view.Scale
		if size < 3 {
			size = 3
		}

		nodeColor := a.nodeColor(node)

		// Draw glow for local visor
		if node.IsLocalVisor {
			glowColor := color.RGBA{255, 200, 50, 80}
			vector.DrawFilledCircle(screen, float32(sx), float32(sy), float32(size*2), glowColor, false)
		}

		// Draw node
		vector.DrawFilledCircle(screen, float32(sx), float32(sy), float32(size), nodeColor, false)

		// Draw selection/hover ring
		if node.IsSelected {
			vector.StrokeCircle(screen, float32(sx), float32(sy), float32(size+3), 2, color.RGBA{255, 255, 255, 255}, false)
		} else if node.IsHovered {
			vector.StrokeCircle(screen, float32(sx), float32(sy), float32(size+2), 1, color.RGBA{255, 200, 50, 255}, false)
		}

		// Draw label if zoomed in
		if a.opts.ShowLabels && a.view.Scale > 0.8 {
			label := node.Label
			if label == "" {
				label = shortPK(node.ID)
			}
			ebitenutil.DebugPrintAt(screen, label, int(sx+size+4), int(sy-6))
		}
	}
}

func (a *App) drawUI(screen *ebiten.Image) {
	y := 10
	ebitenutil.DebugPrintAt(screen, "=== Transport Visualizer ===", 10, y)
	y += 20

	online, offline, unknown := a.graph.CountByStatus()
	ebitenutil.DebugPrintAt(screen, "Nodes: "+itoa(a.graph.NodeCount()), 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "Edges: "+itoa(a.graph.VisibleEdgeCount()), 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "Online: "+itoa(online), 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "Offline: "+itoa(offline), 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "Unknown: "+itoa(unknown), 10, y)

	y += 25
	ebitenutil.DebugPrintAt(screen, "=== Controls ===", 10, y)
	y += 20
	ebitenutil.DebugPrintAt(screen, "F - Fit to screen", 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "R - Refresh data", 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "P - Toggle physics", 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "L - Toggle labels", 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "Scroll - Zoom", 10, y)
	y += 15
	ebitenutil.DebugPrintAt(screen, "Drag - Pan", 10, y)

	// Zoom level
	ebitenutil.DebugPrintAt(screen, "Zoom: "+ftoa(a.view.Scale)+"x", a.width-100, 10)

	// Physics status
	physStatus := "ON"
	if !a.physicsEnabled {
		physStatus = "OFF"
	}
	ebitenutil.DebugPrintAt(screen, "Physics: "+physStatus, a.width-100, 25)

	// Selected node info
	if a.selectedNode != nil {
		y = a.height - 120
		ebitenutil.DebugPrintAt(screen, "=== Selected ===", 10, y)
		y += 20
		ebitenutil.DebugPrintAt(screen, shortPK(a.selectedNode.ID), 10, y)
		y += 15
		status := "Unknown"
		switch a.selectedNode.Status {
		case StatusOnline:
			status = "Online"
		case StatusOffline:
			status = "Offline"
		}
		ebitenutil.DebugPrintAt(screen, "Status: "+status, 10, y)
		y += 15
		if a.selectedNode.Country != "" {
			ebitenutil.DebugPrintAt(screen, "Country: "+a.selectedNode.Country, 10, y)
			y += 15
		}
		if a.selectedNode.Version != "" {
			ebitenutil.DebugPrintAt(screen, "Version: "+a.selectedNode.Version, 10, y)
		}
	}
}

func (a *App) nodeColor(node *Node) color.RGBA {
	if node.IsSelected {
		return color.RGBA{255, 255, 255, 255}
	}
	if node.IsHovered {
		return color.RGBA{255, 200, 50, 255}
	}
	if node.IsLocalVisor {
		return color.RGBA{255, 200, 50, 255}
	}

	switch node.Status {
	case StatusOnline:
		return color.RGBA{0, 217, 165, 255}
	case StatusOffline:
		return color.RGBA{233, 69, 96, 255}
	default:
		return color.RGBA{255, 209, 102, 255}
	}
}

func (a *App) edgeColor(t TransportType) color.RGBA {
	switch t {
	case TransportSTCPR:
		return color.RGBA{0, 217, 165, 200}
	case TransportSUDPH:
		return color.RGBA{233, 69, 96, 200}
	case TransportDMSG:
		return color.RGBA{100, 149, 237, 200}
	default:
		return color.RGBA{255, 209, 102, 200}
	}
}

// Layout implements ebiten.Game
func (a *App) Layout(outsideWidth, outsideHeight int) (int, int) {
	a.width = outsideWidth
	a.height = outsideHeight
	a.view.Width = float64(outsideWidth)
	a.view.Height = float64(outsideHeight)
	return outsideWidth, outsideHeight
}

// LoadData fetches data from the API
func (a *App) LoadData() {
	transports, err := a.fetcher.FetchTransports()
	if err != nil {
		a.loadingMu.Lock()
		a.loadError = err.Error()
		a.loadingMu.Unlock()
		return
	}

	uptimes, _ := a.fetcher.FetchUptimes()
	services, _ := a.fetcher.FetchServices()

	graph := ProcessTransports(transports, uptimes, services)
	graph.RandomizePositions(a.view.Width/a.view.Scale, a.view.Height/a.view.Scale)

	a.loadingMu.Lock()
	a.graph = graph
	a.dataLoaded = true
	a.loadError = ""
	a.loadingMu.Unlock()

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
