package physics

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- test doubles -------------------------------------------------------

type fakeNode struct {
	id             string
	x, y           float64
	mass, size     float64
	fixedX, fixedY bool
}

func (n *fakeNode) GetID() string                   { return n.id }
func (n *fakeNode) GetPosition() (float64, float64) { return n.x, n.y }
func (n *fakeNode) SetPosition(x, y float64)        { n.x, n.y = x, y }
func (n *fakeNode) GetMass() float64                { return n.mass }
func (n *fakeNode) GetSize() float64                { return n.size }
func (n *fakeNode) IsFixed() (bool, bool)           { return n.fixedX, n.fixedY }

type fakeEdge struct {
	id, from, to string
	length       float64
	connected    bool
}

func (e *fakeEdge) GetID() string      { return e.id }
func (e *fakeEdge) GetFromID() string  { return e.from }
func (e *fakeEdge) GetToID() string    { return e.to }
func (e *fakeEdge) GetLength() float64 { return e.length }
func (e *fakeEdge) IsConnected() bool  { return e.connected }

func bodyWith(ids ...string) *PhysicsBody {
	pb := NewPhysicsBody()
	pb.PhysicsNodeIndices = append(pb.PhysicsNodeIndices, ids...)
	for _, id := range ids {
		pb.Forces[id] = &Vec2{}
		pb.EnsureVelocity(id)
	}
	return pb
}

// --- types.go -----------------------------------------------------------

func TestPhysicsBody(t *testing.T) {
	pb := NewPhysicsBody()
	require.NotNil(t, pb.Forces)
	require.NotNil(t, pb.Velocities)

	pb.PhysicsNodeIndices = []string{"a", "b"}
	pb.Forces["a"] = &Vec2{X: 5, Y: 5} // existing entry is zeroed
	// "b" has no entry yet → ResetForces must allocate one.
	pb.ResetForces()
	require.Equal(t, &Vec2{}, pb.Forces["a"])
	require.Equal(t, &Vec2{}, pb.Forces["b"])

	pb.EnsureVelocity("c")
	require.NotNil(t, pb.Velocities["c"])
	v := pb.Velocities["c"]
	pb.EnsureVelocity("c") // idempotent: same pointer
	require.Same(t, v, pb.Velocities["c"])
}

func TestDefaultOptions(t *testing.T) {
	bh := DefaultBarnesHutOptions()
	require.Equal(t, 0.5, bh.Theta)
	require.Equal(t, -2000.0, bh.GravitationalConstant)
	require.Equal(t, 95.0, bh.SpringLength)

	po := DefaultPhysicsOptions()
	require.True(t, po.Enabled)
	require.Equal(t, 50.0, po.MaxVelocity)
	require.Equal(t, 1000, po.StabilizationIterations)
	require.Equal(t, bh, po.BarnesHut)
}

// --- gravity.go ---------------------------------------------------------

func TestCentralGravitySolver(t *testing.T) {
	n := &fakeNode{id: "a", x: 100, y: 0, mass: 1}
	nodes := map[string]PhysicsNode{"a": n}
	pb := bodyWith("a")
	s := NewCentralGravitySolver(nodes, pb, DefaultBarnesHutOptions())

	s.Solve()
	// Pulled toward center: dx=-100, gravityForce=0.3/100 → Fx = -0.3.
	require.InDelta(t, -0.3, pb.Forces["a"].X, 1e-9)
	require.InDelta(t, 0, pb.Forces["a"].Y, 1e-9)

	// Node exactly at center → zero force (distance==0 branch).
	n.x, n.y = 0, 0
	s.Solve()
	require.Equal(t, &Vec2{}, pb.Forces["a"])

	// SetOptions takes effect.
	s.SetOptions(BarnesHutOptions{CentralGravity: 1})
	s.SetOptions("not options") // wrong type ignored
	n.x, n.y = 10, 0
	s.Solve()
	require.InDelta(t, -1.0, pb.Forces["a"].X, 1e-9) // 1/10 * -10

	// nil node referenced by index is skipped.
	pb2 := bodyWith("missing")
	NewCentralGravitySolver(map[string]PhysicsNode{}, pb2, DefaultBarnesHutOptions()).Solve()
}

