package physics

import (
	"math"
)

// SpringSolver calculates spring forces for edges, pulling connected
// nodes toward their ideal edge length
type SpringSolver struct {
	nodes       map[string]PhysicsNode
	edges       map[string]PhysicsEdge
	physicsBody *PhysicsBody
	options     BarnesHutOptions
}

// NewSpringSolver creates a new spring force solver
func NewSpringSolver(nodes map[string]PhysicsNode, edges map[string]PhysicsEdge, physicsBody *PhysicsBody, options BarnesHutOptions) *SpringSolver {
	return &SpringSolver{
		nodes:       nodes,
		edges:       edges,
		physicsBody: physicsBody,
		options:     options,
	}
}

// SetOptions updates the solver configuration
func (s *SpringSolver) SetOptions(opts interface{}) {
	if o, ok := opts.(BarnesHutOptions); ok {
		s.options = o
	}
}

// Solve calculates spring forces for all edges
func (s *SpringSolver) Solve() {
	for _, edgeID := range s.physicsBody.PhysicsEdgeIndices {
		edge := s.edges[edgeID]
		if edge == nil {
			continue
		}

		if !edge.IsConnected() {
			continue
		}

		fromID := edge.GetFromID()
		toID := edge.GetToID()

		// Skip self-loops
		if fromID == toID {
			continue
		}

		// Ensure both nodes exist
		fromNode := s.nodes[fromID]
		toNode := s.nodes[toID]
		if fromNode == nil || toNode == nil {
			continue
		}

		// Get edge length (use default if not specified)
		// The 1.5 multiplier makes edges appear similar to smooth edges
		// which use support nodes that add repulsion
		edgeLength := edge.GetLength()
		if edgeLength == 0 {
			edgeLength = s.options.SpringLength * 1.5
		}

		s.calculateSpringForce(fromNode, toNode, edgeLength)
	}
}

// calculateSpringForce computes the spring force between two nodes
func (s *SpringSolver) calculateSpringForce(node1, node2 PhysicsNode, edgeLength float64) {
	x1, y1 := node1.GetPosition()
	x2, y2 := node2.GetPosition()

	dx := x1 - x2
	dy := y1 - y2
	distance := math.Max(math.Sqrt(dx*dx+dy*dy), 0.01)

	// Spring force: k * (L - d) / d
	// where k = spring constant, L = desired length, d = current distance
	// The 1/distance term gives us the unit direction vector
	springForce := s.options.SpringConstant * (edgeLength - distance) / distance

	fx := dx * springForce
	fy := dy * springForce

	// Apply forces to both nodes (Newton's 3rd law)
	node1ID := node1.GetID()
	node2ID := node2.GetID()

	if s.physicsBody.Forces[node1ID] != nil {
		s.physicsBody.Forces[node1ID].X += fx
		s.physicsBody.Forces[node1ID].Y += fy
	}

	if s.physicsBody.Forces[node2ID] != nil {
		s.physicsBody.Forces[node2ID].X -= fx
		s.physicsBody.Forces[node2ID].Y -= fy
	}
}
