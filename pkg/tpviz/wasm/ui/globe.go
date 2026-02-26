//go:build js && wasm

package ui

import (
	"math"
)

// GlobeRenderer renders a 3D globe with orthographic projection using Canvas 2D
type GlobeRenderer struct {
	canvas   *Canvas
	graph    *Graph
	view     *View
	opts     *RenderOptions
	rotation GlobeRotation
	active   bool

	// Cached node positions on sphere (lat, lon in radians)
	nodeGeoPos map[string]GeoPosition
}

// GlobeRotation holds the current rotation angles
type GlobeRotation struct {
	X, Y       float64 // Rotation in radians
	AutoRotate bool
	Speed      float64
}

// GeoPosition holds latitude and longitude in radians
type GeoPosition struct {
	Lat, Lon float64
}

// CountryCoord holds approximate country centroid coordinates
type CountryCoord struct {
	Lat, Lon float64
}

// Country coordinates (lat, lon in degrees) - approximate centers
var countryCoords = map[string]CountryCoord{
	"US": {37.0902, -95.7129},
	"CA": {56.1304, -106.3468},
	"MX": {23.6345, -102.5528},
	"BR": {-14.2350, -51.9253},
	"AR": {-38.4161, -63.6167},
	"CL": {-35.6751, -71.5430},
	"CO": {4.5709, -74.2973},
	"PE": {-9.1900, -75.0152},
	"VE": {6.4238, -66.5897},
	"GB": {55.3781, -3.4360},
	"DE": {51.1657, 10.4515},
	"FR": {46.2276, 2.2137},
	"ES": {40.4637, -3.7492},
	"IT": {41.8719, 12.5674},
	"PT": {39.3999, -8.2245},
	"NL": {52.1326, 5.2913},
	"BE": {50.5039, 4.4699},
	"CH": {46.8182, 8.2275},
	"AT": {47.5162, 14.5501},
	"PL": {51.9194, 19.1451},
	"CZ": {49.8175, 15.4730},
	"SE": {60.1282, 18.6435},
	"NO": {60.4720, 8.4689},
	"DK": {56.2639, 9.5018},
	"FI": {61.9241, 25.7482},
	"RU": {61.5240, 105.3188},
	"UA": {48.3794, 31.1656},
	"RO": {45.9432, 24.9668},
	"HU": {47.1625, 19.5033},
	"GR": {39.0742, 21.8243},
	"TR": {38.9637, 35.2433},
	"CN": {35.8617, 104.1954},
	"JP": {36.2048, 138.2529},
	"KR": {35.9078, 127.7669},
	"IN": {20.5937, 78.9629},
	"TH": {15.8700, 100.9925},
	"VN": {14.0583, 108.2772},
	"ID": {-0.7893, 113.9213},
	"MY": {4.2105, 101.9758},
	"SG": {1.3521, 103.8198},
	"PH": {12.8797, 121.7740},
	"AU": {-25.2744, 133.7751},
	"NZ": {-40.9006, 174.8860},
	"ZA": {-30.5595, 22.9375},
	"EG": {26.8206, 30.8025},
	"NG": {9.0820, 8.6753},
	"KE": {-0.0236, 37.9062},
	"IL": {31.0461, 34.8516},
	"AE": {23.4241, 53.8478},
	"SA": {23.8859, 45.0792},
	"IR": {32.4279, 53.6880},
	"PK": {30.3753, 69.3451},
	"BD": {23.6850, 90.3563},
	"HK": {22.3193, 114.1694},
	"TW": {23.6978, 120.9605},
	"IE": {53.4129, -8.2439},
	"SK": {48.6690, 19.6990},
	"BG": {42.7339, 25.4858},
	"HR": {45.1000, 15.2000},
	"SI": {46.1512, 14.9955},
	"RS": {44.0165, 21.0059},
	"LT": {55.1694, 23.8813},
	"LV": {56.8796, 24.6032},
	"EE": {58.5953, 25.0136},
}