// --- spring.go ----------------------------------------------------------

func TestSpringSolver(t *testing.T) {
	n1 := &fakeNode{id: "a", x: 0, y: 0, mass: 1}
	n2 := &fakeNode{id: "b", x: 200, y: 0, mass: 1}
	nodes := map[string]PhysicsNode{"a": n1, "b": n2}
	edges := map[string]PhysicsEdge{"e": &fakeEdge{id: "e", from: "a", to: "b", connected: true}}

	pb := bodyWith("a", "b")
	pb.PhysicsEdgeIndices = []string{"e"}
	s := NewSpringSolver(nodes, edges, pb, DefaultBarnesHutOptions())
	s.Solve()

	// Too far apart (200 > 142.5 default) → attractive: a pulled +x, b pulled -x.
	require.InDelta(t, 2.3, pb.Forces["a"].X, 1e-9)
	require.InDelta(t, -2.3, pb.Forces["b"].X, 1e-9)
}

func TestSpringSolver_Skips(t *testing.T) {
	n := &fakeNode{id: "a", mass: 1}
	nodes := map[string]PhysicsNode{"a": n}

	edges := map[string]PhysicsEdge{
		"missing":  (&fakeEdge{id: "missing", from: "a", to: "a", connected: true}),
		"disconn":  (&fakeEdge{id: "disconn", from: "a", to: "b", connected: false}),
		"selfloop": (&fakeEdge{id: "selfloop", from: "a", to: "a", connected: true}),
		"nonode":   (&fakeEdge{id: "nonode", from: "a", to: "ghost", connected: true}),
	}
	pb := bodyWith("a")
	pb.PhysicsEdgeIndices = []string{"missing", "disconn", "selfloop", "nonode", "absent"}
	s := NewSpringSolver(nodes, edges, pb, DefaultBarnesHutOptions())
	require.NotPanics(t, s.Solve)
	// No valid edge applied a force.
	require.Equal(t, &Vec2{}, pb.Forces["a"])

	s.SetOptions("ignored")
	s.SetOptions(DefaultBarnesHutOptions())
}

func TestSpringSolver_ExplicitLength(t *testing.T) {
	n1 := &fakeNode{id: "a", x: 0, y: 0, mass: 1}
	n2 := &fakeNode{id: "b", x: 50, y: 0, mass: 1}
	nodes := map[string]PhysicsNode{"a": n1, "b": n2}
	edges := map[string]PhysicsEdge{"e": &fakeEdge{id: "e", from: "a", to: "b", length: 50, connected: true}}
	pb := bodyWith("a", "b")
	pb.PhysicsEdgeIndices = []string{"e"}
	NewSpringSolver(nodes, edges, pb, DefaultBarnesHutOptions()).Solve()
	// distance == edgeLength → zero spring force.
	require.InDelta(t, 0, pb.Forces["a"].X, 1e-9)
}

// --- barneshut.go -------------------------------------------------------

func TestBarnesHutSolver_NoOps(t *testing.T) {
	nodes := map[string]PhysicsNode{"a": &fakeNode{id: "a", mass: 1}}

	// G == 0 → returns early.
	pb := bodyWith("a")
	opts := DefaultBarnesHutOptions()
	opts.GravitationalConstant = 0
	NewBarnesHutSolver(nodes, pb, opts).Solve()
	require.Equal(t, &Vec2{}, pb.Forces["a"])

	// Empty indices → returns early.
	empty := NewPhysicsBody()
	NewBarnesHutSolver(nodes, empty, DefaultBarnesHutOptions()).Solve()
}

func TestBarnesHutSolver_Repulsion(t *testing.T) {
	a := &fakeNode{id: "a", x: 0, y: 0, mass: 1}
	b := &fakeNode{id: "b", x: 100, y: 0, mass: 1}
	nodes := map[string]PhysicsNode{"a": a, "b": b}
	pb := bodyWith("a", "b")

	NewBarnesHutSolver(nodes, pb, DefaultBarnesHutOptions()).Solve()

	// Repulsive (G negative): a pushed toward -x, b toward +x.
	require.Less(t, pb.Forces["a"].X, 0.0)
	require.Greater(t, pb.Forces["b"].X, 0.0)
}

