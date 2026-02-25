//go:build js && wasm

package ui

import (
	"math"
	"sort"
)

// Satellite orbit constants (matching TypeScript constants.ts)
const (
	SatelliteEmoji       = "🛰️"
	OrbitLanes           = 3
	OrbitLaneSpacing     = 60.0
	OrbitAngularVelocity = 0.0008
)

// GroupBoundary represents the boundary of a cluster group
type GroupBoundary struct {
	Points           []Point
	CenterX, CenterY float64
	Radius           float64
	Level            string // "outer" for country, "inner" for IP
}

// Point represents a 2D point
type Point struct {
	X, Y float64
}

// PlacedCircle represents a placed circle in circle-packing
type PlacedCircle struct {
	X, Y, Radius float64
	Key          string
}

// SatelliteOrbit holds orbit parameters for unknown-country nodes
type SatelliteOrbit struct {
	Angle     float64
	Lane      int
	IsDragged bool
}

// ipGroupInfo holds IP group data within a country
type ipGroupInfo struct {
	ipGroup int
	nodes   []*Node
	radius  float64
}

// applyCirclePackingLayout positions nodes into cluster groups using circle-packing
// This matches the TypeScript enableDualGrouping function for dual mode
func (a *App) applyCirclePackingLayout() {
	if a.graph == nil || len(a.graph.Nodes) == 0 {
		return
	}

	// Build hierarchy: country -> ipGroup -> nodes
	// Include DMSG servers in their country clusters (matching TypeScript)
	type countryData struct {
		country    string
		ipGroups   map[int][]*Node
		noIPNodes  []*Node
		totalNodes int
	}

	countryMap := make(map[string]*countryData)

	for _, node := range a.graph.Nodes {
		country := node.Country
		if country == "" {
			country = "_unknown"
		}

		if countryMap[country] == nil {
			countryMap[country] = &countryData{
				country:  country,
				ipGroups: make(map[int][]*Node),
			}
		}
		cd := countryMap[country]
		cd.totalNodes++

		// Assign to IP group or no-IP group
		if a.clusterByIP && a.ipGroupsEnabled && node.IPGroup > 0 {
			cd.ipGroups[node.IPGroup] = append(cd.ipGroups[node.IPGroup], node)
		} else {
			cd.noIPNodes = append(cd.noIPNodes, node)
		}
	}

	// Convert to slice and sort by total nodes (largest first)
	var countries []*countryData
	for _, cd := range countryMap {
		countries = append(countries, cd)
	}
	sort.Slice(countries, func(i, j int) bool {
		return countries[i].totalNodes > countries[j].totalNodes
	})

	// Calculate radius for each country
	// In dual mode, need to fit all IP sub-circles inside
	for _, cd := range countries {
		if a.clusterByCountry && a.clusterByIP && len(cd.ipGroups) > 1 {
			// Dual mode: calculate radius to fit all IP sub-circles
			var totalSubCircleArea float64
			for _, nodes := range cd.ipGroups {
				subRadius := calculateGroupRadius(len(nodes))
				totalSubCircleArea += math.Pi * math.Pow(subRadius+25, 2)
			}
			// Add space for noIP nodes
			if len(cd.noIPNodes) > 0 {
				totalSubCircleArea *= 1.15
			}
			// Use 40% packing efficiency
			requiredRadius := math.Sqrt(totalSubCircleArea/(math.Pi*0.40)) + 40

			// Also ensure radius is large enough for the largest sub-circle
			var maxSubRadius float64
			for _, nodes := range cd.ipGroups {
				r := calculateGroupRadius(len(nodes))
				if r > maxSubRadius {
					maxSubRadius = r
				}
			}
			cd.totalNodes = int(math.Max(requiredRadius, maxSubRadius*1.8))
		}
	}

	// Place country circles using circle-packing (no overlaps)
	// Skip _unknown country - those will be satellites
	placedCountries := []PlacedCircle{}
	a.countryBoundaries = make(map[string]GroupBoundary)
	countryCentroids := make(map[string]PlacedCircle)
	var unknownCountryNodes []*Node

	for _, cd := range countries {
		if !a.clusterByCountry && !a.clusterByIP {
			continue
		}

		// Collect unknown country nodes for satellite orbit
		if cd.country == "_unknown" {
			for _, nodes := range cd.ipGroups {
				unknownCountryNodes = append(unknownCountryNodes, nodes...)
			}
			unknownCountryNodes = append(unknownCountryNodes, cd.noIPNodes...)
			continue // Don't create a country circle for unknown
		}

		var radius float64
		if a.clusterByCountry && a.clusterByIP && len(cd.ipGroups) > 1 {
			// Dual mode: use pre-calculated radius for sub-circles
			var totalSubCircleArea float64
			for _, nodes := range cd.ipGroups {
				subRadius := calculateGroupRadius(len(nodes))
				totalSubCircleArea += math.Pi * math.Pow(subRadius+25, 2)
			}
			if len(cd.noIPNodes) > 0 {
				totalSubCircleArea *= 1.15
			}
			requiredRadius := math.Sqrt(totalSubCircleArea/(math.Pi*0.40)) + 40
			var maxSubRadius float64
			for _, nodes := range cd.ipGroups {
				r := calculateGroupRadius(len(nodes))
				if r > maxSubRadius {
					maxSubRadius = r
				}
			}
			radius = math.Max(requiredRadius, maxSubRadius*1.8)
		} else {
			radius = calculateGroupRadius(cd.totalNodes)
		}

		pos := findNonOverlappingPosition(radius, placedCountries, 80)
		placedCountries = append(placedCountries, PlacedCircle{
			X:      pos.X,
			Y:      pos.Y,
			Radius: radius,
			Key:    cd.country,
		})
		countryCentroids[cd.country] = PlacedCircle{X: pos.X, Y: pos.Y, Radius: radius}

		// Store country boundary
		a.countryBoundaries[cd.country] = GroupBoundary{
			Points:  createCirclePoints(pos.X, pos.Y, radius),
			CenterX: pos.X,
			CenterY: pos.Y,
			Radius:  radius,
			Level:   "outer",
		}
	}

	// Now place IP sub-circles within each country using Fibonacci spiral
	a.ipGroupBoundaries = make(map[int]GroupBoundary)

	for _, cd := range countries {
		// Skip _unknown - those are satellites, not in a country circle
		if cd.country == "_unknown" {
			continue
		}

		cc := countryCentroids[cd.country]

		if !a.clusterByIP || len(cd.ipGroups) == 0 {
			// No IP grouping or no IP data - place all nodes in country circle
			allNodes := cd.noIPNodes
			for _, nodes := range cd.ipGroups {
				allNodes = append(allNodes, nodes...)
			}
			positionNodesInGroup(allNodes, cc.X, cc.Y, cc.Radius)
			continue
		}

		// Sort IP groups by size (largest first), skip empty groups
		var ipGroups []ipGroupInfo
		for ipGroup, nodes := range cd.ipGroups {
			if len(nodes) == 0 {
				continue // Skip empty IP groups
			}
			ipGroups = append(ipGroups, ipGroupInfo{
				ipGroup: ipGroup,
				nodes:   nodes,
				radius:  calculateGroupRadius(len(nodes)),
			})
		}
		sort.Slice(ipGroups, func(i, j int) bool {
			return ipGroups[i].radius > ipGroups[j].radius
		})

		if len(ipGroups) <= 1 && len(cd.noIPNodes) == 0 {
			// Single IP group - place nodes directly in country circle
			if len(ipGroups) == 1 {
				positionNodesInGroup(ipGroups[0].nodes, cc.X, cc.Y, cc.Radius)
				a.ipGroupBoundaries[ipGroups[0].ipGroup] = GroupBoundary{
					Points:  createCirclePoints(cc.X, cc.Y, cc.Radius*0.9),
					CenterX: cc.X,
					CenterY: cc.Y,
					Radius:  cc.Radius * 0.9,
					Level:   "inner",
				}
			}
			continue
		}

		// Multiple IP groups - use Fibonacci spiral placement within country
		placedSubs := placeFibonacciSpiral(ipGroups, cc, cd.country)

		for i, ig := range ipGroups {
			pos := placedSubs[i]
			positionNodesInGroup(ig.nodes, pos.X, pos.Y, ig.radius)

			a.ipGroupBoundaries[ig.ipGroup] = GroupBoundary{
				Points:  createCirclePoints(pos.X, pos.Y, ig.radius),
				CenterX: pos.X,
				CenterY: pos.Y,
				Radius:  ig.radius,
				Level:   "inner",
			}
		}

		// Place noIP nodes scattered within country, avoiding IP sub-circles
		if len(cd.noIPNodes) > 0 {
			placeNodesScattered(cd.noIPNodes, cc, placedSubs)
		}
	}

	// Initialize satellite orbits for unknown-country nodes
	// These orbit around the country clusters (matching TypeScript grouping.ts)
	if len(unknownCountryNodes) > 0 && len(placedCountries) > 0 {
		a.initializeSatelliteOrbits(unknownCountryNodes, placedCountries)
	} else {
		// Clear satellites if none needed
		a.satelliteOrbits = nil
		a.satelliteEnabled = false
	}

	// Start boundary enforcement to keep nodes in groups
	a.startBoundaryEnforcement()
}

