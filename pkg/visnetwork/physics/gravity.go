package physics

import "math"

// CentralGravitySolver applies a force pulling all nodes toward the center
// This prevents the graph from drifting off-screen
type CentralGravitySolver struct {
	nodes       map[string]PhysicsNode
	physicsBody *PhysicsBody
	options     BarnesHutOptions
}

// NewCentralGravitySolver creates a new central gravity solver
func NewCentralGravitySolver(nodes map[string]PhysicsNode, physicsBody *PhysicsBody, options BarnesHutOptions) *CentralGravitySolver {
	return &CentralGravitySolver{
		nodes:       nodes,
		physicsBody: physicsBody,
		options:     options,
	}
}

// SetOptions updates the solver configuration
func (s *CentralGravitySolver) SetOptions(opts interface{}) {
	if o, ok := opts.(BarnesHutOptions); ok {
		s.options = o
	}
}

// Solve calculates central gravity forces for all nodes
func (s *CentralGravitySolver) Solve() {
	for _, nodeID := range s.physicsBody.PhysicsNodeIndices {
		node := s.nodes[nodeID]
		if node == nil {
			continue
		}

		x, y := node.GetPosition()

		// Direction toward center (0,0)
		dx := -x
		dy := -y
		distance := math.Sqrt(dx*dx + dy*dy)

		s.calculateForces(distance, dx, dy, node)
	}
}

// calculateForces applies the central gravity force to a node
func (s *CentralGravitySolver) calculateForces(distance, dx, dy float64, node PhysicsNode) {
	var gravityForce float64
	if distance == 0 {
		gravityForce = 0
	} else {
		gravityForce = s.options.CentralGravity / distance
	}

	nodeID := node.GetID()
	if s.physicsBody.Forces[nodeID] != nil {
		// Note: vis-network uses assignment (=) not addition (+=) here
		// This means central gravity is the baseline force
		s.physicsBody.Forces[nodeID].X = dx * gravityForce
		s.physicsBody.Forces[nodeID].Y = dy * gravityForce
	}
}
