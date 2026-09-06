package physics

import (
	"math"
	"math/rand"
)

// Region represents quadtree quadrants
type Region int

const (
	RegionNW Region = iota
	RegionNE
	RegionSW
	RegionSE
)

// Range represents a bounding box
type Range struct {
	MinX, MaxX float64
	MinY, MaxY float64
}

// Branch represents a node in the Barnes-Hut quadtree
type Branch struct {
	// CenterOfMass is the weighted center of all contained nodes
	CenterOfMass Vec2
	// Mass is the total mass of all contained nodes
	Mass float64
	// Range is the bounding box of this branch
	Range Range
	// Size is the width/height of this square region
	Size float64
	// CalcSize is 1/Size for faster calculations
	CalcSize float64
	// MaxWidth tracks the largest node in this branch
	MaxWidth float64
	// Level is the depth in the tree
	Level int
	// ChildrenCount is 0 (empty), 1 (leaf), or 4 (internal)
	ChildrenCount int
	// Children contains the four quadrant branches (when ChildrenCount==4)
	Children [4]*Branch
	// Data contains a single node (when ChildrenCount==1)
	Data PhysicsNode
}

// BarnesHutTree is the root container for the quadtree
type BarnesHutTree struct {
	Root *Branch
}

// BarnesHutSolver implements the Barnes-Hut N-body algorithm
// for O(N log N) gravitational force calculations
type BarnesHutSolver struct {
	nodes       map[string]PhysicsNode
	physicsBody *PhysicsBody
	options     BarnesHutOptions

	thetaInversed          float64
	overlapAvoidanceFactor float64

	// For debugging
	Tree *BarnesHutTree
}

// NewBarnesHutSolver creates a new Barnes-Hut physics solver
func NewBarnesHutSolver(nodes map[string]PhysicsNode, physicsBody *PhysicsBody, options BarnesHutOptions) *BarnesHutSolver {
	solver := &BarnesHutSolver{
		nodes:       nodes,
		physicsBody: physicsBody,
	}
	solver.SetOptions(options)
	return solver
}

// SetOptions updates the solver configuration
func (s *BarnesHutSolver) SetOptions(opts interface{}) {
	if o, ok := opts.(BarnesHutOptions); ok {
		s.options = o
		s.thetaInversed = 1 / o.Theta

		// if 1 then min distance = 0.5, if 0.5 then min distance = 0.5 + 0.5*node.radius
		s.overlapAvoidanceFactor = 1 - math.Max(0, math.Min(1, o.AvoidOverlap))
	}
}

// Solve calculates gravitational forces between all nodes using Barnes-Hut
func (s *BarnesHutSolver) Solve() {
	if s.options.GravitationalConstant == 0 {
		return
	}
	if len(s.physicsBody.PhysicsNodeIndices) == 0 {
		return
	}

	// Build the quadtree
	tree := s.formBarnesHutTree()
	s.Tree = tree // Store for debugging

	// Calculate forces for each node
	for _, nodeID := range s.physicsBody.PhysicsNodeIndices {
		node := s.nodes[nodeID]
		if node == nil {
			continue
		}
		if node.GetMass() > 0 {
			// Starting with root is irrelevant, it never passes the BH condition
			s.getForceContributions(tree.Root, node)
		}
	}
}

// getForceContributions calculates forces from all four quadrants
func (s *BarnesHutSolver) getForceContributions(branch *Branch, node PhysicsNode) {
	for i := 0; i < 4; i++ {
		if branch.Children[i] != nil {
			s.getForceContribution(branch.Children[i], node)
		}
	}
}

// getForceContribution traverses the tree, approximating distant regions
func (s *BarnesHutSolver) getForceContribution(branch *Branch, node PhysicsNode) {
	// Empty regions contribute no force
	if branch.ChildrenCount == 0 {
		return
	}

	// Get distance from node to center of mass
	nodeX, nodeY := node.GetPosition()
	dx := branch.CenterOfMass.X - nodeX
	dy := branch.CenterOfMass.Y - nodeY
	distance := math.Sqrt(dx*dx + dy*dy)

	// Barnes-Hut condition: d * (1/s) > 1/theta
	// If the region is far enough away, approximate with center of mass
	if distance*branch.CalcSize > s.thetaInversed {
		s.calculateForces(distance, dx, dy, node, branch)
	} else {
		// Region is too close, need to go deeper
		if branch.ChildrenCount == 4 {
			s.getForceContributions(branch, node)
		} else {
			// Leaf node with single element
			if branch.Data != nil && branch.Data.GetID() != node.GetID() {
				s.calculateForces(distance, dx, dy, node, branch)
			}
		}
	}
}

