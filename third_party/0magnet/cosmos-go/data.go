//go:build js && wasm

package cosmos

import "math"

// adjacencyPair is one entry of the adjacency lists: the connected point
// index and the index of the link in the links array.
type adjacencyPair struct {
	pointIndex int
	linkIndex  int
}

// graphData is the port of the GraphData module: flat typed-array data
// with defaults applied.
type graphData struct {
	cfg *Config

	inputPointPositions    []float32 // [x1, y1, x2, y2, ...]
	inputPointColors       []float32 // [r, g, b, a, ...] with components 0..255 (alpha 0..1)
	inputPointSizes        []float32
	inputPointShapes       []float32
	inputPointImageIndices []float32
	inputPointImageSizes   []float32
	inputLinkColors        []float32
	inputLinkWidths        []float32
	inputLinkStrength      []float32
	inputPointClusters     []int     // -1 = no cluster
	inputClusterPositions  []float32 // [x1, y1, ...], negative = centermass
	inputClusterStrength   []float32
	inputPinnedPoints      []int

	pointPositions    []float32
	pointColors       []float32
	pointSizes        []float32
	pointShapes       []float32
	pointImageIndices []float32
	pointImageSizes   []float32

	inputLinks     []float32 // [source1, target1, source2, target2, ...]
	links          []float32
	linkColors     []float32
	linkWidths     []float32
	linkArrowsBool []bool
	linkArrows     []float32
	linkStrength   []float32

	pointClusters    []int
	clusterPositions []float32
	clusterStrength  []float32

	sourceIndexToTargetIndices [][]adjacencyPair
	targetIndexToSourceIndices [][]adjacencyPair

	degree    []int
	inDegree  []int
	outDegree []int
}

func newGraphData(cfg *Config) *graphData {
	return &graphData{cfg: cfg}
}

func (g *graphData) pointsNumber() int { return len(g.pointPositions) / 2 }
func (g *graphData) linksNumber() int  { return len(g.links) / 2 }

func (g *graphData) updatePoints() {
	g.pointPositions = g.inputPointPositions
}

// updatePointColor applies input colors or the config default. Input colors
// use 0..255 RGB components with 0..1 alpha (like getRgbaColor); the stored
// colors are normalized to 0..1.
func (g *graphData) updatePointColor() {
	n := g.pointsNumber()
	if n == 0 {
		g.pointColors = nil
		return
	}
	defaultRgba := parseRGBA(g.cfg.PointDefaultColor)
	if g.inputPointColors == nil || len(g.inputPointColors)/4 != n {
		g.pointColors = make([]float32, n*4)
		for i := 0; i < n; i++ {
			g.pointColors[i*4+0] = float32(defaultRgba[0])
			g.pointColors[i*4+1] = float32(defaultRgba[1])
			g.pointColors[i*4+2] = float32(defaultRgba[2])
			g.pointColors[i*4+3] = float32(defaultRgba[3])
		}
	} else {
		g.pointColors = g.inputPointColors
		for i := 0; i < n*4; i++ {
			if math.IsNaN(float64(g.pointColors[i])) {
				g.pointColors[i] = float32(defaultRgba[i%4])
			}
		}
	}
}

func (g *graphData) updatePointSize() {
	n := g.pointsNumber()
	if n == 0 {
		g.pointSizes = nil
		return
	}
	defaultSize := float32(g.cfg.PointDefaultSize)
	if g.inputPointSizes == nil || len(g.inputPointSizes) != n {
		g.pointSizes = make([]float32, n)
		for i := range g.pointSizes {
			g.pointSizes[i] = defaultSize
		}
	} else {
		g.pointSizes = g.inputPointSizes
		for i := range g.pointSizes {
			if math.IsNaN(float64(g.pointSizes[i])) {
				g.pointSizes[i] = defaultSize
			}
		}
	}
}

