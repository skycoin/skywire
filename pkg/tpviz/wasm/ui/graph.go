//go:build js && wasm

package ui

import (
	"math"
	"math/rand"
)

// TransportType represents the type of transport
type TransportType string

const (
	TransportSTCPR TransportType = "stcpr"
	TransportSUDPH TransportType = "sudph"
	TransportDMSG  TransportType = "dmsg"
)

// NodeStatus represents the online/offline status of a node
type NodeStatus int

const (
	StatusUnknown NodeStatus = iota
	StatusOnline
	StatusOffline
)

// Node represents a visor in the network
type Node struct {
	ID      string
	X, Y    float64 // Position in world coordinates
	VX, VY  float64 // Velocity for physics simulation
	Size    float64
	Status  NodeStatus
	Country string
	Version string
	Label   string // Short label for display

	// Flags
	IsLocalVisor bool
	HasServices  bool
	IsSelected   bool
	IsHovered    bool
	IsPinned     bool // If true, position is fixed
}

// Edge represents a transport between two visors
type Edge struct {
	ID     string
	From   string // Node ID
	To     string // Node ID
	Type   TransportType
	Hidden bool
}

// Graph holds all nodes and edges
type Graph struct {
	Nodes map[string]*Node
	Edges map[string]*Edge

	// Indices for fast lookup
	edgesByNode map[string][]*Edge // node ID -> edges connected to it
}

// NewGraph creates a new empty graph
func NewGraph() *Graph {
	return &Graph{
		Nodes:       make(map[string]*Node),
		Edges:       make(map[string]*Edge),
		edgesByNode: make(map[string][]*Edge),
	}
}

// AddNode adds a node to the graph
func (g *Graph) AddNode(node *Node) {
	g.Nodes[node.ID] = node
}

// AddEdge adds an edge to the graph
func (g *Graph) AddEdge(edge *Edge) {
	g.Edges[edge.ID] = edge

	// Update indices
	g.edgesByNode[edge.From] = append(g.edgesByNode[edge.From], edge)
	g.edgesByNode[edge.To] = append(g.edgesByNode[edge.To], edge)
}

// GetEdgesForNode returns all edges connected to a node
func (g *Graph) GetEdgesForNode(nodeID string) []*Edge {
	return g.edgesByNode[nodeID]
}

// Clear removes all nodes and edges
func (g *Graph) Clear() {
	g.Nodes = make(map[string]*Node)
	g.Edges = make(map[string]*Edge)
	g.edgesByNode = make(map[string][]*Edge)
}

// NodeCount returns the number of nodes
func (g *Graph) NodeCount() int {
	return len(g.Nodes)
}

// EdgeCount returns the number of edges
func (g *Graph) EdgeCount() int {
	return len(g.Edges)
}

// GetNodeAt returns the node at the given world position, or nil
func (g *Graph) GetNodeAt(x, y, scale float64) *Node {
	for _, node := range g.Nodes {
		dx := x - node.X
		dy := y - node.Y
		dist := math.Sqrt(dx*dx + dy*dy)
		// Adjust hit radius based on scale
		hitRadius := node.Size + 5/scale
		if dist <= hitRadius {
			return node
		}
	}
	return nil
}

// RandomizePositions places all nodes at random positions
func (g *Graph) RandomizePositions(width, height float64) {
	for _, node := range g.Nodes {
		if !node.IsPinned {
			node.X = (rand.Float64() - 0.5) * width * 0.8
			node.Y = (rand.Float64() - 0.5) * height * 0.8
		}
	}
}

// GetBounds returns the bounding box of all nodes (minX, minY, maxX, maxY)
func (g *Graph) GetBounds() (float64, float64, float64, float64) {
	if len(g.Nodes) == 0 {
		return 0, 0, 0, 0
	}

	minX, minY := math.MaxFloat64, math.MaxFloat64
	maxX, maxY := -math.MaxFloat64, -math.MaxFloat64

	for _, node := range g.Nodes {
		if node.X < minX {
			minX = node.X
		}
		if node.Y < minY {
			minY = node.Y
		}
		if node.X > maxX {
			maxX = node.X
		}
		if node.Y > maxY {
			maxY = node.Y
		}
	}

	return minX, minY, maxX, maxY
}

// VisibleEdgeCount returns count of visible edges
func (g *Graph) VisibleEdgeCount() int {
	count := 0
	for _, edge := range g.Edges {
		if edge.Hidden {
			continue
		}
		_, fromOK := g.Nodes[edge.From]
		_, toOK := g.Nodes[edge.To]
		if fromOK && toOK {
			count++
		}
	}
	return count
}