// calculateForces computes the gravitational force between a node and a branch
func (s *BarnesHutSolver) calculateForces(distance, dx, dy float64, node PhysicsNode, branch *Branch) {
	if distance == 0 {
		distance = 0.1
		dx = distance
	}

	// Handle overlap avoidance
	nodeRadius := node.GetSize()
	if s.overlapAvoidanceFactor < 1 && nodeRadius > 0 {
		distance = math.Max(
			0.1+s.overlapAvoidanceFactor*nodeRadius,
			distance-nodeRadius,
		)
	}

	// Gravitational force: G * m1 * m2 / d^2
	// Dividing by d^3 instead of d^2 lets us get fx, fy without trig
	// F = G * M * m / d^2
	// fx = dx/d * F = dx * G * M * m / d^3
	gravityForce := (s.options.GravitationalConstant * branch.Mass * node.GetMass()) /
		(distance * distance * distance)

	fx := dx * gravityForce
	fy := dy * gravityForce

	nodeID := node.GetID()
	if s.physicsBody.Forces[nodeID] != nil {
		s.physicsBody.Forces[nodeID].X += fx
		s.physicsBody.Forces[nodeID].Y += fy
	}
}

// formBarnesHutTree builds the quadtree from all physics nodes
func (s *BarnesHutSolver) formBarnesHutTree() *BarnesHutTree {
	nodeIndices := s.physicsBody.PhysicsNodeIndices
	if len(nodeIndices) == 0 {
		return &BarnesHutTree{}
	}

	// Find the bounding box of all nodes
	firstNode := s.nodes[nodeIndices[0]]
	firstX, firstY := firstNode.GetPosition()
	minX, maxX := firstX, firstX
	minY, maxY := firstY, firstY

	for i := 1; i < len(nodeIndices); i++ {
		node := s.nodes[nodeIndices[i]]
		if node == nil || node.GetMass() <= 0 {
			continue
		}
		x, y := node.GetPosition()
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}

	// Make the range a square
	sizeDiff := math.Abs(maxX-minX) - math.Abs(maxY-minY)
	if sizeDiff > 0 {
		minY -= 0.5 * sizeDiff
		maxY += 0.5 * sizeDiff
	} else {
		minX += 0.5 * sizeDiff
		maxX -= 0.5 * sizeDiff
	}

	const minimumTreeSize = 1e-5
	rootSize := math.Max(minimumTreeSize, math.Abs(maxX-minX))
	halfRootSize := 0.5 * rootSize
	centerX := 0.5 * (minX + maxX)
	centerY := 0.5 * (minY + maxY)

	// Create the root branch
	root := &Branch{
		CenterOfMass:  Vec2{X: 0, Y: 0},
		Mass:          0,
		Range:         Range{MinX: centerX - halfRootSize, MaxX: centerX + halfRootSize, MinY: centerY - halfRootSize, MaxY: centerY + halfRootSize},
		Size:          rootSize,
		CalcSize:      1 / rootSize,
		MaxWidth:      0,
		Level:         0,
		ChildrenCount: 4,
	}

	// Split the root into quadrants
	s.splitBranch(root)

	// Insert all nodes into the tree
	for _, nodeID := range nodeIndices {
		node := s.nodes[nodeID]
		if node != nil && node.GetMass() > 0 {
			s.placeInTree(root, node)
		}
	}

	return &BarnesHutTree{Root: root}
}

// updateBranchMass updates a branch's center of mass when adding a node
func (s *BarnesHutSolver) updateBranchMass(branch *Branch, node PhysicsNode) {
	nodeMass := node.GetMass()
	nodeX, nodeY := node.GetPosition()
	totalMass := branch.Mass + nodeMass
	totalMassInv := 1 / totalMass

	branch.CenterOfMass.X = (branch.CenterOfMass.X*branch.Mass + nodeX*nodeMass) * totalMassInv
	branch.CenterOfMass.Y = (branch.CenterOfMass.Y*branch.Mass + nodeY*nodeMass) * totalMassInv
	branch.Mass = totalMass

	// Track largest node size for overlap calculations
	nodeSize := node.GetSize()
	if nodeSize > branch.MaxWidth {
		branch.MaxWidth = nodeSize
	}
}