// Simplified continent outlines (lon, lat pairs in degrees)
// These are rough polygons for visual effect
var continentOutlines = [][]Point{
	// North America
	{
		{-168, 65}, {-140, 70}, {-130, 72}, {-100, 73}, {-85, 70}, {-75, 60},
		{-55, 50}, {-67, 45}, {-70, 43}, {-75, 35}, {-80, 25}, {-90, 20},
		{-100, 20}, {-105, 22}, {-117, 32}, {-125, 40}, {-125, 48}, {-135, 55},
		{-150, 60}, {-165, 65}, {-168, 65},
	},
	// South America
	{
		{-80, 10}, {-60, 5}, {-35, -5}, {-35, -20}, {-40, -23}, {-45, -23},
		{-50, -25}, {-55, -30}, {-58, -38}, {-65, -42}, {-68, -52}, {-75, -50},
		{-73, -40}, {-70, -30}, {-70, -18}, {-75, -5}, {-80, 0}, {-80, 10},
	},
	// Europe
	{
		{-10, 36}, {-10, 44}, {-5, 48}, {0, 50}, {5, 51}, {10, 54}, {15, 55},
		{25, 55}, {30, 60}, {28, 70}, {20, 70}, {10, 62}, {5, 60}, {-5, 58},
		{-10, 50}, {-10, 36},
	},
	// Africa
	{
		{-17, 15}, {-15, 28}, {-5, 36}, {10, 37}, {25, 32}, {35, 30}, {40, 12},
		{50, 12}, {50, 0}, {42, -12}, {35, -22}, {28, -33}, {18, -35}, {15, -30},
		{12, -17}, {10, -5}, {0, 5}, {-5, 5}, {-17, 15},
	},
	// Asia
	{
		{30, 40}, {40, 42}, {50, 45}, {60, 55}, {70, 55}, {80, 50}, {90, 45},
		{100, 35}, {110, 35}, {120, 40}, {130, 45}, {140, 45}, {145, 50},
		{150, 60}, {170, 65}, {180, 65}, {180, 70}, {130, 75}, {100, 75},
		{70, 70}, {55, 55}, {40, 45}, {30, 40},
	},
	// Australia
	{
		{114, -22}, {120, -18}, {130, -12}, {142, -11}, {150, -15}, {153, -25},
		{150, -35}, {145, -38}, {135, -35}, {128, -33}, {114, -30}, {114, -22},
	},
}

// NewGlobeRenderer creates a new globe renderer
func NewGlobeRenderer(canvas *Canvas, graph *Graph, view *View, opts *RenderOptions) *GlobeRenderer {
	return &GlobeRenderer{
		canvas:     canvas,
		graph:      graph,
		view:       view,
		opts:       opts,
		nodeGeoPos: make(map[string]GeoPosition),
		rotation: GlobeRotation{
			X:          -0.3, // Slight tilt
			Y:          0,
			AutoRotate: true,
			Speed:      0.002,
		},
		active: false,
	}
}

// SetActive enables or disables the globe view
func (g *GlobeRenderer) SetActive(active bool) {
	g.active = active
}

// IsActive returns whether the globe view is active
func (g *GlobeRenderer) IsActive() bool {
	return g.active
}

// Rotate applies rotation delta
func (g *GlobeRenderer) Rotate(dx, dy float64) {
	g.rotation.Y += dx * 0.01
	g.rotation.X += dy * 0.01
	// Clamp vertical rotation
	if g.rotation.X > math.Pi/2 {
		g.rotation.X = math.Pi / 2
	}
	if g.rotation.X < -math.Pi/2 {
		g.rotation.X = -math.Pi / 2
	}
}

// Update updates the globe state (called each frame)
func (g *GlobeRenderer) Update() {
	if g.rotation.AutoRotate {
		g.rotation.Y += g.rotation.Speed
	}
}

// degToRad converts degrees to radians
func degToRad(deg float64) float64 {
	return deg * math.Pi / 180
}