// placeFibonacciSpiral places IP sub-circles within a country circle
// Uses Vogel spiral (golden angle) matching TypeScript implementation exactly
func placeFibonacciSpiral(ipGroups []ipGroupInfo, cc PlacedCircle, countryKey string) []PlacedCircle {
	goldenAngle := math.Pi * (3 - math.Sqrt(5)) // ~137.508 degrees
	subPadding := 35.0                          // Increased padding to prevent overlap

	if len(ipGroups) == 0 {
		return []PlacedCircle{}
	}

	positions := make([]PlacedCircle, 0, len(ipGroups))

	// First (largest) circle goes at center
	positions = append(positions, PlacedCircle{X: cc.X, Y: cc.Y, Radius: ipGroups[0].radius})

	if len(ipGroups) == 1 {
		return positions
	}

	// Estimate initial scaling factor based on average sub-circle size (matching TypeScript exactly)
	var totalRadius float64
	for _, g := range ipGroups {
		totalRadius += g.radius
	}
	avgRadius := totalRadius / float64(len(ipGroups))
	scaleFactor := (avgRadius + subPadding) * 0.85

	// Debug: track placement stats
	fallbackCount := 0

	for i := 1; i < len(ipGroups); i++ {
		subRadius := ipGroups[i].radius
		placed := false

		// Walk along the Vogel spiral testing candidate positions
		for n := i; n < i+200; n++ {
			angle := float64(n) * goldenAngle
			dist := scaleFactor * math.Sqrt(float64(n))
			x := cc.X + dist*math.Cos(angle)
			y := cc.Y + dist*math.Sin(angle)

			// Check within country boundary
			distFromCenter := math.Sqrt((x-cc.X)*(x-cc.X) + (y-cc.Y)*(y-cc.Y))
			if distFromCenter+subRadius > cc.Radius-30 {
				continue
			}

			// Check overlap with already-placed sub-circles
			overlaps := false
			for _, p := range positions {
				d := math.Sqrt((x-p.X)*(x-p.X) + (y-p.Y)*(y-p.Y))
				if d < subRadius+p.Radius+subPadding {
					overlaps = true
					break
				}
			}

			if !overlaps {
				positions = append(positions, PlacedCircle{X: x, Y: y, Radius: subRadius})
				placed = true
				break
			}
		}

		// Fallback: if spiral couldn't place it, try with increased scale
		if !placed {
			fallbackCount++
			scaleFactor *= 1.15
			for n := 1; n < 500; n++ {
				angle := float64(n) * goldenAngle
				dist := scaleFactor * math.Sqrt(float64(n))
				x := cc.X + dist*math.Cos(angle)
				y := cc.Y + dist*math.Sin(angle)

				distFromCenter := math.Sqrt((x-cc.X)*(x-cc.X) + (y-cc.Y)*(y-cc.Y))
				if distFromCenter+subRadius > cc.Radius-20 {
					continue
				}

				overlaps := false
				for _, p := range positions {
					d := math.Sqrt((x-p.X)*(x-p.X) + (y-p.Y)*(y-p.Y))
					if d < subRadius+p.Radius+subPadding {
						overlaps = true
						break
					}
				}

				if !overlaps {
					positions = append(positions, PlacedCircle{X: x, Y: y, Radius: subRadius})
					placed = true
					break
				}
			}
		}

		// Ultimate fallback: find position with minimum overlap
		if !placed {
			bestX, bestY := cc.X, cc.Y
			bestOverlap := math.MaxFloat64

			// Try many positions to find one with least overlap
			for angle := 0.0; angle < 2*math.Pi; angle += math.Pi / 16 {
				for dist := 0.0; dist <= cc.Radius-subRadius-10; dist += 15 {
					x := cc.X + dist*math.Cos(angle)
					y := cc.Y + dist*math.Sin(angle)

					var totalOverlap float64
					for _, p := range positions {
						d := math.Sqrt((x-p.X)*(x-p.X) + (y-p.Y)*(y-p.Y))
						minD := subRadius + p.Radius + subPadding
						if d < minD {
							totalOverlap += minD - d
						}
					}

					if totalOverlap < bestOverlap {
						bestOverlap = totalOverlap
						bestX, bestY = x, y
						if totalOverlap == 0 {
							break
						}
					}
				}
				if bestOverlap == 0 {
					break
				}
			}

			positions = append(positions, PlacedCircle{X: bestX, Y: bestY, Radius: subRadius})
		}
	}

	// Log summary only for countries with 3+ IP groups
	if len(ipGroups) >= 3 {
		consoleLog("[spiral] " + countryKey + ": " + itoa(len(ipGroups)) + " groups, ccR=" + ftoa(cc.Radius) + ", scale=" + ftoa(scaleFactor) + ", fallbacks=" + itoa(fallbackCount))
	}

	return positions
}