// placeInTree recursively places a node into the appropriate branch
func (s *BarnesHutSolver) placeInTree(branch *Branch, node PhysicsNode) {
	// Update the mass/center of this branch
	s.updateBranchMass(branch, node)

	// Determine which quadrant the node belongs to
	nodeX, nodeY := node.GetPosition()
	var region Region

	// Use NW range to determine boundaries (all quadrants share edges)
	nw := branch.Children[RegionNW]
	if nw == nil {
		return
	}

	if nw.Range.MaxX > nodeX {
		// In NW or SW
		if nw.Range.MaxY > nodeY {
			region = RegionNW
		} else {
			region = RegionSW
		}
	} else {
		// In NE or SE
		if nw.Range.MaxY > nodeY {
			region = RegionNE
		} else {
			region = RegionSE
		}
	}

	s.placeInRegion(branch, node, region)
}

// placeInRegion places a node into a specific quadrant
func (s *BarnesHutSolver) placeInRegion(branch *Branch, node PhysicsNode, region Region) {
	child := branch.Children[region]
	if child == nil {
		return
	}

	switch child.ChildrenCount {
	case 0:
		// Empty region - place node here as a leaf
		child.Data = node
		child.ChildrenCount = 1
		s.updateBranchMass(child, node)

	case 1:
		// Already has one node - check for exact overlap
		existingX, existingY := child.Data.GetPosition()
		nodeX, nodeY := node.GetPosition()

		if existingX == nodeX && existingY == nodeY {
			// Nodes exactly overlap - jitter the new one slightly
			// #nosec G404 -- math/rand is fine for visual jitter, not security
			node.SetPosition(nodeX+rand.Float64()*0.1, nodeY+rand.Float64()*0.1)
		} else {
			// Split this region and re-insert both nodes
			s.splitBranch(child)
			s.placeInTree(child, node)
		}

	case 4:
		// Internal node with children - recurse
		s.placeInTree(child, node)
	}
}

// splitBranch splits a branch into four quadrants
func (s *BarnesHutSolver) splitBranch(branch *Branch) {
	// Save any existing node to re-insert after splitting
	var containedNode PhysicsNode
	if branch.ChildrenCount == 1 {
		containedNode = branch.Data
		branch.Mass = 0
		branch.CenterOfMass.X = 0
		branch.CenterOfMass.Y = 0
	}

	branch.ChildrenCount = 4
	branch.Data = nil

	// Create the four child regions
	s.insertRegion(branch, RegionNW)
	s.insertRegion(branch, RegionNE)
	s.insertRegion(branch, RegionSW)
	s.insertRegion(branch, RegionSE)

	// Re-insert the contained node if there was one
	if containedNode != nil {
		s.placeInTree(branch, containedNode)
	}
}

// insertRegion creates a child branch for a specific quadrant
func (s *BarnesHutSolver) insertRegion(branch *Branch, region Region) {
	childSize := 0.5 * branch.Size
	var minX, maxX, minY, maxY float64

	switch region {
	case RegionNW:
		minX = branch.Range.MinX
		maxX = branch.Range.MinX + childSize
		minY = branch.Range.MinY
		maxY = branch.Range.MinY + childSize
	case RegionNE:
		minX = branch.Range.MinX + childSize
		maxX = branch.Range.MaxX
		minY = branch.Range.MinY
		maxY = branch.Range.MinY + childSize
	case RegionSW:
		minX = branch.Range.MinX
		maxX = branch.Range.MinX + childSize
		minY = branch.Range.MinY + childSize
		maxY = branch.Range.MaxY
	case RegionSE:
		minX = branch.Range.MinX + childSize
		maxX = branch.Range.MaxX
		minY = branch.Range.MinY + childSize
		maxY = branch.Range.MaxY
	}

	branch.Children[region] = &Branch{
		CenterOfMass:  Vec2{X: 0, Y: 0},
		Mass:          0,
		Range:         Range{MinX: minX, MaxX: maxX, MinY: minY, MaxY: maxY},
		Size:          childSize,
		CalcSize:      2 * branch.CalcSize,
		MaxWidth:      0,
		Level:         branch.Level + 1,
		ChildrenCount: 0,
	}
}