func (g *graphData) updatePointShape() {
	n := g.pointsNumber()
	if n == 0 {
		g.pointShapes = nil
		return
	}
	if g.inputPointShapes == nil || len(g.inputPointShapes) != n {
		g.pointShapes = make([]float32, n) // ShapeCircle = 0
	} else {
		g.pointShapes = make([]float32, n)
		copy(g.pointShapes, g.inputPointShapes)
		for i := range g.pointShapes {
			s := g.pointShapes[i]
			if math.IsNaN(float64(s)) || s < 0 || s > 8 {
				g.pointShapes[i] = float32(ShapeCircle)
			}
		}
	}
}

func (g *graphData) updatePointImageIndices() {
	n := g.pointsNumber()
	if n == 0 {
		g.pointImageIndices = nil
		return
	}
	if g.inputPointImageIndices == nil || len(g.inputPointImageIndices) != n {
		g.pointImageIndices = make([]float32, n)
		for i := range g.pointImageIndices {
			g.pointImageIndices[i] = -1
		}
	} else {
		g.pointImageIndices = make([]float32, n)
		copy(g.pointImageIndices, g.inputPointImageIndices)
		for i := range g.pointImageIndices {
			v := float64(g.pointImageIndices[i])
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				g.pointImageIndices[i] = -1
			} else {
				g.pointImageIndices[i] = float32(math.Trunc(v))
			}
		}
	}
}

func (g *graphData) updatePointImageSizes() {
	n := g.pointsNumber()
	if n == 0 {
		g.pointImageSizes = nil
		return
	}
	defaultSize := float32(g.cfg.PointDefaultSize)
	if g.inputPointImageSizes == nil || len(g.inputPointImageSizes) != n {
		if g.pointSizes != nil {
			g.pointImageSizes = make([]float32, n)
			copy(g.pointImageSizes, g.pointSizes)
		} else {
			g.pointImageSizes = make([]float32, n)
			for i := range g.pointImageSizes {
				g.pointImageSizes[i] = defaultSize
			}
		}
	} else {
		g.pointImageSizes = make([]float32, n)
		copy(g.pointImageSizes, g.inputPointImageSizes)
		for i := range g.pointImageSizes {
			if math.IsNaN(float64(g.pointImageSizes[i])) {
				if g.pointSizes != nil && i < len(g.pointSizes) {
					g.pointImageSizes[i] = g.pointSizes[i]
				} else {
					g.pointImageSizes[i] = defaultSize
				}
			}
		}
	}
}

func (g *graphData) updateLinks() {
	g.links = g.inputLinks
}

func (g *graphData) updateLinkColor() {
	n := g.linksNumber()
	if n == 0 {
		g.linkColors = nil
		return
	}
	defaultRgba := parseRGBA(g.cfg.LinkDefaultColor)
	if g.inputLinkColors == nil || len(g.inputLinkColors)/4 != n {
		g.linkColors = make([]float32, n*4)
		for i := 0; i < n; i++ {
			g.linkColors[i*4+0] = float32(defaultRgba[0])
			g.linkColors[i*4+1] = float32(defaultRgba[1])
			g.linkColors[i*4+2] = float32(defaultRgba[2])
			g.linkColors[i*4+3] = float32(defaultRgba[3])
		}
	} else {
		g.linkColors = g.inputLinkColors
		for i := 0; i < n*4; i++ {
			if math.IsNaN(float64(g.linkColors[i])) {
				g.linkColors[i] = float32(defaultRgba[i%4])
			}
		}
	}
}

func (g *graphData) updateLinkWidth() {
	n := g.linksNumber()
	if n == 0 {
		g.linkWidths = nil
		return
	}
	defaultWidth := float32(g.cfg.LinkDefaultWidth)
	if g.inputLinkWidths == nil || len(g.inputLinkWidths) != n {
		g.linkWidths = make([]float32, n)
		for i := range g.linkWidths {
			g.linkWidths[i] = defaultWidth
		}
	} else {
		g.linkWidths = g.inputLinkWidths
		for i := range g.linkWidths {
			if math.IsNaN(float64(g.linkWidths[i])) {
				g.linkWidths[i] = defaultWidth
			}
		}
	}
}