// placeNodesScattered places nodes randomly within a country circle, avoiding placed sub-circles
func placeNodesScattered(nodes []*Node, cc PlacedCircle, placedSubs []PlacedCircle) {
	margin := 50.0
	placed := 0
	attempts := 0
	count := len(nodes)

	for placed < count && attempts < count*50 {
		angle := randFloat() * 2 * math.Pi
		r := randFloat() * (cc.Radius - margin)
		x := cc.X + r*math.Cos(angle)
		y := cc.Y + r*math.Sin(angle)

		valid := true
		for _, sub := range placedSubs {
			dist := math.Sqrt((x-sub.X)*(x-sub.X) + (y-sub.Y)*(y-sub.Y))
			if dist < sub.Radius+30 {
				valid = false
				break
			}
		}

		if valid {
			nodes[placed].X = x
			nodes[placed].Y = y
			nodes[placed].VX = 0
			nodes[placed].VY = 0
			placed++
		}
		attempts++
	}

	// Fallback: place remaining nodes at edge of country
	for placed < count {
		angle := float64(placed) / float64(count) * 2 * math.Pi
		r := cc.Radius - margin
		nodes[placed].X = cc.X + r*math.Cos(angle)
		nodes[placed].Y = cc.Y + r*math.Sin(angle)
		nodes[placed].VX = 0
		nodes[placed].VY = 0
		placed++
	}
}

