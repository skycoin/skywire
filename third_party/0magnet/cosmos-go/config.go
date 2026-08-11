//go:build js && wasm

package cosmos

import "syscall/js"

// PointShape enumerates the built-in point shapes (matching the
// PointShape enum of cosmos.gl).
type PointShape = float32

const (
	ShapeCircle   PointShape = 0
	ShapeSquare   PointShape = 1
	ShapeTriangle PointShape = 2
	ShapeDiamond  PointShape = 3
	ShapePentagon PointShape = 4
	ShapeHexagon  PointShape = 5
	ShapeStar     PointShape = 6
	ShapeCross    PointShape = 7
	ShapeNone     PointShape = 8
)

// Config mirrors GraphConfigInterface of cosmos.gl 2.6.3.
// Use NewConfig to get one with default values. Colors are CSS color
// strings ("" keeps the default). Fields whose zero value is meaningful in
// the original (opacities that may be "unset") use the dedicated sentinel
// noted in their comment.
type Config struct {
	// If false, the simulation will not run. Applied only on initialization.
	// Default: true
	EnableSimulation bool
	// Canvas background color. Default: "#222222"
	BackgroundColor string
	// Simulation space size (max 8192). Default: 8192
	SpaceSize int
	// Default color for points without explicit colors. Default: "#b3b3b3"
	PointDefaultColor string
	// Color for greyed out points ("" = derive from the point color by
	// darkening/lightening based on the background).
	PointGreyoutColor string
	// Opacity for greyed out points; negative = unset (use color as is).
	// Default: -1 (unset)
	PointGreyoutOpacity float64
	// Default size for points without explicit sizes. Default: 4
	PointDefaultSize float64
	// Universal opacity multiplier for all points. Default: 1
	PointOpacity float64
	// Scale factor for point sizes. Default: 1
	PointSizeScale float64
	// Cursor style when hovering over a point. Default: "auto"
	HoveredPointCursor string
	// Cursor style when hovering over a link. Default: "auto"
	HoveredLinkCursor string
	// Turns ring rendering around a hovered point on / off. Default: false
	RenderHoveredPointRing bool
	// Hovered point ring color. Default: "white"
	HoveredPointRingColor string
	// Focused point ring color. Default: "white"
	FocusedPointRingColor string
	// Focused point index; negative = none. Default: -1
	FocusedPointIndex int
	// Turns link rendering on / off. Default: true
	RenderLinks bool
	// Default color for links without explicit colors. Default: "#666666"
	LinkDefaultColor string
	// Universal opacity multiplier for all links. Default: 1
	LinkOpacity float64
	// Greyed out link opacity when a selection is active. Default: 0.1
	LinkGreyoutOpacity float64
	// Default width for links without explicit widths. Default: 1
	LinkDefaultWidth float64
	// Color for hovered links ("" = keep the link color).
	HoveredLinkColor string
	// Extra width in pixels for the hovered link. Default: 5
	HoveredLinkWidthIncrease float64
	// Scale factor for link widths. Default: 1
	LinkWidthScale float64
	// Scale links when zooming in or out. Default: false
	ScaleLinksOnZoom bool
	// Render links as curved lines. Default: false
	CurvedLinks bool
	// Number of segments in a curved line. Default: 19
	CurvedLinkSegments int
	// Weight affects the shape of the curve. Default: 0.8
	CurvedLinkWeight float64
	// Position of the curve control point on the normal from the line
	// center. Default: 0.5
	CurvedLinkControlPointDistance float64
	// Display link arrows by default. Default: false
	LinkDefaultArrows bool
	// Scale factor for link arrow size. Default: 1
	LinkArrowsSizeScale float64
	// Minimum and maximum link visibility distance in pixels.
	// Default: [50, 150]
	LinkVisibilityDistanceRange [2]float64
	// Transparency of a link at the maximum visibility distance.
	// Default: 0.25
	LinkVisibilityMinTransparency float64
	// Use the classic quadtree algorithm for the Many-Body force. Applied
	// only on initialization. Default: false
	UseClassicQuadtree bool

	// Decay coefficient (smaller = slower cool down). Default: 5000
	SimulationDecay float64
	// Gravity force coefficient. Default: 0.25
	SimulationGravity float64
	// Centering to center mass force coefficient. Default: 0
	SimulationCenter float64
	// Repulsion force coefficient. Default: 1.0
	SimulationRepulsion float64
	// Many-Body force detalization / Barnes–Hut criterion. Default: 1.15
	SimulationRepulsionTheta float64
	// Barnes–Hut approximation depth (with UseClassicQuadtree). Default: 12
	SimulationRepulsionQuadtreeLevels int
	// Link spring force coefficient. Default: 1
	SimulationLinkSpring float64
	// Minimum link distance. Default: 10
	SimulationLinkDistance float64
	// Range of random link distance values. Default: [1, 1.2]
	SimulationLinkDistRandomVariationRange [2]float64
	// Repulsion from the mouse position (right mouse button). Default: 2
	SimulationRepulsionFromMouse float64
	// Enable repulsion from mouse on right-click. Default: false
	EnableRightClickRepulsion bool
	// Friction coefficient, 0 (high friction) to 1 (no friction).
	// Default: 0.85
	SimulationFriction float64
	// Cluster force coefficient. Default: 0.1
	SimulationCluster float64

	OnSimulationStart   func()
	OnSimulationTick    func(alpha float64, hoveredIndex int, position [2]float64)
	OnSimulationEnd     func()
	OnSimulationPause   func()
	OnSimulationUnpause func()

	// index arguments are -1 when no point/link is involved
	OnClick           func(index int, position [2]float64, event js.Value)
	OnPointClick      func(index int, position [2]float64, event js.Value)
	OnLinkClick       func(linkIndex int, event js.Value)
	OnBackgroundClick func(event js.Value)
	OnMouseMove       func(index int, position [2]float64, event js.Value)
	OnPointMouseOver  func(index int, position [2]float64, event js.Value)
	OnPointMouseOut   func(event js.Value)
	OnLinkMouseOver   func(linkIndex int)
	OnLinkMouseOut    func(event js.Value)
	OnZoomStart       func(userDriven bool)
	OnZoom            func(userDriven bool)
	OnZoomEnd         func(userDriven bool)
	OnDragStart       func(event js.Value)
	OnDrag            func(event js.Value)
	OnDragEnd         func(event js.Value)

	// Show an FPS counter. Default: false
	ShowFPSMonitor bool
	// Canvas pixel ratio. Default: 2
	PixelRatio float64
	// Scale points when zooming in or out. Default: false
	ScalePointsOnZoom bool
	// Initial zoom level (0 = unset). If set, FitViewOnInit is ignored.
	InitialZoomLevel float64
	// Enable zooming in and out. Default: true
	EnableZoom bool
	// Keep the simulation running during zoom operations. Default: false
	EnableSimulationDuringZoom bool
	// Enable dragging of points. Default: false
	EnableDrag bool
	// Center and zoom the view to fit all points on first render.
	// Default: true
	FitViewOnInit bool
	// Delay in milliseconds before fitting the view. Default: 250
	FitViewDelay float64
	// Padding when fitting the view. Default: 0.1
	FitViewPadding float64
	// Duration in milliseconds of the fit view animation. Default: 250
	FitViewDuration float64
	// Fit the view to a rect [[left, bottom], [right, top]] in space
	// coordinates (nil = all points).
	FitViewByPointsInRect [][2]float64
	// Fit the view to these point indices (takes precedence over
	// FitViewByPointsInRect).
	FitViewByPointIndices []int
	// Random seed for layout reproducibility ("" = non-deterministic).
	// Applied only on initialization.
	RandomSeed string
	// Point sampling distance in pixels for GetSampledPoints. Default: 150
	PointSamplingDistance float64
	// Controls automatic rescaling of point positions into the visible
	// space: 0 = auto (rescale only when the simulation is disabled),
	// 1 = always rescale, 2 = never rescale. Default: RescaleAuto
	RescalePositions RescaleMode
	// HTML shown in the bottom right corner ("" = nothing).
	Attribution string
}

