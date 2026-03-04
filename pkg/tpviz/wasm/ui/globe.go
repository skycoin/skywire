//go:build js && wasm

package ui

import (
	"math"
	"syscall/js"
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

	// Voronoi mode: nodes evenly distributed on sphere
	voronoiMode bool
	// Node 3D positions (used in Voronoi mode)
	nodePos3D map[string]Vec3

	// Earth texture
	earthImage  js.Value
	earthLoaded bool
	earthCanvas js.Value // Offscreen canvas for texture rendering
	earthCtx    js.Value
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

// Vec3 holds a 3D vector
type Vec3 struct {
	X, Y, Z float64
}

// Normalize normalizes the vector to unit length
func (v Vec3) Normalize() Vec3 {
	length := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if length == 0 {
		return v
	}
	return Vec3{v.X / length, v.Y / length, v.Z / length}
}

// Scale scales the vector by a scalar
func (v Vec3) Scale(s float64) Vec3 {
	return Vec3{v.X * s, v.Y * s, v.Z * s}
}

// CountryCoord holds approximate country centroid coordinates
type CountryCoord struct {
	Lat, Lon float64
}

// Country colors for Voronoi regions
var countryColors = map[string]string{
	"US": "#1e90ff", "CA": "#dc143c", "MX": "#006400", "BR": "#ffd700", "AR": "#87ceeb",
	"GB": "#4169e1", "DE": "#000000", "FR": "#00008b", "ES": "#ff4500", "IT": "#228b22",
	"NL": "#ff8c00", "BE": "#8b0000", "CH": "#ff0000", "AT": "#dc143c", "PL": "#ffffff",
	"RU": "#0000cd", "UA": "#ffd700", "CN": "#ff0000", "JP": "#ff69b4", "KR": "#4169e1",
	"IN": "#ff8c00", "AU": "#006400", "ZA": "#228b22", "SE": "#ffd700", "NO": "#ff0000",
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
	"UY": {-32.5228, -55.7658}, // Uruguay
	"PY": {-23.4425, -58.4438}, // Paraguay
	"BO": {-16.2902, -63.5887}, // Bolivia
	"EC": {-1.8312, -78.1834},  // Ecuador
	"PA": {8.5380, -80.7821},   // Panama
	"CR": {9.7489, -83.7534},   // Costa Rica
	"NI": {12.8654, -85.2072},  // Nicaragua
	"HN": {15.2, -86.2419},     // Honduras
	"SV": {13.7942, -88.8965},  // El Salvador
	"GT": {15.7835, -90.2308},  // Guatemala
	"BZ": {17.1899, -88.4976},  // Belize
	"CU": {21.5218, -77.7812},  // Cuba
	"DO": {18.7357, -70.1627},  // Dominican Republic
	"JM": {18.1096, -77.2975},  // Jamaica
	"PR": {18.2208, -66.5901},  // Puerto Rico
	"TT": {10.6918, -61.2225},  // Trinidad and Tobago
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
	"BY": {53.7098, 27.9534},  // Belarus
	"MD": {47.4116, 28.3699},  // Moldova
	"AL": {41.1533, 20.1683},  // Albania
	"MK": {41.5124, 21.7453},  // North Macedonia
	"BA": {43.9159, 17.6791},  // Bosnia and Herzegovina
	"ME": {42.7087, 19.3744},  // Montenegro
	"XK": {42.6026, 20.9030},  // Kosovo
	"CY": {35.1264, 33.4299},  // Cyprus
	"LU": {49.8153, 6.1296},   // Luxembourg
	"MT": {35.9375, 14.3754},  // Malta
	"IS": {64.9631, -19.0208}, // Iceland
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
	"MM": {21.9162, 95.9560},  // Myanmar
	"KH": {12.5657, 104.9910}, // Cambodia
	"LA": {19.8563, 102.4955}, // Laos
	"NP": {28.3949, 84.1240},  // Nepal
	"LK": {7.8731, 80.7718},   // Sri Lanka
	"AU": {-25.2744, 133.7751},
	"NZ": {-40.9006, 174.8860},
	"ZA": {-30.5595, 22.9375},
	"EG": {26.8206, 30.8025},
	"NG": {9.0820, 8.6753},
	"KE": {-0.0236, 37.9062},
	"MA": {31.7917, -7.0926}, // Morocco
	"DZ": {28.0339, 1.6596},  // Algeria
	"TN": {33.8869, 9.5375},  // Tunisia
	"GH": {7.9465, -1.0232},  // Ghana
	"TZ": {-6.3690, 34.8888}, // Tanzania
	"ET": {9.1450, 40.4897},  // Ethiopia
	"IL": {31.0461, 34.8516},
	"AE": {23.4241, 53.8478},
	"SA": {23.8859, 45.0792},
	"IR": {32.4279, 53.6880},
	"IQ": {33.2232, 43.6793}, // Iraq
	"JO": {30.5852, 36.2384}, // Jordan
	"LB": {33.8547, 35.8623}, // Lebanon
	"KW": {29.3117, 47.4818}, // Kuwait
	"QA": {25.3548, 51.1839}, // Qatar
	"OM": {21.4735, 55.9754}, // Oman
	"BH": {26.0667, 50.5577}, // Bahrain
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

// Continent outlines (lon, lat pairs in degrees) - more detailed for accuracy
var continentOutlines = [][]Point{
	// North America
	{
		{-168, 66}, {-162, 70}, {-155, 71}, {-140, 70}, {-130, 69}, {-125, 70},
		{-110, 69}, {-95, 69}, {-85, 67}, {-80, 64}, {-68, 60}, {-66, 50}, {-70, 46},
		{-70, 43}, {-75, 40}, {-75, 35}, {-82, 29}, {-81, 25}, {-88, 30}, {-97, 26},
		{-97, 22}, {-106, 23}, {-112, 29}, {-117, 32}, {-120, 34}, {-122, 37},
		{-124, 40}, {-124, 44}, {-123, 48}, {-130, 55}, {-140, 60}, {-147, 61},
		{-153, 58}, {-160, 58}, {-165, 62}, {-168, 66},
	},
	// Greenland
	{
		{-44, 60}, {-42, 65}, {-35, 68}, {-25, 72}, {-18, 76}, {-20, 80},
		{-35, 83}, {-50, 82}, {-55, 78}, {-60, 75}, {-52, 70}, {-44, 62}, {-44, 60},
	},
	// South America
	{
		{-80, 9}, {-75, 11}, {-72, 12}, {-62, 10}, {-52, 4}, {-50, 0}, {-50, -3},
		{-45, -3}, {-41, -3}, {-38, -5}, {-35, -8}, {-35, -15}, {-38, -18}, {-40, -22},
		{-48, -26}, {-53, -28}, {-55, -32}, {-58, -36}, {-62, -38}, {-65, -42},
		{-68, -48}, {-74, -52}, {-70, -55}, {-68, -52}, {-72, -46}, {-75, -42},
		{-72, -35}, {-70, -30}, {-70, -22}, {-75, -15}, {-81, -5}, {-80, 0}, {-80, 9},
	},
	// Europe
	{
		{-10, 36}, {-6, 37}, {-6, 43}, {-2, 43}, {3, 43}, {3, 47}, {-4, 48},
		{-5, 52}, {0, 51}, {5, 52}, {8, 54}, {12, 54}, {14, 55}, {20, 55}, {24, 55},
		{28, 58}, {30, 60}, {28, 64}, {25, 68}, {20, 70}, {15, 68}, {10, 63},
		{5, 62}, {0, 58}, {-5, 56}, {-10, 52}, {-10, 46}, {-10, 36},
	},
	// Africa
	{
		{-17, 21}, {-17, 28}, {-12, 33}, {-5, 36}, {0, 36}, {10, 37}, {20, 32},
		{25, 32}, {32, 31}, {35, 30}, {38, 22}, {43, 12}, {51, 11}, {51, 3},
		{42, -1}, {40, -11}, {35, -20}, {32, -26}, {28, -33}, {20, -35}, {18, -32},
		{15, -27}, {12, -18}, {10, -6}, {5, 4}, {1, 6}, {-5, 5}, {-10, 8},
		{-15, 11}, {-17, 15}, {-17, 21},
	},
	// Asia (Russia, China - main landmass)
	{
		{30, 42}, {35, 43}, {40, 42}, {50, 45}, {60, 50}, {70, 52}, {75, 55},
		{85, 55}, {95, 50}, {105, 52}, {110, 45}, {120, 40}, {125, 43}, {130, 43},
		{135, 45}, {140, 45}, {145, 50}, {155, 55}, {160, 60}, {170, 63}, {180, 65},
		{180, 72}, {170, 70}, {155, 72}, {140, 72}, {120, 73}, {100, 73}, {80, 72},
		{70, 70}, {60, 68}, {55, 60}, {45, 50}, {35, 45}, {30, 42},
	},
	// India
	{
		{68, 24}, {72, 22}, {74, 20}, {78, 15}, {80, 10}, {77, 8}, {74, 10},
		{72, 15}, {68, 22}, {68, 24},
	},
	// Southeast Asia
	{
		{95, 22}, {100, 20}, {105, 15}, {104, 10}, {100, 5}, {100, 2},
		{103, 1}, {100, 0}, {98, 5}, {98, 10}, {95, 16}, {92, 20}, {95, 22},
	},
	// Japan
	{
		{130, 33}, {132, 34}, {135, 35}, {138, 36}, {140, 38}, {141, 41}, {145, 45},
		{144, 42}, {140, 36}, {136, 35}, {130, 33},
	},
	// Australia
	{
		{114, -22}, {117, -20}, {122, -18}, {127, -14}, {132, -12}, {136, -12},
		{140, -11}, {142, -11}, {145, -15}, {150, -18}, {153, -24}, {153, -28},
		{150, -33}, {147, -37}, {143, -39}, {140, -38}, {135, -35}, {130, -32},
		{125, -32}, {118, -32}, {114, -28}, {113, -24}, {114, -22},
	},
	// New Zealand North Island
	{
		{173, -37}, {175, -38}, {177, -39}, {178, -42}, {175, -41}, {173, -39}, {173, -37},
	},
	// New Zealand South Island
	{
		{168, -44}, {170, -43}, {172, -43}, {174, -46}, {170, -47}, {168, -46}, {168, -44},
	},
}

// NewGlobeRenderer creates a new globe renderer
func NewGlobeRenderer(canvas *Canvas, graph *Graph, view *View, opts *RenderOptions) *GlobeRenderer {
	g := &GlobeRenderer{
		canvas:      canvas,
		graph:       graph,
		view:        view,
		opts:        opts,
		nodeGeoPos:  make(map[string]GeoPosition),
		nodePos3D:   make(map[string]Vec3),
		voronoiMode: false, // Geographic mode by default
		rotation: GlobeRotation{
			X:          -0.3, // Slight tilt
			Y:          0,
			AutoRotate: true,
			Speed:      0.002,
		},
		active:      false,
		earthLoaded: false,
	}

	// Load earth texture image
	g.loadEarthTexture()

	return g
}

// loadEarthTexture loads the earth.jpg texture
func (g *GlobeRenderer) loadEarthTexture() {
	doc := js.Global().Get("document")
	img := doc.Call("createElement", "img")

	// Set up load handler
	img.Set("onload", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		g.earthImage = img
		g.earthLoaded = true
		consoleLog("Earth texture loaded: " + itoa(img.Get("width").Int()) + "x" + itoa(img.Get("height").Int()))

		// Create offscreen canvas for spherical projection
		g.earthCanvas = doc.Call("createElement", "canvas")
		g.earthCanvas.Set("width", 512)
		g.earthCanvas.Set("height", 512)
		g.earthCtx = g.earthCanvas.Call("getContext", "2d")

		return nil
	}))

	img.Set("onerror", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		consoleLog("Failed to load earth texture, using fallback")
		g.earthLoaded = false
		return nil
	}))

	// Try to load the texture
	img.Set("src", "textures/earth.jpg")
}