func TestBarnesHutSolver_OverlapAndManyNodes(t *testing.T) {
	nodes := map[string]PhysicsNode{}
	pb := NewPhysicsBody()
	// A spread of nodes forces tree splits / recursion across quadrants.
	coords := [][2]float64{{0, 0}, {100, 0}, {0, 100}, {100, 100}, {50, 50}, {-50, -50}, {200, 200}}
	for i, c := range coords {
		id := string(rune('a' + i))
		nodes[id] = &fakeNode{id: id, x: c[0], y: c[1], mass: 1, size: 10}
		pb.PhysicsNodeIndices = append(pb.PhysicsNodeIndices, id)
		pb.Forces[id] = &Vec2{}
	}
	opts := DefaultBarnesHutOptions()
	opts.AvoidOverlap = 0.5 // exercises the overlap-avoidance branch
	s := NewBarnesHutSolver(nodes, pb, opts)
	require.NotPanics(t, s.Solve)
	require.NotNil(t, s.Tree)
	require.NotNil(t, s.Tree.Root)

	// Two exactly-overlapping nodes hit the jitter branch without panicking.
	on := map[string]PhysicsNode{"x": &fakeNode{id: "x", x: 5, y: 5, mass: 1}, "y": &fakeNode{id: "y", x: 5, y: 5, mass: 1}}
	opb := bodyWith("x", "y")
	require.NotPanics(t, NewBarnesHutSolver(on, opb, DefaultBarnesHutOptions()).Solve)
}

// --- engine.go ----------------------------------------------------------

func triangleEngine() (*PhysicsEngine, map[string]*fakeNode) {
	a := &fakeNode{id: "a", x: -50, y: 0, mass: 1}
	b := &fakeNode{id: "b", x: 50, y: 0, mass: 1}
	c := &fakeNode{id: "c", x: 0, y: 80, mass: 1}
	nodes := map[string]PhysicsNode{"a": a, "b": b, "c": c}
	edges := map[string]PhysicsEdge{
		"ab": &fakeEdge{id: "ab", from: "a", to: "b", connected: true},
		"bc": &fakeEdge{id: "bc", from: "b", to: "c", connected: true},
		"ca": &fakeEdge{id: "ca", from: "c", to: "a", connected: true},
	}
	return NewPhysicsEngine(nodes, edges), map[string]*fakeNode{"a": a, "b": b, "c": c}
}

func TestEngine_OptionsAndUpdate(t *testing.T) {
	e, _ := triangleEngine()

	require.Equal(t, DefaultPhysicsOptions(), e.GetOptions())

	opts := DefaultPhysicsOptions()
	opts.Timestep = 0.25
	e.SetOptions(opts)
	require.Equal(t, 0.25, e.GetOptions().Timestep)

	e.UpdatePhysicsData()
	require.Len(t, e.physicsBody.PhysicsNodeIndices, 3)
	require.Len(t, e.physicsBody.PhysicsEdgeIndices, 3)
	require.Len(t, e.physicsBody.Forces, 3)
	require.Len(t, e.physicsBody.Velocities, 3)

	// A stale velocity for a now-deleted node is cleaned up on the next update.
	e.physicsBody.Velocities["ghost"] = &Vec2{}
	e.UpdatePhysicsData()
	_, ok := e.physicsBody.Velocities["ghost"]
	require.False(t, ok)
}

func TestEngine_PhysicsStepMovesNodes(t *testing.T) {
	e, ns := triangleEngine()
	e.UpdatePhysicsData()
	before := map[string][2]float64{}
	for id, n := range ns {
		before[id] = [2]float64{n.x, n.y}
	}
	e.PhysicsStep()
	moved := false
	for id, n := range ns {
		if n.x != before[id][0] || n.y != before[id][1] {
			moved = true
		}
	}
	require.True(t, moved, "PhysicsStep should move at least one node")
}

func TestEngine_StabilizeSingleNode(t *testing.T) {
	n := &fakeNode{id: "a", x: 0, y: 0, mass: 1}
	e := NewPhysicsEngine(map[string]PhysicsNode{"a": n}, map[string]PhysicsEdge{})
	iters := e.Stabilize(0) // 0 → use default max
	require.Positive(t, iters)
	require.True(t, e.IsStabilized())
	require.Equal(t, iters, e.GetStabilizationIterations())
}