// RescaleMode controls Config.RescalePositions.
type RescaleMode uint8

const (
	RescaleAuto RescaleMode = iota
	RescaleAlways
	RescaleNever
)

// NewConfig returns a Config with the same defaults as cosmos.gl 2.6.3.
func NewConfig() *Config {
	return &Config{
		EnableSimulation:               true,
		BackgroundColor:                "#222222",
		SpaceSize:                      8192,
		PointDefaultColor:              "#b3b3b3",
		PointGreyoutColor:              "",
		PointGreyoutOpacity:            -1,
		PointDefaultSize:               4,
		PointOpacity:                   1.0,
		PointSizeScale:                 1,
		HoveredPointCursor:             "auto",
		HoveredLinkCursor:              "auto",
		RenderHoveredPointRing:         false,
		HoveredPointRingColor:          "white",
		FocusedPointRingColor:          "white",
		FocusedPointIndex:              -1,
		RenderLinks:                    true,
		LinkDefaultColor:               "#666666",
		LinkOpacity:                    1.0,
		LinkGreyoutOpacity:             0.1,
		LinkDefaultWidth:               1,
		HoveredLinkColor:               "",
		HoveredLinkWidthIncrease:       5,
		LinkWidthScale:                 1,
		ScaleLinksOnZoom:               false,
		CurvedLinks:                    false,
		CurvedLinkSegments:             19,
		CurvedLinkWeight:               0.8,
		CurvedLinkControlPointDistance: 0.5,
		LinkDefaultArrows:              false,
		LinkArrowsSizeScale:            1,
		LinkVisibilityDistanceRange:    [2]float64{50, 150},
		LinkVisibilityMinTransparency:  0.25,
		UseClassicQuadtree:             false,

		SimulationDecay:                        5000,
		SimulationGravity:                      0.25,
		SimulationCenter:                       0,
		SimulationRepulsion:                    1.0,
		SimulationRepulsionTheta:               1.15,
		SimulationRepulsionQuadtreeLevels:      12,
		SimulationLinkSpring:                   1,
		SimulationLinkDistance:                 10,
		SimulationLinkDistRandomVariationRange: [2]float64{1, 1.2},
		SimulationRepulsionFromMouse:           2,
		EnableRightClickRepulsion:              false,
		SimulationFriction:                     0.85,
		SimulationCluster:                      0.1,

		ShowFPSMonitor:             false,
		PixelRatio:                 2,
		ScalePointsOnZoom:          false,
		EnableZoom:                 true,
		EnableSimulationDuringZoom: false,
		EnableDrag:                 false,
		FitViewOnInit:              true,
		FitViewDelay:               250,
		FitViewPadding:             0.1,
		FitViewDuration:            250,
		PointSamplingDistance:      150,
	}
}

const (
	hoveredPointRingOpacity = 0.7
	focusedPointRingOpacity = 0.95
	defaultScaleToZoom      = 3.0
)