// latLonTo3D converts lat/lon (in radians) to 3D coordinates
func latLonTo3D(lat, lon, radius float64) (x, y, z float64) {
	x = radius * math.Cos(lat) * math.Sin(lon)
	y = radius * math.Sin(lat)
	z = radius * math.Cos(lat) * math.Cos(lon)
	return
}

// rotate3D applies rotation around X and Y axes
func rotate3D(x, y, z, rotX, rotY float64) (rx, ry, rz float64) {
	// Rotate around Y axis
	cosY := math.Cos(rotY)
	sinY := math.Sin(rotY)
	x1 := x*cosY + z*sinY
	z1 := -x*sinY + z*cosY

	// Rotate around X axis
	cosX := math.Cos(rotX)
	sinX := math.Sin(rotX)
	y1 := y*cosX - z1*sinX
	z2 := y*sinX + z1*cosX

	return x1, y1, z2
}

// project3Dto2D projects a 3D point to 2D screen coordinates (orthographic)
func (g *GlobeRenderer) project3Dto2D(x, y, z, radius float64) (sx, sy float64, visible bool) {
	// Apply rotation
	rx, ry, rz := rotate3D(x, y, z, g.rotation.X, g.rotation.Y)

	// Orthographic projection - only show front hemisphere
	if rz < 0 {
		return 0, 0, false
	}

	// Scale and center
	centerX := g.canvas.Width() / 2
	centerY := g.canvas.Height() / 2
	scale := math.Min(g.canvas.Width(), g.canvas.Height()) * 0.4

	sx = centerX + rx*scale
	sy = centerY - ry*scale // Flip Y for screen coordinates

	return sx, sy, true
}

// Render draws the globe
func (g *GlobeRenderer) Render() {
	if !g.active {
		return
	}

	g.canvas.Clear("#1a1a2e")

	radius := 1.0
	centerX := g.canvas.Width() / 2
	centerY := g.canvas.Height() / 2
	scale := math.Min(g.canvas.Width(), g.canvas.Height()) * 0.4

	// Draw globe background (ocean)
	g.canvas.FillCircle(centerX, centerY, scale, "#0a1628")

	// Draw atmosphere glow
	g.canvas.StrokeCircle(centerX, centerY, scale*1.05, 3, "rgba(0, 150, 200, 0.3)")
	g.canvas.StrokeCircle(centerX, centerY, scale*1.02, 2, "rgba(0, 180, 220, 0.2)")

	// Draw latitude/longitude grid
	g.drawGrid(radius, scale)

	// Draw continents
	g.drawContinents(radius, scale)

	// Calculate node positions
	g.calculateNodePositions()

	// Draw edges (geodesics)
	g.drawEdges(radius, scale)

	// Draw nodes
	g.drawNodes(radius, scale)

	// Draw globe outline
	g.canvas.StrokeCircle(centerX, centerY, scale, 2, "#00d9a5")
}

// drawGrid draws latitude/longitude grid lines
func (g *GlobeRenderer) drawGrid(radius, scale float64) {
	// Longitude lines (meridians)
	for lon := -180.0; lon < 180; lon += 30 {
		lonRad := degToRad(lon)
		points := make([]Point, 0, 37)

		for lat := -90.0; lat <= 90; lat += 5 {
			latRad := degToRad(lat)
			x, y, z := latLonTo3D(latRad, lonRad, radius)
			sx, sy, visible := g.project3Dto2D(x, y, z, radius)
			if visible {
				points = append(points, Point{sx, sy})
			} else if len(points) > 0 {
				// Draw accumulated visible segment
				g.drawPolyline(points, 1, "rgba(50, 80, 120, 0.3)")
				points = points[:0]
			}
		}
		if len(points) > 1 {
			g.drawPolyline(points, 1, "rgba(50, 80, 120, 0.3)")
		}
	}

	// Latitude lines (parallels)
	for lat := -60.0; lat <= 60; lat += 30 {
		latRad := degToRad(lat)
		points := make([]Point, 0, 73)

		for lon := -180.0; lon <= 180; lon += 5 {
			lonRad := degToRad(lon)
			x, y, z := latLonTo3D(latRad, lonRad, radius)
			sx, sy, visible := g.project3Dto2D(x, y, z, radius)
			if visible {
				points = append(points, Point{sx, sy})
			} else if len(points) > 0 {
				g.drawPolyline(points, 1, "rgba(50, 80, 120, 0.3)")
				points = points[:0]
			}
		}
		if len(points) > 1 {
			g.drawPolyline(points, 1, "rgba(50, 80, 120, 0.3)")
		}
	}
}

