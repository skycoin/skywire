//go:build js && wasm

package ui

import (
	"github.com/skycoin/skywire/pkg/visnetwork/physics"
)

// PhysicsAdapter bridges the tpviz graph with the vis-network physics engine
type PhysicsAdapter struct {
	engine      *physics.PhysicsEngine
	nodeMap     map[string]physics.PhysicsNode
	edgeMap     map[string]physics.PhysicsEdge
	initialized bool
}

// NewPhysicsAdapter creates a new physics adapter
func NewPhysicsAdapter() *PhysicsAdapter {
	return &PhysicsAdapter{
		nodeMap: make(map[string]physics.PhysicsNode),
		edgeMap: make(map[string]physics.PhysicsEdge),
	}
}

// UpdateFromGraph syncs the physics engine with the current graph state
func (p *PhysicsAdapter) UpdateFromGraph(g *Graph) {
	// Clear and rebuild maps
	p.nodeMap = make(map[string]physics.PhysicsNode)
	p.edgeMap = make(map[string]physics.PhysicsEdge)

	for id, node := range g.Nodes {
		p.nodeMap[id] = node
	}

	for id, edge := range g.Edges {
		p.edgeMap[id] = edge
	}

	// Create or update the engine
	if p.engine == nil {
		p.engine = physics.NewPhysicsEngine(p.nodeMap, p.edgeMap)
	} else {
		// Just update the data, engine will rebuild indices
		p.engine = physics.NewPhysicsEngine(p.nodeMap, p.edgeMap)
	}

	p.engine.UpdatePhysicsData()
	p.initialized = true
}

// Configure sets physics options based on clustering mode
func (p *PhysicsAdapter) Configure(clustering bool) {
	if p.engine == nil {
		return
	}

	opts := physics.DefaultPhysicsOptions()

	if clustering {
		// Very weak physics when clustering is enabled (matching TypeScript grouping.ts)
		// TypeScript: gravitationalConstant: -300, springConstant: 0.00002, springLength: 30, damping: 0.98
		// These extremely weak settings let boundary enforcement control positioning
		opts.BarnesHut.GravitationalConstant = -300
		opts.BarnesHut.SpringConstant = 0.00002
		opts.BarnesHut.SpringLength = 30
		opts.BarnesHut.Damping = 0.98
		opts.MaxVelocity = 20
	} else {
		// Standard physics (matching vis-network defaults)
		opts.BarnesHut.GravitationalConstant = -2000
		opts.BarnesHut.SpringConstant = 0.04
		opts.BarnesHut.SpringLength = 95
		opts.BarnesHut.Damping = 0.09
		opts.MaxVelocity = 50
	}

	p.engine.SetOptions(opts)
}

// Step runs one physics iteration
func (p *PhysicsAdapter) Step() bool {
	if p.engine == nil || !p.initialized {
		return true
	}
	return p.engine.RunSingleStep()
}

// IsStabilized returns whether physics has settled
func (p *PhysicsAdapter) IsStabilized() bool {
	if p.engine == nil {
		return true
	}
	return p.engine.IsStabilized()
}

// Reset restarts stabilization
func (p *PhysicsAdapter) Reset() {
	if p.engine != nil {
		p.engine.ResetStabilization()
	}
}
