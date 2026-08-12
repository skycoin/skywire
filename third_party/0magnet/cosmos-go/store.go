//go:build js && wasm

package cosmos

import "math"

const (
	alphaMin         = 0.001
	maxPointSizeCore = 64.0
	// maxHoverDetectionDelay is the number of animation frames to skip
	// between hover detection runs.
	maxHoverDetectionDelay = 4
)

type hoveredPoint struct {
	index    int
	position [2]float64
}

// store is the port of the Store module: shared render/simulation state.
type store struct {
	pointsTextureSize   int
	linksTextureSize    int
	alpha               float64
	transform           mat3
	screenSize          [2]float64
	mousePosition       [2]float64
	screenMousePosition [2]float64
	selectedArea        [2][2]float64
	isSimulationRunning bool
	simulationProgress  float64
	selectedIndices     []int // nil = no selection
	hasSelection        bool
	maxPointSize        float64
	hoveredPoint        *hoveredPoint
	focusedPointIndex   int // -1 = none
	draggingPointIndex  int // -1 = none
	hoveredLinkIndex    int // -1 = none
	adjustedSpaceSize   float64
	isSpaceKeyPressed   bool
	webglMaxTextureSize int

	hoveredPointRingColor [4]float64
	focusedPointRingColor [4]float64
	hoveredLinkColor      [4]float64 // -1 components = not set
	greyoutPointColor     [4]float64 // -1 components = not set
	isDarkenGreyout       bool
	isLinkHoveringEnabled bool
	backgroundColor       [4]float64
	alphaTarget           float64
	scalePointX           linearScale
	scalePointY           linearScale
	random                *rng
}

func newStore() *store {
	return &store{
		alpha:                 1,
		transform:             mat3Identity(),
		maxPointSize:          maxPointSizeCore,
		adjustedSpaceSize:     8192,
		focusedPointIndex:     -1,
		draggingPointIndex:    -1,
		hoveredLinkIndex:      -1,
		webglMaxTextureSize:   16384,
		hoveredPointRingColor: [4]float64{1, 1, 1, hoveredPointRingOpacity},
		focusedPointRingColor: [4]float64{1, 1, 1, focusedPointRingOpacity},
		hoveredLinkColor:      [4]float64{-1, -1, -1, -1},
		greyoutPointColor:     [4]float64{-1, -1, -1, -1},
		random:                newRng(0),
	}
}

func (s *store) setBackgroundColor(color [4]float64) {
	s.backgroundColor = color
	brightness := 0.2126*color[0] + 0.7152*color[1] + 0.0722*color[2]
	s.isDarkenGreyout = brightness < 0.65
}

func (s *store) addRandomSeed(seed string) {
	s.random = newRng(seedFromString(seed))
}

func (s *store) getRandomFloat(min, max float64) float64 {
	return s.random.float(min, max)
}

// adjustSpaceSize reduces the space size if it exceeds the WebGL limits,
// without changing the config parameter.
func (s *store) adjustSpaceSize(configSpaceSize int, webglMaxTextureSize int) {
	if configSpaceSize >= webglMaxTextureSize {
		s.adjustedSpaceSize = float64(webglMaxTextureSize) / 2
		consoleWarn("The `SpaceSize` has been reduced due to WebGL limits")
	} else {
		s.adjustedSpaceSize = float64(configSpaceSize)
	}
}

func (s *store) updateScreenSize(width, height float64) {
	space := s.adjustedSpaceSize
	s.screenSize = [2]float64{width, height}
	s.scalePointX = linearScale{d0: 0, d1: space, r0: (width - space) / 2, r1: (width + space) / 2}
	s.scalePointY = linearScale{d0: space, d1: 0, r0: (height - space) / 2, r1: (height + space) / 2}
}

func (s *store) scaleX(x float64) float64 { return s.scalePointX.scale(x) }
func (s *store) scaleY(y float64) float64 { return s.scalePointY.scale(y) }

func (s *store) setHoveredPointRingColor(color string) {
	c := parseRGBA(color)
	s.hoveredPointRingColor[0] = c[0]
	s.hoveredPointRingColor[1] = c[1]
	s.hoveredPointRingColor[2] = c[2]
}

func (s *store) setFocusedPointRingColor(color string) {
	c := parseRGBA(color)
	s.focusedPointRingColor[0] = c[0]
	s.focusedPointRingColor[1] = c[1]
	s.focusedPointRingColor[2] = c[2]
}

func (s *store) setGreyoutPointColor(color string) {
	if color == "" {
		s.greyoutPointColor = [4]float64{-1, -1, -1, -1}
		return
	}
	s.greyoutPointColor = parseRGBA(color)
}

func (s *store) setHoveredLinkColor(color string) {
	if color == "" {
		s.hoveredLinkColor = [4]float64{-1, -1, -1, -1}
		return
	}
	s.hoveredLinkColor = parseRGBA(color)
}

func (s *store) updateLinkHoveringEnabled(cfg *Config) {
	s.isLinkHoveringEnabled = cfg.OnLinkClick != nil || cfg.OnLinkMouseOver != nil || cfg.OnLinkMouseOut != nil
	if !s.isLinkHoveringEnabled {
		s.hoveredLinkIndex = -1
	}
}

func (s *store) addAlpha(decay float64) float64 {
	return (s.alphaTarget - s.alpha) * s.alphaDecay(decay)
}

func (s *store) alphaDecay(decay float64) float64 {
	return 1 - math.Pow(alphaMin, 1/decay)
}