// drawContinents draws continent outlines
func (g *GlobeRenderer) drawContinents(radius, scale float64) {
	for _, outline := range continentOutlines {
		points := make([]Point, 0, len(outline))
		lastVisible := false

		for _, p := range outline {
			latRad := degToRad(p.Y) // Y is latitude
			lonRad := degToRad(p.X) // X is longitude
			x, y, z := latLonTo3D(latRad, lonRad, radius)
			sx, sy, visible := g.project3Dto2D(x, y, z, radius)

			if visible {
				points = append(points, Point{sx, sy})
				lastVisible = true
			} else if lastVisible && len(points) > 1 {
				// Draw accumulated visible segment
				g.drawPolyline(points, 2, "rgba(0, 217, 165, 0.6)")
				points = points[:0]
				lastVisible = false
			}
		}

		if len(points) > 1 {
			g.drawPolyline(points, 2, "rgba(0, 217, 165, 0.6)")
		}
	}
}

// drawPolyline draws a connected series of points
func (g *GlobeRenderer) drawPolyline(points []Point, lineWidth float64, color string) {
	if len(points) < 2 {
		return
	}
	for i := 0; i < len(points)-1; i++ {
		g.canvas.Line(points[i].X, points[i].Y, points[i+1].X, points[i+1].Y, lineWidth, color)
	}
}

// calculateNodePositions assigns geographic positions to nodes based on country
func (g *GlobeRenderer) calculateNodePositions() {
	countryCounts := make(map[string]int)

	for _, node := range g.graph.Nodes {
		country := node.Country
		if country == "" {
			country = "US" // Default
		}

		coords, ok := countryCoords[country]
		if !ok {
			coords = CountryCoord{0, 0}
		}

		// Add jitter for multiple nodes in same country
		count := countryCounts[country]
		countryCounts[country]++

		jitterLat := float64(count%5-2) * 3
		jitterLon := float64(count/5-2) * 5

		g.nodeGeoPos[node.ID] = GeoPosition{
			Lat: degToRad(coords.Lat + jitterLat),
			Lon: degToRad(coords.Lon + jitterLon),
		}
	}
}

// drawEdges draws transport connections as geodesic arcs
func (g *GlobeRenderer) drawEdges(radius, scale float64) {
	for _, edge := range g.graph.Edges {
		if edge.Hidden {
			continue
		}

		// Check filters
		if !g.opts.ShowEdgeType(edge.Type) {
			continue
		}

		fromPos, ok1 := g.nodeGeoPos[edge.From]
		toPos, ok2 := g.nodeGeoPos[edge.To]
		if !ok1 || !ok2 {
			continue
		}

		// Get edge color
		color := "#ffffff"
		switch edge.Type {
		case TransportSTCPR:
			color = "#00d9a5"
		case TransportSUDPH:
			color = "#00b4d8"
		case TransportDMSG:
			color = "#ffd166"
		}
		if edge.IsLocalEdge || edge.IsLocalOnly {
			color = "#00ffff"
		}

		// Draw geodesic arc
		g.drawGeodesic(fromPos, toPos, radius, scale, color, edge.IsLocalEdge)
	}
}