// Simple pseudo-random for scatter placement
var randSeed uint32 = 12345

func randFloat() float64 {
	randSeed = randSeed*1103515245 + 12345
	return float64(randSeed&0x7fffffff) / float64(0x7fffffff)
}

// calculateGroupRadius returns the radius needed for a group of nodes
func calculateGroupRadius(nodeCount int) float64 {
	// Area per node (with spacing) - matching TypeScript exactly
	nodeArea := 2500.0 // ~50x50 per node with spacing
	return math.Max(80, math.Sqrt(float64(nodeCount)*nodeArea/math.Pi)+40)
}

// findNonOverlappingPosition finds a position for a circle that doesn't overlap
func findNonOverlappingPosition(radius float64, placed []PlacedCircle, padding float64) Point {
	if len(placed) == 0 {
		return Point{0, 0}
	}

	// Try concentric rings with multiple angles per ring
	minSpacing := radius + padding
	for ring := 1; ring <= 50; ring++ {
		ringRadius := minSpacing * float64(ring) * 0.4
		numAngles := max(8, ring*4)

		for i := 0; i < numAngles; i++ {
			angle := float64(i)/float64(numAngles)*2*math.Pi + float64(ring%2)*(math.Pi/float64(numAngles))
			x := ringRadius * math.Cos(angle)
			y := ringRadius * math.Sin(angle)

			if !circleOverlaps(x, y, radius, placed, padding) {
				return Point{x, y}
			}
		}
	}

	// Fallback: hexagonal packing
	cols := int(math.Ceil(math.Sqrt(float64(len(placed) + 1))))
	row := len(placed) / cols
	col := len(placed) % cols
	spacing := (radius + padding) * 2.5
	xOffset := 0.0
	if row%2 == 1 {
		xOffset = spacing * 0.5
	}
	return Point{float64(col)*spacing + xOffset, float64(row) * spacing * 0.866}
}