func TestEngine_StabilizeGraph(t *testing.T) {
	e, _ := triangleEngine()
	iters := e.Stabilize(1000)
	require.Positive(t, iters)
	require.LessOrEqual(t, iters, 1000)
	// Positions must remain finite throughout the adaptive-timestep run.
	for _, id := range e.physicsBody.PhysicsNodeIndices {
		x, y := e.nodes[id].GetPosition()
		require.False(t, math.IsNaN(x) || math.IsInf(x, 0))
		require.False(t, math.IsNaN(y) || math.IsInf(y, 0))
	}
}

func TestEngine_StabilizationControls(t *testing.T) {
	e, _ := triangleEngine()
	e.UpdatePhysicsData()

	e.SetStabilized(true)
	require.True(t, e.IsStabilized())
	// PhysicsTick is a no-op when already stabilized.
	e.PhysicsTick()

	e.ResetStabilization()
	require.False(t, e.IsStabilized())
	require.Zero(t, e.GetStabilizationIterations())

	require.IsType(t, false, e.RunSingleStep())
}

func TestEngine_PerformStep_FixedNode(t *testing.T) {
	n := &fakeNode{id: "a", x: 10, y: 20, mass: 1, fixedX: true, fixedY: true}
	e := NewPhysicsEngine(map[string]PhysicsNode{"a": n}, map[string]PhysicsEdge{})
	e.UpdatePhysicsData()
	e.physicsBody.Forces["a"] = &Vec2{X: 100, Y: 100}

	v := e.performStep("a")
	require.Zero(t, v)          // fixed → no velocity
	require.Equal(t, 10.0, n.x) // position unchanged
	require.Equal(t, 20.0, n.y)
	require.Equal(t, &Vec2{}, e.physicsBody.Forces["a"]) // forces zeroed
}

func TestEngine_PerformStep_ZeroMassAndNilGuards(t *testing.T) {
	n := &fakeNode{id: "a", x: 0, y: 0, mass: 0} // mass<=0 → treated as 1
	e := NewPhysicsEngine(map[string]PhysicsNode{"a": n}, map[string]PhysicsEdge{})
	e.UpdatePhysicsData()
	e.physicsBody.Forces["a"] = &Vec2{X: 10, Y: 10}
	require.NotPanics(t, func() { e.performStep("a") })
	require.NotEqual(t, 0.0, n.x) // it moved

	// nil node / nil force guards.
	require.Zero(t, e.performStep("does-not-exist"))
	e.nodes["b"] = &fakeNode{id: "b", mass: 1}
	require.Zero(t, e.performStep("b")) // no force entry → 0
}

func TestEngine_PerformStep_VelocityClampUnbounded(t *testing.T) {
	n := &fakeNode{id: "a", x: 0, y: 0, mass: 1}
	e := NewPhysicsEngine(map[string]PhysicsNode{"a": n}, map[string]PhysicsEdge{})
	opts := e.GetOptions()
	opts.MaxVelocity = 0 // <=0 → effectively unbounded (1e9 cap)
	e.SetOptions(opts)
	e.UpdatePhysicsData()
	e.physicsBody.Forces["a"] = &Vec2{X: 1e6, Y: 0}
	require.NotPanics(t, func() { e.performStep("a") })
}

func TestEngine_Revert(t *testing.T) {
	n := &fakeNode{id: "a", x: 7, y: 9, mass: 1}
	e := NewPhysicsEngine(map[string]PhysicsNode{"a": n}, map[string]PhysicsEdge{})
	e.UpdatePhysicsData()
	e.physicsBody.Forces["a"] = &Vec2{X: 100, Y: 100}

	e.performStep("a") // records previousState (7,9) then moves the node
	require.NotEqual(t, 7.0, n.x)

	e.revert()
	require.Equal(t, 7.0, n.x) // restored
	require.Equal(t, 9.0, n.y)

	// referenceState captured the post-step (pre-revert) position.
	require.NotNil(t, e.referenceState["a"])
}