// drawGeodesic draws a great circle arc between two points
func (g *GlobeRenderer) drawGeodesic(from, to GeoPosition, radius, scale float64, color string, highlight bool) {
	segments := 32
	points := make([]Point, 0, segments+1)

	for i := 0; i <= segments; i++ {
		t := float64(i) / float64(segments)

		// Spherical linear interpolation (slerp)
		lat, lon := slerp(from.Lat, from.Lon, to.Lat, to.Lon, t)

		// Add slight arc height
		arcHeight := math.Sin(t*math.Pi) * 0.1
		r := radius + arcHeight

		x, y, z := latLonTo3D(lat, lon, r)
		sx, sy, visible := g.project3Dto2D(x, y, z, r)

		if visible {
			points = append(points, Point{sx, sy})
		} else if len(points) > 1 {
			lineWidth := 1.0
			if highlight {
				lineWidth = 2.0
			}
			g.drawPolyline(points, lineWidth, color)
			points = points[:0]
		}
	}

	if len(points) > 1 {
		lineWidth := 1.0
		if highlight {
			lineWidth = 2.0
		}
		g.drawPolyline(points, lineWidth, color)
	}
}

// slerp performs spherical linear interpolation between two lat/lon points
func slerp(lat1, lon1, lat2, lon2, t float64) (lat, lon float64) {
	// Convert to 3D vectors
	x1 := math.Cos(lat1) * math.Cos(lon1)
	y1 := math.Sin(lat1)
	z1 := math.Cos(lat1) * math.Sin(lon1)

	x2 := math.Cos(lat2) * math.Cos(lon2)
	y2 := math.Sin(lat2)
	z2 := math.Cos(lat2) * math.Sin(lon2)

	// Calculate angle between vectors
	dot := x1*x2 + y1*y2 + z1*z2
	if dot > 1 {
		dot = 1
	}
	if dot < -1 {
		dot = -1
	}

	theta := math.Acos(dot)

	// Handle nearly identical points
	if theta < 0.001 {
		return lat1 + (lat2-lat1)*t, lon1 + (lon2-lon1)*t
	}

	sinTheta := math.Sin(theta)
	a := math.Sin((1-t)*theta) / sinTheta
	b := math.Sin(t*theta) / sinTheta

	x := a*x1 + b*x2
	y := a*y1 + b*y2
	z := a*z1 + b*z2

	// Convert back to lat/lon
	lat = math.Asin(y)
	lon = math.Atan2(z, x)

	return lat, lon
}

// drawNodes draws visor nodes on the globe
func (g *GlobeRenderer) drawNodes(radius, scale float64) {
	for _, node := range g.graph.Nodes {
		// Check status filters
		if !g.opts.ShowNodeStatus(node.Status) {
			continue
		}

		pos, ok := g.nodeGeoPos[node.ID]
		if !ok {
			continue
		}

		x, y, z := latLonTo3D(pos.Lat, pos.Lon, radius*1.02)
		sx, sy, visible := g.project3Dto2D(x, y, z, radius)

		if !visible {
			continue
		}

		// Determine node color
		color := "#ffd166" // unknown
		switch node.Status {
		case StatusOnline:
			color = "#00d9a5"
		case StatusOffline:
			color = "#e94560"
		}

		if node.IsLocalVisor {
			color = "#00ffff"
		}

		// Draw node
		nodeSize := 4.0 + float64(node.ConnectionCount)/10
		if nodeSize > 12 {
			nodeSize = 12
		}
		if node.IsLocalVisor {
			nodeSize = 10
		}

		g.canvas.FillCircle(sx, sy, nodeSize, color)

		if node.IsLocalVisor {
			g.canvas.StrokeCircle(sx, sy, nodeSize+2, 2, "#ff00ff")
		}

		// Draw country flag or short label if zoomed in enough
		if node.Country != "" && scale > 200 {
			flag := countryToFlag(node.Country)
			if flag != "" {
				g.canvas.Text(flag, sx+nodeSize+2, sy+4, "#ffffff", "12px Arial")
			}
		}
	}
}

// Note: countryToFlag is defined in render.go