// circleOverlaps checks if a circle overlaps with any placed circles
func circleOverlaps(x, y, radius float64, placed []PlacedCircle, padding float64) bool {
	for _, p := range placed {
		dx := x - p.X
		dy := y - p.Y
		dist := math.Sqrt(dx*dx + dy*dy)
		minDist := radius + p.Radius + padding
		if dist < minDist {
			return true
		}
	}
	return false
}

// positionNodesInGroup positions nodes within a circular group using Fibonacci spiral
func positionNodesInGroup(nodes []*Node, cx, cy, radius float64) {
	if len(nodes) == 0 {
		return
	}

	// Single node goes at center
	if len(nodes) == 1 {
		nodes[0].X = cx
		nodes[0].Y = cy
		nodes[0].VX = 0
		nodes[0].VY = 0
		return
	}

	// Sort nodes by connection count (most connected at center)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ConnectionCount > nodes[j].ConnectionCount
	})

	innerRadius := radius - 40
	count := len(nodes)

	// Use different placement strategies based on count
	if count <= 6 {
		// Small groups: use circular placement
		for i, node := range nodes {
			angle := float64(i) / float64(count) * 2 * math.Pi
			r := innerRadius * 0.5
			node.X = cx + r*math.Cos(angle)
			node.Y = cy + r*math.Sin(angle)
			node.VX = 0
			node.VY = 0
		}
	} else {
		// Larger groups: use Fibonacci spiral (golden angle)
		goldenAngle := math.Pi * (3 - math.Sqrt(5)) // ~137.5 degrees

		for i, node := range nodes {
			angle := float64(i) * goldenAngle
			r := innerRadius * math.Sqrt(float64(i)/float64(count)) * 0.9
			node.X = cx + r*math.Cos(angle)
			node.Y = cy + r*math.Sin(angle)
			node.VX = 0
			node.VY = 0
		}
	}
}

// createCirclePoints creates boundary points for a circular group
func createCirclePoints(cx, cy, radius float64) []Point {
	numPoints := 32
	points := make([]Point, numPoints)

	for i := 0; i < numPoints; i++ {
		angle := float64(i) / float64(numPoints) * 2 * math.Pi
		points[i] = Point{
			X: cx + (radius+20)*math.Cos(angle),
			Y: cy + (radius+20)*math.Sin(angle),
		}
	}

	return points
}

// Deprecated - use createCirclePoints instead
func createCircleBoundary(cx, cy, radius float64) GroupBoundary {
	return GroupBoundary{
		Points:  createCirclePoints(cx, cy, radius),
		CenterX: cx,
		CenterY: cy,
		Radius:  radius,
	}
}

// startBoundaryEnforcement starts an interval to keep nodes within their groups
func (a *App) startBoundaryEnforcement() {
	// Boundary enforcement will be called via setInterval in setupSidebarCallbacks
	a.boundaryEnforcementEnabled = true
}

