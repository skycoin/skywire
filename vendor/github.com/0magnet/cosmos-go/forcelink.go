//go:build js && wasm

package cosmos

import "math"

type linkDirection int

const (
	linkIncoming linkDirection = iota
	linkOutgoing
)

// forceLink is the port of the ForceLink module (spring force).
type forceLink struct {
	linkFirstIndicesAndAmountFbo *framebuffer
	indicesFbo                   *framebuffer
	biasAndStrengthFbo           *framebuffer
	randomDistanceFbo            *framebuffer
	maxPointDegree               int
	runCommand                   *command
	lastMaxLinks                 int
}

func newForceLink() *forceLink { return &forceLink{} }

func (f *forceLink) create(ctx *glCtx, st *store, data *graphData, direction linkDirection) {
	pointsTextureSize, linksTextureSize := st.pointsTextureSize, st.linksTextureSize
	if pointsTextureSize == 0 || linksTextureSize == 0 {
		return
	}
	linkFirstIndicesAndAmount := make([]float32, pointsTextureSize*pointsTextureSize*4)
	indices := make([]float32, linksTextureSize*linksTextureSize*4)
	linkBiasAndStrengthState := make([]float32, linksTextureSize*linksTextureSize*4)
	linkDistanceState := make([]float32, linksTextureSize*linksTextureSize*4)

	grouped := data.sourceIndexToTargetIndices
	if direction == linkOutgoing {
		grouped = data.targetIndexToSourceIndices
	}
	f.maxPointDegree = 0
	linkIndex := 0
	for pointIndex, connectedPointIndices := range grouped {
		if len(connectedPointIndices) == 0 {
			continue
		}
		linkFirstIndicesAndAmount[pointIndex*4+0] = float32(linkIndex % linksTextureSize)
		linkFirstIndicesAndAmount[pointIndex*4+1] = float32(linkIndex / linksTextureSize)
		linkFirstIndicesAndAmount[pointIndex*4+2] = float32(len(connectedPointIndices))

		for _, pair := range connectedPointIndices {
			connectedPointIndex := pair.pointIndex
			initialLinkIndex := pair.linkIndex
			indices[linkIndex*4+0] = float32(connectedPointIndex % pointsTextureSize)
			indices[linkIndex*4+1] = float32(connectedPointIndex / pointsTextureSize)
			degree := data.degree[connectedPointIndex]
			connectedDegree := data.degree[pointIndex]
			degreeSum := degree + connectedDegree
			bias := 0.5
			if degreeSum != 0 {
				bias = float64(degree) / float64(degreeSum)
			}
			minDegree := math.Min(float64(degree), float64(connectedDegree))
			var strength float64
			if data.linkStrength != nil && initialLinkIndex < len(data.linkStrength) {
				strength = float64(data.linkStrength[initialLinkIndex])
			} else {
				strength = 1 / math.Max(minDegree, 1)
			}
			strength = math.Sqrt(strength)
			linkBiasAndStrengthState[linkIndex*4+0] = float32(bias)
			linkBiasAndStrengthState[linkIndex*4+1] = float32(strength)
			linkDistanceState[linkIndex*4] = float32(st.getRandomFloat(0, 1))

			linkIndex++
		}

		if len(connectedPointIndices) > f.maxPointDegree {
			f.maxPointDegree = len(connectedPointIndices)
		}
	}

	f.linkFirstIndicesAndAmountFbo.destroy()
	f.linkFirstIndicesAndAmountFbo = ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, linkFirstIndicesAndAmount)
	f.indicesFbo.destroy()
	f.indicesFbo = ctx.newFramebuffer(linksTextureSize, linksTextureSize, indices)
	f.biasAndStrengthFbo.destroy()
	f.biasAndStrengthFbo = ctx.newFramebuffer(linksTextureSize, linksTextureSize, linkBiasAndStrengthState)
	f.randomDistanceFbo.destroy()
	f.randomDistanceFbo = ctx.newFramebuffer(linksTextureSize, linksTextureSize, linkDistanceState)
}

func (f *forceLink) initPrograms(ctx *glCtx, cfg *Config, st *store, pts *points, quadAttr []attrBinding) error {
	// the shader depends on maxPointDegree; recompile when it grows
	if f.runCommand != nil && f.lastMaxLinks == f.maxPointDegree {
		return nil
	}
	prog, err := ctx.program(quadVert, forceSpringFrag(f.maxPointDegree))
	if err != nil {
		return err
	}
	f.lastMaxLinks = f.maxPointDegree
	f.runCommand = &command{
		ctx: ctx, prog: prog,
		fbo:       func() *framebuffer { return pts.velocityFbo },
		primitive: "triangle strip",
		count:     func() int { return 4 },
		attrs:     quadAttr,
		uniforms: map[string]func() uniformValue{
			"positionsTexture": func() uniformValue { return pts.previousPositionFbo },
			"linkSpring":       func() uniformValue { return cfg.SimulationLinkSpring },
			"linkDistance":     func() uniformValue { return cfg.SimulationLinkDistance },
			"linkDistRandomVariationRange": func() uniformValue {
				return cfg.SimulationLinkDistRandomVariationRange[:]
			},
			"linkInfoTexture":           func() uniformValue { return f.linkFirstIndicesAndAmountFbo },
			"linkIndicesTexture":        func() uniformValue { return f.indicesFbo },
			"linkPropertiesTexture":     func() uniformValue { return f.biasAndStrengthFbo },
			"linkRandomDistanceTexture": func() uniformValue { return f.randomDistanceFbo },
			"pointsTextureSize":         func() uniformValue { return float64(st.pointsTextureSize) },
			"linksTextureSize":          func() uniformValue { return float64(st.linksTextureSize) },
			"alpha":                     func() uniformValue { return st.alpha },
		},
	}
	return nil
}

func (f *forceLink) run() { f.runCommand.run() }

func (f *forceLink) destroy() {
	f.linkFirstIndicesAndAmountFbo.destroy()
	f.indicesFbo.destroy()
	f.biasAndStrengthFbo.destroy()
	f.randomDistanceFbo.destroy()
	f.linkFirstIndicesAndAmountFbo = nil
	f.indicesFbo = nil
	f.biasAndStrengthFbo = nil
	f.randomDistanceFbo = nil
}
