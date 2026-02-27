package physics

import "math"

// PhysicsEngine orchestrates the physics simulation, combining
// node repulsion (Barnes-Hut), edge springs, and central gravity
type PhysicsEngine struct {
	// Node and edge data
	nodes map[string]PhysicsNode
	edges map[string]PhysicsEdge

	// Physics state
	physicsBody *PhysicsBody
	options     PhysicsOptions

	// Solvers
	nodesSolver   *BarnesHutSolver
	edgesSolver   *SpringSolver
	gravitySolver *CentralGravitySolver

	// State tracking
	timestep                 float64
	stabilized               bool
	stabilizationIterations  int
	previousStates           map[string]*nodeState

	// Adaptive timestep
	adaptiveTimestep        bool
	adaptiveTimestepEnabled bool
	adaptiveCounter         int
	adaptiveInterval        int
	referenceState          map[string]*Vec2
}

// nodeState stores previous position and velocity for reverting
type nodeState struct {
	x, y   float64
	vx, vy float64
}

// NewPhysicsEngine creates a new physics engine instance
func NewPhysicsEngine(nodes map[string]PhysicsNode, edges map[string]PhysicsEdge) *PhysicsEngine {
	physicsBody := NewPhysicsBody()
	options := DefaultPhysicsOptions()

	engine := &PhysicsEngine{
		nodes:            nodes,
		edges:            edges,
		physicsBody:      physicsBody,
		options:          options,
		timestep:         options.Timestep,
		adaptiveInterval: 3,
		previousStates:   make(map[string]*nodeState),
		referenceState:   make(map[string]*Vec2),
	}

	// Initialize solvers
	engine.nodesSolver = NewBarnesHutSolver(nodes, physicsBody, options.BarnesHut)
	engine.edgesSolver = NewSpringSolver(nodes, edges, physicsBody, options.BarnesHut)
	engine.gravitySolver = NewCentralGravitySolver(nodes, physicsBody, options.BarnesHut)

	return engine
}

// SetOptions updates the physics configuration
func (e *PhysicsEngine) SetOptions(options PhysicsOptions) {
	e.options = options
	e.timestep = options.Timestep

	e.nodesSolver.SetOptions(options.BarnesHut)
	e.edgesSolver.SetOptions(options.BarnesHut)
	e.gravitySolver.SetOptions(options.BarnesHut)
}

// GetOptions returns the current physics options
func (e *PhysicsEngine) GetOptions() PhysicsOptions {
	return e.options
}

// UpdatePhysicsData rebuilds the list of physics-enabled nodes and edges
// Call this after adding/removing nodes or edges
func (e *PhysicsEngine) UpdatePhysicsData() {
	e.physicsBody.Forces = make(map[string]*Vec2)
	e.physicsBody.PhysicsNodeIndices = make([]string, 0)
	e.physicsBody.PhysicsEdgeIndices = make([]string, 0)

	// Collect physics-enabled nodes
	for id := range e.nodes {
		e.physicsBody.PhysicsNodeIndices = append(e.physicsBody.PhysicsNodeIndices, id)
	}

	// Collect physics-enabled edges
	for id := range e.edges {
		e.physicsBody.PhysicsEdgeIndices = append(e.physicsBody.PhysicsEdgeIndices, id)
	}

	// Initialize forces and velocities
	for _, nodeID := range e.physicsBody.PhysicsNodeIndices {
		e.physicsBody.Forces[nodeID] = &Vec2{}
		e.physicsBody.EnsureVelocity(nodeID)
	}

	// Clean up velocities for deleted nodes
	for nodeID := range e.physicsBody.Velocities {
		if _, exists := e.nodes[nodeID]; !exists {
			delete(e.physicsBody.Velocities, nodeID)
		}
	}
}

// PhysicsStep performs one iteration of physics simulation
func (e *PhysicsEngine) PhysicsStep() {
	// Reset forces before calculating
	e.physicsBody.ResetForces()

	// Apply forces from all solvers in order:
	// 1. Central gravity (baseline)
	// 2. Node repulsion (Barnes-Hut)
	// 3. Edge springs
	e.gravitySolver.Solve()
	e.nodesSolver.Solve()
	e.edgesSolver.Solve()

	// Move nodes based on accumulated forces
	e.moveNodes()
}

// PhysicsTick performs a physics step with optional adaptive timestep
func (e *PhysicsEngine) PhysicsTick() {
	if e.stabilized {
		return
	}

	if e.adaptiveTimestep && e.adaptiveTimestepEnabled {
		// Adaptive timestep: adjust based on simulation stability
		doAdaptive := e.adaptiveCounter%e.adaptiveInterval == 0

		if doAdaptive {
			// Take a big step and save state
			e.timestep = 2 * e.timestep
			e.PhysicsStep()
			e.revert()

			// Take two half steps (more accurate)
			e.timestep = 0.5 * e.timestep
			e.PhysicsStep()
			e.PhysicsStep()

			e.adjustTimeStep()
		} else {
			e.PhysicsStep()
		}
		e.adaptiveCounter++
	} else {
		// Static timestep
		e.timestep = e.options.Timestep
		e.PhysicsStep()
	}

	if e.stabilized {
		e.revert()
	}
	e.stabilizationIterations++
}