// enforceBoundaries applies gravity toward group centers (matching TypeScript grouping.ts)
// This is called every 30ms via setInterval to continuously keep nodes in their groups
func (a *App) enforceBoundaries() {
	if !a.boundaryEnforcementEnabled {
		return
	}

	// Base gravity strength (TypeScript uses 0.05 + cross-connections factor up to 0.45)
	baseGravityStrength := 0.15
	minNodeDist := 35.0

	// Country boundaries - apply to all nodes with that country
	if a.clusterByCountry {
		for country, boundary := range a.countryBoundaries {
			var countryNodes []*Node
			for _, node := range a.graph.Nodes {
				nodeCountry := node.Country
				if nodeCountry == "" {
					nodeCountry = "_unknown"
				}
				if nodeCountry != country || node.IsPinned {
					continue
				}
				countryNodes = append(countryNodes, node)
			}

			// Apply gravity toward country center
			for _, node := range countryNodes {
				dx := node.X - boundary.CenterX
				dy := node.Y - boundary.CenterY
				dist := math.Sqrt(dx*dx + dy*dy)

				if dist < 1 {
					continue
				}

				// Hard boundary - keep node inside country circle
				maxDist := boundary.Radius - 40
				if dist > maxDist && maxDist > 0 {
					scale := maxDist / dist
					node.X = boundary.CenterX + dx*scale
					node.Y = boundary.CenterY + dy*scale
					node.VX *= 0.5
					node.VY *= 0.5
				} else if dist > boundary.Radius*0.7 {
					// Gentle pull when near edge
					pull := baseGravityStrength * 1.5
					node.X -= (dx / dist) * pull
					node.Y -= (dy / dist) * pull
				} else if dist > 5 {
					// Light gravity toward center
					pull := baseGravityStrength * 0.3
					node.X -= (dx / dist) * pull
					node.Y -= (dy / dist) * pull
				}
			}

			// Node-to-node repulsion within country
			for i := 0; i < len(countryNodes); i++ {
				for j := i + 1; j < len(countryNodes); j++ {
					nodeA := countryNodes[i]
					nodeB := countryNodes[j]

					dx := nodeB.X - nodeA.X
					dy := nodeB.Y - nodeA.Y
					dist := math.Sqrt(dx*dx + dy*dy)

					if dist < minNodeDist && dist > 0 {
						overlap := minNodeDist - dist
						pushX := (dx / dist) * overlap * 0.3
						pushY := (dy / dist) * overlap * 0.3

						nodeA.X -= pushX
						nodeA.Y -= pushY
						nodeB.X += pushX
						nodeB.Y += pushY
					}
				}
			}
		}
	}

	// IP group boundaries (tighter enforcement)
	if a.clusterByIP && a.ipGroupsEnabled {
		for group, boundary := range a.ipGroupBoundaries {
			for _, node := range a.graph.Nodes {
				if node.IPGroup != group || node.IsPinned {
					continue
				}

				dx := node.X - boundary.CenterX
				dy := node.Y - boundary.CenterY
				dist := math.Sqrt(dx*dx + dy*dy)

				if dist < 1 {
					continue
				}

				// Hard boundary for IP groups
				maxDist := boundary.Radius - 30
				if dist > maxDist && maxDist > 0 {
					scale := maxDist / dist
					node.X = boundary.CenterX + dx*scale
					node.Y = boundary.CenterY + dy*scale
					node.VX *= 0.5
					node.VY *= 0.5
				} else if dist > boundary.Radius*0.6 {
					// Stronger pull for IP groups
					pull := baseGravityStrength * 2
					node.X -= (dx / dist) * pull
					node.Y -= (dy / dist) * pull
				}
			}
		}

		// Repel non-IP nodes from IP sub-circles (nodes with country but no IP group)
		// Matching TypeScript: _country nodes are pushed OUT of IP sub-circles
		for _, node := range a.graph.Nodes {
			if node.IPGroup > 0 || node.IsPinned || node.IsSatellite {
				continue // Skip nodes that belong to IP groups or are satellites
			}

			// Find this node's country boundary
			nodeCountry := node.Country
			if nodeCountry == "" {
				nodeCountry = "_unknown"
			}
			countryBoundary, hasCountry := a.countryBoundaries[nodeCountry]

			// Gravity toward country center - matching TypeScript formula
			// TypeScript uses: nodeX -= dx * gravityStrength (0.05 to 0.45)
			if hasCountry {
				dx := node.X - countryBoundary.CenterX
				dy := node.Y - countryBoundary.CenterY
				dist := math.Sqrt(dx*dx + dy*dy)
				if dist > 5 {
					// Simple proportional gravity like TypeScript
					gravityStrength := 0.15 // Stronger for non-IP nodes (they should flow to center)
					node.X -= dx * gravityStrength
					node.Y -= dy * gravityStrength
				}
			}

			// Repel from all IP sub-circles - matching TypeScript formula exactly
			// TypeScript: pushScale = (minDist - sDist) / sDist; nodeX += sdx * pushScale * 0.8
			for _, boundary := range a.ipGroupBoundaries {
				dx := node.X - boundary.CenterX
				dy := node.Y - boundary.CenterY
				dist := math.Sqrt(dx*dx + dy*dy)

				minDist := boundary.Radius + 25
				if dist < minDist && dist > 0 {
					pushScale := (minDist - dist) / dist
					node.X += dx * pushScale * 0.8
					node.Y += dy * pushScale * 0.8
				}
			}
		}
	}
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// initializeSatelliteOrbits sets up orbiting nodes for unknown-country nodes
// (matching TypeScript grouping.ts calculateOrbitParams and initializeSatelliteOrbits)
func (a *App) initializeSatelliteOrbits(nodes []*Node, placedCountries []PlacedCircle) {
	if len(nodes) == 0 || len(placedCountries) == 0 {
		return
	}

	// Calculate orbit center as center of all placed countries
	var sumX, sumY float64
	for _, c := range placedCountries {
		sumX += c.X
		sumY += c.Y
	}
	a.orbitCenter = Point{
		X: sumX / float64(len(placedCountries)),
		Y: sumY / float64(len(placedCountries)),
	}

	// Calculate base radius as max extent from center + margin
	var maxExtent float64
	for _, c := range placedCountries {
		dx := c.X - a.orbitCenter.X
		dy := c.Y - a.orbitCenter.Y
		extent := math.Sqrt(dx*dx+dy*dy) + c.Radius
		if extent > maxExtent {
			maxExtent = extent
		}
	}
	a.orbitBaseRadius = maxExtent + 150

	// Initialize orbit parameters for each satellite node
	a.satelliteOrbits = make(map[string]*SatelliteOrbit)
	angleStep := (2 * math.Pi) / float64(len(nodes))

	for i, node := range nodes {
		lane := i % OrbitLanes
		angle := float64(i) * angleStep

		a.satelliteOrbits[node.ID] = &SatelliteOrbit{
			Angle: angle,
			Lane:  lane,
		}

		// Position satellite at initial orbit position
		r := a.orbitBaseRadius + float64(lane)*OrbitLaneSpacing
		node.X = a.orbitCenter.X + r*math.Cos(angle)
		node.Y = a.orbitCenter.Y + r*math.Sin(angle)
		node.VX = 0
		node.VY = 0

		// Mark as satellite for rendering
		node.IsSatellite = true
	}

	a.satelliteEnabled = true
}

// animateSatellites updates satellite positions (called each frame)
func (a *App) animateSatellites() {
	if !a.satelliteEnabled || a.satelliteOrbits == nil {
		return
	}

	for nodeID, orbit := range a.satelliteOrbits {
		if orbit.IsDragged {
			continue
		}

		node := a.graph.Nodes[nodeID]
		if node == nil {
			continue
		}

		// Update angle
		orbit.Angle += OrbitAngularVelocity

		// Calculate new position
		r := a.orbitBaseRadius + float64(orbit.Lane)*OrbitLaneSpacing
		node.X = a.orbitCenter.X + r*math.Cos(orbit.Angle)
		node.Y = a.orbitCenter.Y + r*math.Sin(orbit.Angle)
	}

	a.needsRedraw = true
}