// SetActive enables or disables the globe view
func (g *GlobeRenderer) SetActive(active bool) {
	g.active = active
}

// SetGraph updates the graph reference (called when data is loaded)
func (g *GlobeRenderer) SetGraph(graph *Graph) {
	g.graph = graph
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

// SetVoronoiMode enables or disables Voronoi mode
func (g *GlobeRenderer) SetVoronoiMode(enabled bool) {
	g.voronoiMode = enabled
}

// IsVoronoiMode returns whether Voronoi mode is active
func (g *GlobeRenderer) IsVoronoiMode() bool {
	return g.voronoiMode
}

// fibonacciSphere generates n evenly distributed points on a sphere using Fibonacci spiral
func fibonacciSphere(n int, radius float64) []Vec3 {
	points := make([]Vec3, n)
	goldenRatio := (1 + math.Sqrt(5)) / 2

	for i := 0; i < n; i++ {
		theta := 2 * math.Pi * float64(i) / goldenRatio
		phi := math.Acos(1 - 2*(float64(i)+0.5)/float64(n))

		x := radius * math.Sin(phi) * math.Cos(theta)
		y := radius * math.Sin(phi) * math.Sin(theta)
		z := radius * math.Cos(phi)

		points[i] = Vec3{x, y, z}
	}

	return points
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

	if g.voronoiMode {
		// VORONOI MODE: Transparent globe, show interior lines
		// Draw very faint globe outline only
		g.canvas.StrokeCircle(centerX, centerY, scale, 1, "rgba(0, 217, 165, 0.2)")
	} else {
		// GEOGRAPHIC MODE: Render earth texture or fallback
		// Draw atmosphere glow first (behind globe)
		g.canvas.StrokeCircle(centerX, centerY, scale*1.05, 3, "rgba(0, 150, 200, 0.3)")
		g.canvas.StrokeCircle(centerX, centerY, scale*1.02, 2, "rgba(0, 180, 220, 0.2)")

		// Try to render earth texture
		rendered := g.renderEarthTexture(centerX, centerY, scale)

		if !rendered {
			// Fallback: Draw globe background (ocean) and continents
			g.canvas.FillCircle(centerX, centerY, scale, "#0a1628")
			g.drawGrid(radius, scale)
			g.drawContinents(radius, scale)
		}
	}

	// Calculate node positions
	g.calculateNodePositions()

	// Draw edges (geodesics or interior chords)
	g.drawEdges(radius, scale)

	// Draw nodes
	g.drawNodes(radius, scale)

	// Draw globe outline
	g.canvas.StrokeCircle(centerX, centerY, scale, 2, "#00d9a5")
}

// renderEarthTexture renders the earth texture using JavaScript
func (g *GlobeRenderer) renderEarthTexture(centerX, centerY, scale float64) bool {
	renderFn := js.Global().Get("renderEarthTexture")
	if renderFn.IsUndefined() || renderFn.IsNull() {
		return false
	}

	result := renderFn.Invoke(g.canvas.ctx, g.rotation.X, g.rotation.Y, centerX, centerY, scale)
	return result.Bool()
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

// drawContinents draws filled continent shapes
func (g *GlobeRenderer) drawContinents(radius, scale float64) {
	landColor := "rgba(34, 85, 51, 0.8)"     // Dark green for land
	outlineColor := "rgba(0, 217, 165, 0.6)" // Teal outline

	for _, outline := range continentOutlines {
		points := make([]Point, 0, len(outline))
		allVisible := true

		// Collect all visible points
		for _, p := range outline {
			latRad := degToRad(p.Y) // Y is latitude
			lonRad := degToRad(p.X) // X is longitude
			x, y, z := latLonTo3D(latRad, lonRad, radius)
			sx, sy, visible := g.project3Dto2D(x, y, z, radius)

			if visible {
				points = append(points, Point{sx, sy})
			} else {
				allVisible = false
			}
		}

		// If we have enough visible points, fill the polygon
		if len(points) >= 3 {
			g.canvas.FillPolygon(points, landColor)
		}

		// Draw outline for visible segments
		if len(points) > 1 {
			// Close the polygon for outline
			if allVisible && len(points) >= 3 {
				points = append(points, points[0])
			}
			g.drawPolyline(points, 1.5, outlineColor)
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

// calculateNodePositions assigns geographic positions to nodes based on country or Fibonacci distribution
func (g *GlobeRenderer) calculateNodePositions() {
	// Clear previous positions
	g.nodeGeoPos = make(map[string]GeoPosition)
	g.nodePos3D = make(map[string]Vec3)

	// Get all nodes as a slice for Fibonacci distribution
	allNodes := make([]*Node, 0, len(g.graph.Nodes))
	for _, node := range g.graph.Nodes {
		allNodes = append(allNodes, node)
	}

	if g.voronoiMode && len(allNodes) >= 3 {
		// VORONOI MODE: Use Fibonacci sphere distribution for ALL nodes
		fibPoints := fibonacciSphere(len(allNodes), 1.02)

		for i, node := range allNodes {
			g.nodePos3D[node.ID] = fibPoints[i]
			// Also set geoPos for compatibility (convert 3D back to lat/lon)
			lat := math.Asin(fibPoints[i].Y / 1.02)
			lon := math.Atan2(fibPoints[i].X, fibPoints[i].Z)
			g.nodeGeoPos[node.ID] = GeoPosition{Lat: lat, Lon: lon}
		}
	} else {
		// GEOGRAPHIC MODE: Position by country
		countryCounts := make(map[string]int)
		satelliteIndex := 0

		for _, node := range g.graph.Nodes {
			country := node.Country

			// Check if node has valid country code
			coords, ok := countryCoords[country]
			if !ok || country == "" {
				// Position as orbiting satellite
				orbitRadius := 1.4 + float64(satelliteIndex%3)*0.1
				angle := float64(satelliteIndex) * 0.618 * 2 * math.Pi // Golden angle spread

				// Position in XZ plane (Y=0) - flat circle around globe
				x := math.Cos(angle) * orbitRadius
				z := math.Sin(angle) * orbitRadius
				g.nodePos3D[node.ID] = Vec3{x, 0, z}

				// Convert to geo position for compatibility (though not really geographic)
				g.nodeGeoPos[node.ID] = GeoPosition{Lat: 0, Lon: angle}

				satelliteIndex++
				continue
			}

			// Add jitter for multiple nodes in same country
			count := countryCounts[country]
			countryCounts[country]++

			jitterLat := float64(count%5-2) * 3
			jitterLon := float64(count/5-2) * 5

			lat := degToRad(coords.Lat + jitterLat)
			lon := degToRad(coords.Lon + jitterLon)
			g.nodeGeoPos[node.ID] = GeoPosition{Lat: lat, Lon: lon}

			// Also compute 3D position
			x, y, z := latLonTo3D(lat, lon, 1.02)
			g.nodePos3D[node.ID] = Vec3{x, y, z}
		}
	}
}

// drawEdges draws transport connections as geodesic arcs or interior chords
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

		if g.voronoiMode {
			// VORONOI MODE: Draw interior chord (straight line through sphere)
			from3D, ok1 := g.nodePos3D[edge.From]
			to3D, ok2 := g.nodePos3D[edge.To]
			if !ok1 || !ok2 {
				continue
			}
			g.drawInteriorChord(from3D, to3D, radius, color, edge.IsLocalEdge)
		} else {
			// GEOGRAPHIC MODE: Draw geodesic arc
			g.drawGeodesic(fromPos, toPos, radius, scale, color, edge.IsLocalEdge)
		}
	}
}

// drawInteriorChord draws a straight line (chord) between two 3D points
func (g *GlobeRenderer) drawInteriorChord(from, to Vec3, radius float64, color string, highlight bool) {
	// Project both endpoints
	sx1, sy1, v1 := g.project3Dto2D(from.X, from.Y, from.Z, radius)
	sx2, sy2, v2 := g.project3Dto2D(to.X, to.Y, to.Z, radius)

	// For interior chord, we draw even if one or both points are on back side
	// but with reduced opacity
	lineWidth := 1.0
	if highlight {
		lineWidth = 2.0
	}

	// If both visible, draw solid
	if v1 && v2 {
		g.canvas.Line(sx1, sy1, sx2, sy2, lineWidth, color)
	} else if v1 || v2 {
		// One point visible - draw with reduced opacity
		// Re-project to get the screen coords anyway
		rx1, ry1, _ := rotate3D(from.X, from.Y, from.Z, g.rotation.X, g.rotation.Y)
		rx2, ry2, _ := rotate3D(to.X, to.Y, to.Z, g.rotation.X, g.rotation.Y)

		centerX := g.canvas.Width() / 2
		centerY := g.canvas.Height() / 2
		scale := math.Min(g.canvas.Width(), g.canvas.Height()) * 0.4

		sx1 = centerX + rx1*scale
		sy1 = centerY - ry1*scale
		sx2 = centerX + rx2*scale
		sy2 = centerY - ry2*scale

		// Draw with lower opacity (simulated by using a darker color variant)
		// Since we can't easily do opacity in simple canvas, just use dashed or lighter appearance
		g.canvas.Line(sx1, sy1, sx2, sy2, lineWidth*0.5, color)
	}
	// If neither visible, skip (chord is entirely on back side)
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
	centerX := g.canvas.Width() / 2
	centerY := g.canvas.Height() / 2

	for _, node := range g.graph.Nodes {
		// Check status filters
		if !g.opts.ShowNodeStatus(node.Status) {
			continue
		}

		var sx, sy float64
		var visible bool
		isSatellite := false

		// Check if this is a satellite (no valid country)
		_, hasCountry := countryCoords[node.Country]
		if !hasCountry || node.Country == "" {
			isSatellite = true
		}

		if g.voronoiMode {
			// Use 3D position directly in Voronoi mode
			pos3D, ok := g.nodePos3D[node.ID]
			if !ok {
				continue
			}
			sx, sy, visible = g.project3Dto2D(pos3D.X, pos3D.Y, pos3D.Z, radius)
		} else if isSatellite {
			// Satellite: render in fixed ring (no rotation applied)
			pos3D, ok := g.nodePos3D[node.ID]
			if !ok {
				continue
			}
			// Project directly to screen without rotation (XZ plane, Y=0)
			sx = centerX + pos3D.X*scale
			sy = centerY - pos3D.Z*scale // Z becomes Y on screen
			visible = true               // Satellites are always visible
		} else {
			// Use geo position in geographic mode
			pos, ok := g.nodeGeoPos[node.ID]
			if !ok {
				continue
			}
			x, y, z := latLonTo3D(pos.Lat, pos.Lon, radius*1.02)
			sx, sy, visible = g.project3Dto2D(x, y, z, radius)
		}

		if !visible {
			continue
		}

		// In Voronoi mode, draw country-colored halo first
		if g.voronoiMode && node.Country != "" {
			countryColor, ok := countryColors[node.Country]
			if ok {
				// Draw larger semi-transparent halo
				g.canvas.FillCircle(sx, sy, 12, countryColor+"40") // 40 = ~25% opacity in hex
			}
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
