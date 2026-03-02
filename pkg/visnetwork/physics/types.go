// Package physics implements vis-network compatible physics simulation
// for force-directed graph layouts. This is a Go port of the vis-network
// JavaScript physics engine.
package physics

// Vec2 represents a 2D vector for positions, forces, and velocities
type Vec2 struct {
	X float64
	Y float64
}

// PhysicsNode is the interface that graph nodes must implement
// for physics simulation
type PhysicsNode interface {
	// GetID returns the unique identifier for this node
	GetID() string
	// GetPosition returns the current X, Y position
	GetPosition() (float64, float64)
	// SetPosition sets the X, Y position
	SetPosition(x, y float64)
	// GetMass returns the node's mass (default 1)
	GetMass() float64
	// GetSize returns the node's radius/size for overlap avoidance
	GetSize() float64
	// IsFixed returns whether the node's X and Y positions are fixed
	IsFixed() (fixedX, fixedY bool)
}

// PhysicsEdge is the interface that graph edges must implement
// for spring physics
type PhysicsEdge interface {
	// GetID returns the unique identifier for this edge
	GetID() string
	// GetFromID returns the source node ID
	GetFromID() string
	// GetToID returns the target node ID
	GetToID() string
	// GetLength returns the desired edge length (0 for default)
	GetLength() float64
	// IsConnected returns whether both endpoints exist
	IsConnected() bool
}

// PhysicsBody holds the physics state for all nodes
type PhysicsBody struct {
	// PhysicsNodeIndices contains IDs of nodes participating in physics
	PhysicsNodeIndices []string
	// PhysicsEdgeIndices contains IDs of edges participating in physics
	PhysicsEdgeIndices []string
	// Forces maps node ID to accumulated force vector
	Forces map[string]*Vec2
	// Velocities maps node ID to current velocity vector
	Velocities map[string]*Vec2
}

// NewPhysicsBody creates a new physics body state
func NewPhysicsBody() *PhysicsBody {
	return &PhysicsBody{
		PhysicsNodeIndices: make([]string, 0),
		PhysicsEdgeIndices: make([]string, 0),
		Forces:             make(map[string]*Vec2),
		Velocities:         make(map[string]*Vec2),
	}
}

// ResetForces resets all force vectors to zero
func (pb *PhysicsBody) ResetForces() {
	for _, id := range pb.PhysicsNodeIndices {
		if pb.Forces[id] == nil {
			pb.Forces[id] = &Vec2{}
		} else {
			pb.Forces[id].X = 0
			pb.Forces[id].Y = 0
		}
	}
}

// EnsureVelocity ensures a velocity entry exists for the given node
func (pb *PhysicsBody) EnsureVelocity(nodeID string) {
	if pb.Velocities[nodeID] == nil {
		pb.Velocities[nodeID] = &Vec2{}
	}
}

// BarnesHutOptions contains settings for the Barnes-Hut solver
type BarnesHutOptions struct {
	// Theta is the approximation threshold (default 0.5)
	// Lower values = more accurate but slower
	Theta float64
	// GravitationalConstant controls node repulsion (default -2000)
	// Negative values cause repulsion
	GravitationalConstant float64
	// CentralGravity pulls nodes toward center (default 0.3)
	CentralGravity float64
	// SpringLength is the default edge length (default 95)
	SpringLength float64
	// SpringConstant controls edge stiffness (default 0.04)
	SpringConstant float64
	// Damping reduces velocity over time (default 0.09)
	Damping float64
	// AvoidOverlap 0-1, how much to avoid node overlap (default 0)
	AvoidOverlap float64
}

// DefaultBarnesHutOptions returns the default Barnes-Hut configuration
// matching vis-network defaults
func DefaultBarnesHutOptions() BarnesHutOptions {
	return BarnesHutOptions{
		Theta:                 0.5,
		GravitationalConstant: -2000,
		CentralGravity:        0.3,
		SpringLength:          95,
		SpringConstant:        0.04,
		Damping:               0.09,
		AvoidOverlap:          0,
	}
}

// PhysicsOptions contains the overall physics engine configuration
type PhysicsOptions struct {
	// Enabled toggles physics simulation
	Enabled bool
	// BarnesHut contains Barnes-Hut solver options
	BarnesHut BarnesHutOptions
	// MaxVelocity caps node movement speed (default 50)
	MaxVelocity float64
	// MinVelocity threshold for stabilization (default 0.75)
	MinVelocity float64
	// Timestep for physics integration (default 0.5)
	Timestep float64
	// StabilizationIterations max iterations to stabilize (default 1000)
	StabilizationIterations int
}

// DefaultPhysicsOptions returns the default physics configuration
// matching vis-network defaults
func DefaultPhysicsOptions() PhysicsOptions {
	return PhysicsOptions{
		Enabled:                 true,
		BarnesHut:               DefaultBarnesHutOptions(),
		MaxVelocity:             50,
		MinVelocity:             0.75,
		Timestep:                0.5,
		StabilizationIterations: 1000,
	}
}

// Solver is the interface that all physics solvers implement
type Solver interface {
	Solve()
	SetOptions(opts interface{})
}