// moveNodes applies forces to move nodes and check for stabilization
func (e *PhysicsEngine) moveNodes() {
	nodeIndices := e.physicsBody.PhysicsNodeIndices
	var maxNodeVelocity float64
	var averageNodeVelocity float64

	const velocityAdaptiveThreshold = 5.0

	for _, nodeID := range nodeIndices {
		nodeVelocity := e.performStep(nodeID)
		if nodeVelocity > maxNodeVelocity {
			maxNodeVelocity = nodeVelocity
		}
		averageNodeVelocity += nodeVelocity
	}

	// Check stabilization conditions
	if len(nodeIndices) > 0 {
		e.adaptiveTimestepEnabled = averageNodeVelocity/float64(len(nodeIndices)) < velocityAdaptiveThreshold
	}
	e.stabilized = maxNodeVelocity < e.options.MinVelocity
}

// performStep moves a single node based on accumulated forces
func (e *PhysicsEngine) performStep(nodeID string) float64 {
	node := e.nodes[nodeID]
	if node == nil {
		return 0
	}

	force := e.physicsBody.Forces[nodeID]
	if force == nil {
		return 0
	}

	velocity := e.physicsBody.Velocities[nodeID]
	if velocity == nil {
		velocity = &Vec2{}
		e.physicsBody.Velocities[nodeID] = velocity
	}

	// Store state for potential revert
	x, y := node.GetPosition()
	e.previousStates[nodeID] = &nodeState{
		x: x, y: y,
		vx: velocity.X, vy: velocity.Y,
	}

	fixedX, fixedY := node.IsFixed()
	mass := node.GetMass()
	if mass <= 0 {
		mass = 1
	}

	// Update X component
	if !fixedX {
		velocity.X = e.calculateComponentVelocity(velocity.X, force.X, mass)
		x += velocity.X * e.timestep
	} else {
		force.X = 0
		velocity.X = 0
	}

	// Update Y component
	if !fixedY {
		velocity.Y = e.calculateComponentVelocity(velocity.Y, force.Y, mass)
		y += velocity.Y * e.timestep
	} else {
		force.Y = 0
		velocity.Y = 0
	}

	node.SetPosition(x, y)

	return math.Sqrt(velocity.X*velocity.X + velocity.Y*velocity.Y)
}

// calculateComponentVelocity computes new velocity for one axis
func (e *PhysicsEngine) calculateComponentVelocity(v, f, m float64) float64 {
	// Damping force
	df := e.options.BarnesHut.Damping * v
	// Acceleration
	a := (f - df) / m
	// Update velocity
	v += a * e.timestep

	// Clamp velocity
	maxV := e.options.MaxVelocity
	if maxV <= 0 {
		maxV = 1e9
	}
	if math.Abs(v) > maxV {
		if v > 0 {
			v = maxV
		} else {
			v = -maxV
		}
	}

	return v
}

// revert restores nodes to their previous state
func (e *PhysicsEngine) revert() {
	e.referenceState = make(map[string]*Vec2)

	for nodeID, state := range e.previousStates {
		node := e.nodes[nodeID]
		if node == nil {
			delete(e.previousStates, nodeID)
			continue
		}

		// Save reference state for adaptive timestep comparison
		x, y := node.GetPosition()
		e.referenceState[nodeID] = &Vec2{X: x, Y: y}

		// Restore previous state
		velocity := e.physicsBody.Velocities[nodeID]
		if velocity != nil {
			velocity.X = state.vx
			velocity.Y = state.vy
		}
		node.SetPosition(state.x, state.y)
	}
}

// adjustTimeStep modifies timestep based on simulation quality
func (e *PhysicsEngine) adjustTimeStep() {
	const factor = 1.2
	const posThreshold = 0.3

	// Check if the step was good quality
	goodQuality := true
	for nodeID, ref := range e.referenceState {
		node := e.nodes[nodeID]
		if node == nil {
			continue
		}
		x, y := node.GetPosition()
		dx := x - ref.X
		dy := y - ref.Y
		dpos := math.Sqrt(dx*dx + dy*dy)
		if dpos > posThreshold {
			goodQuality = false
			break
		}
	}

	if goodQuality {
		e.timestep = factor * e.timestep
	} else {
		if e.timestep/factor < e.options.Timestep {
			e.timestep = e.options.Timestep
		} else {
			e.adaptiveCounter = -1
			e.timestep = math.Max(e.options.Timestep, e.timestep/factor)
		}
	}
}

// IsStabilized returns whether the simulation has settled
func (e *PhysicsEngine) IsStabilized() bool {
	return e.stabilized
}

// SetStabilized manually sets the stabilization state
func (e *PhysicsEngine) SetStabilized(s bool) {
	e.stabilized = s
}

// GetStabilizationIterations returns the number of iterations performed
func (e *PhysicsEngine) GetStabilizationIterations() int {
	return e.stabilizationIterations
}

// ResetStabilization resets stabilization state for a new run
func (e *PhysicsEngine) ResetStabilization() {
	e.stabilized = false
	e.stabilizationIterations = 0
	e.adaptiveTimestep = e.options.Timestep > 0
}

// Stabilize runs physics until stable or max iterations reached
func (e *PhysicsEngine) Stabilize(maxIterations int) int {
	if maxIterations <= 0 {
		maxIterations = e.options.StabilizationIterations
	}

	e.UpdatePhysicsData()
	e.ResetStabilization()

	for !e.stabilized && e.stabilizationIterations < maxIterations {
		e.PhysicsTick()
	}

	return e.stabilizationIterations
}

// RunSingleStep runs one physics step, returning whether stabilized
func (e *PhysicsEngine) RunSingleStep() bool {
	e.PhysicsTick()
	return e.stabilized
}