// CountByStatus returns counts of nodes by status
func (g *Graph) CountByStatus() (online, offline, unknown int) {
	for _, node := range g.Nodes {
		switch node.Status {
		case StatusOnline:
			online++
		case StatusOffline:
			offline++
		default:
			unknown++
		}
	}
	return
}

// View manages the viewport transform
type View struct {
	OffsetX, OffsetY float64 // Pan offset
	Scale            float64 // Zoom level
	Width, Height    float64 // Viewport size
}

// NewView creates a new view centered at origin
func NewView(width, height float64) *View {
	return &View{
		OffsetX: width / 2,
		OffsetY: height / 2,
		Scale:   1.0,
		Width:   width,
		Height:  height,
	}
}

// WorldToScreen converts world coordinates to screen coordinates
func (v *View) WorldToScreen(wx, wy float64) (float64, float64) {
	return wx*v.Scale + v.OffsetX, wy*v.Scale + v.OffsetY
}

// ScreenToWorld converts screen coordinates to world coordinates
func (v *View) ScreenToWorld(sx, sy float64) (float64, float64) {
	return (sx - v.OffsetX) / v.Scale, (sy - v.OffsetY) / v.Scale
}

// Zoom adjusts the scale, zooming towards the given screen point
func (v *View) Zoom(delta float64, sx, sy float64) {
	oldScale := v.Scale
	v.Scale *= math.Pow(1.1, delta)

	// Clamp scale
	if v.Scale < 0.1 {
		v.Scale = 0.1
	}
	if v.Scale > 10.0 {
		v.Scale = 10.0
	}

	// Zoom towards mouse position
	scaleFactor := v.Scale / oldScale
	v.OffsetX = sx - (sx-v.OffsetX)*scaleFactor
	v.OffsetY = sy - (sy-v.OffsetY)*scaleFactor
}

// FitToGraph adjusts the view to fit the entire graph
func (v *View) FitToGraph(g *Graph, padding float64) {
	if len(g.Nodes) == 0 {
		return
	}

	minX, minY, maxX, maxY := g.GetBounds()

	// Add padding
	minX -= padding
	minY -= padding
	maxX += padding
	maxY += padding

	graphWidth := maxX - minX
	graphHeight := maxY - minY

	// Calculate scale to fit
	scaleX := v.Width / graphWidth
	scaleY := v.Height / graphHeight
	v.Scale = math.Min(scaleX, scaleY) * 0.9

	// Clamp
	if v.Scale < 0.1 {
		v.Scale = 0.1
	}
	if v.Scale > 10.0 {
		v.Scale = 10.0
	}

	// Center on graph center
	centerX := (minX + maxX) / 2
	centerY := (minY + maxY) / 2
	v.OffsetX = v.Width/2 - centerX*v.Scale
	v.OffsetY = v.Height/2 - centerY*v.Scale
}

// RenderOptions controls what gets rendered
type RenderOptions struct {
	ShowSTCPR         bool
	ShowSUDPH         bool
	ShowDMSG          bool
	ShowOnline        bool
	ShowOffline       bool
	ShowUnknown       bool
	ShowLabels        bool
	HighlightServices bool
	SelectedNode      *Node
	HoveredNode       *Node
}

// NewRenderOptions creates default render options
func NewRenderOptions() *RenderOptions {
	return &RenderOptions{
		ShowSTCPR:  true,
		ShowSUDPH:  true,
		ShowDMSG:   true,
		ShowOnline: true,
		ShowLabels: true,
	}
}

// ShowEdgeType returns whether edges of the given type should be shown
func (o *RenderOptions) ShowEdgeType(t TransportType) bool {
	switch t {
	case TransportSTCPR:
		return o.ShowSTCPR
	case TransportSUDPH:
		return o.ShowSUDPH
	case TransportDMSG:
		return o.ShowDMSG
	default:
		return true
	}
}

// ShowNodeStatus returns whether nodes with the given status should be shown
func (o *RenderOptions) ShowNodeStatus(s NodeStatus) bool {
	switch s {
	case StatusOnline:
		return o.ShowOnline
	case StatusOffline:
		return o.ShowOffline
	case StatusUnknown:
		return o.ShowUnknown
	default:
		return true
	}
}

// Helper functions

// shortPK returns a shortened version of a public key
func shortPK(pk string) string {
	if len(pk) <= 12 {
		return pk
	}
	return pk[:6] + ".." + pk[len(pk)-4:]
}

// itoa converts int to string without fmt
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ftoa converts float to string (1 decimal place)
func ftoa(f float64) string {
	if f < 0 {
		return "-" + ftoa(-f)
	}
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac)
}