func (g *graphData) updateArrows() {
	n := g.linksNumber()
	if n == 0 {
		g.linkArrows = nil
		return
	}
	defaultArrow := float32(0)
	if g.cfg.LinkDefaultArrows {
		defaultArrow = 1
	}
	if g.linkArrowsBool == nil || len(g.linkArrowsBool) != n {
		g.linkArrows = make([]float32, n)
		for i := range g.linkArrows {
			g.linkArrows[i] = defaultArrow
		}
	} else {
		g.linkArrows = make([]float32, n)
		for i, b := range g.linkArrowsBool {
			if b {
				g.linkArrows[i] = 1
			}
		}
	}
}

func (g *graphData) updateLinkStrength() {
	n := g.linksNumber()
	if g.inputLinkStrength == nil || len(g.inputLinkStrength) != n {
		g.linkStrength = nil
	} else {
		g.linkStrength = g.inputLinkStrength
	}
}

func (g *graphData) updateClusters() {
	n := g.pointsNumber()
	if n == 0 {
		g.pointClusters = nil
		g.clusterPositions = nil
		return
	}
	if g.inputPointClusters == nil || len(g.inputPointClusters) != n {
		g.pointClusters = nil
	} else {
		g.pointClusters = g.inputPointClusters
	}
	g.clusterPositions = g.inputClusterPositions
	if g.inputClusterStrength == nil || len(g.inputClusterStrength) != n {
		g.clusterStrength = nil
	} else {
		g.clusterStrength = g.inputClusterStrength
	}
}

func (g *graphData) update() {
	g.updatePoints()
	g.updatePointColor()
	g.updatePointSize()
	g.updatePointShape()
	g.updatePointImageIndices()
	g.updatePointImageSizes()

	g.updateLinks()
	g.updateLinkColor()
	g.updateLinkWidth()
	g.updateArrows()
	g.updateLinkStrength()

	g.updateClusters()

	g.createAdjacencyLists()
	g.calculateDegrees()
}

func (g *graphData) getAdjacentIndices(index int) []int {
	var result []int
	if index >= 0 && index < len(g.sourceIndexToTargetIndices) {
		for _, p := range g.sourceIndexToTargetIndices[index] {
			result = append(result, p.pointIndex)
		}
	}
	if index >= 0 && index < len(g.targetIndexToSourceIndices) {
		for _, p := range g.targetIndexToSourceIndices[index] {
			result = append(result, p.pointIndex)
		}
	}
	return result
}

func (g *graphData) createAdjacencyLists() {
	if g.links == nil {
		g.sourceIndexToTargetIndices = nil
		g.targetIndexToSourceIndices = nil
		return
	}
	n := g.pointsNumber()
	g.sourceIndexToTargetIndices = make([][]adjacencyPair, n)
	g.targetIndexToSourceIndices = make([][]adjacencyPair, n)
	for i := 0; i < g.linksNumber(); i++ {
		sourceIndex := int(g.links[i*2])
		targetIndex := int(g.links[i*2+1])
		if sourceIndex >= 0 && sourceIndex < n && targetIndex >= 0 && targetIndex < n {
			g.sourceIndexToTargetIndices[sourceIndex] = append(g.sourceIndexToTargetIndices[sourceIndex], adjacencyPair{targetIndex, i})
			g.targetIndexToSourceIndices[targetIndex] = append(g.targetIndexToSourceIndices[targetIndex], adjacencyPair{sourceIndex, i})
		}
	}
}

func (g *graphData) calculateDegrees() {
	n := g.pointsNumber()
	g.degree = make([]int, n)
	g.inDegree = make([]int, n)
	g.outDegree = make([]int, n)
	for i := 0; i < n; i++ {
		g.inDegree[i] = len(g.targetIndexToSourceIndices[i])
		g.outDegree[i] = len(g.sourceIndexToTargetIndices[i])
		g.degree[i] = g.inDegree[i] + g.outDegree[i]
	}
}
