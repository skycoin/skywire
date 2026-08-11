//go:build js && wasm

package cosmos

import "math"

// clusters is the port of the Clusters module (cluster force).
type clusters struct {
	ctx    *glCtx
	cfg    *Config
	st     *store
	data   *graphData
	points *points

	centermassFbo *framebuffer
	clusterCount  int

	clusterFbo                 *framebuffer
	clusterPositionsFbo        *framebuffer
	clusterForceCoefficientFbo *framebuffer
	clearCentermassCommand     *command
	calculateCentermassCommand *command
	applyForcesCommand         *command
	clustersTextureSize        int
	created                    bool
}

func newClusters(ctx *glCtx, cfg *Config, st *store, data *graphData, pts *points) *clusters {
	return &clusters{ctx: ctx, cfg: cfg, st: st, data: data, points: pts}
}

func (c *clusters) create() {
	st, data := c.st, c.data
	pointsTextureSize := st.pointsTextureSize
	n := data.pointsNumber()
	if n == 0 || (data.pointClusters == nil && data.clusterPositions == nil) {
		c.created = false
		return
	}

	// highest cluster index + 1 (cluster indices start at 0)
	maxCluster := 0
	for _, clusterIndex := range data.pointClusters {
		if clusterIndex > maxCluster {
			maxCluster = clusterIndex
		}
	}
	c.clusterCount = maxCluster + 1
	c.clustersTextureSize = int(math.Ceil(math.Sqrt(float64(c.clusterCount))))

	clusterState := make([]float32, pointsTextureSize*pointsTextureSize*4)
	clusterPositions := make([]float32, c.clustersTextureSize*c.clustersTextureSize*4)
	for i := range clusterPositions {
		clusterPositions[i] = -1
	}
	clusterForceCoefficient := make([]float32, pointsTextureSize*pointsTextureSize*4)
	for i := range clusterForceCoefficient {
		clusterForceCoefficient[i] = 1
	}
	if data.clusterPositions != nil {
		for cluster := 0; cluster < c.clusterCount; cluster++ {
			if cluster*2+1 < len(data.clusterPositions) {
				clusterPositions[cluster*4+0] = data.clusterPositions[cluster*2+0]
				clusterPositions[cluster*4+1] = data.clusterPositions[cluster*2+1]
			}
		}
	}

	for i := 0; i < n; i++ {
		clusterIndex := -1
		if data.pointClusters != nil && i < len(data.pointClusters) {
			clusterIndex = data.pointClusters[i]
		}
		if clusterIndex < 0 {
			// no cluster, so no forces
			clusterState[i*4+0] = -1
			clusterState[i*4+1] = -1
		} else {
			clusterState[i*4+0] = float32(clusterIndex % c.clustersTextureSize)
			clusterState[i*4+1] = float32(clusterIndex / c.clustersTextureSize)
		}
		if data.clusterStrength != nil && i < len(data.clusterStrength) {
			clusterForceCoefficient[i*4+0] = data.clusterStrength[i]
		}
	}

	c.clusterFbo.destroy()
	c.clusterFbo = c.ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, clusterState)
	c.clusterPositionsFbo.destroy()
	c.clusterPositionsFbo = c.ctx.newFramebuffer(c.clustersTextureSize, c.clustersTextureSize, clusterPositions)
	c.clusterForceCoefficientFbo.destroy()
	c.clusterForceCoefficientFbo = c.ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, clusterForceCoefficient)
	c.centermassFbo.destroy()
	c.centermassFbo = c.ctx.newFramebuffer(c.clustersTextureSize, c.clustersTextureSize, nil)
	c.created = true
}

func (c *clusters) initPrograms(quadAttr, indexAttr []attrBinding) error {
	ctx, st, data := c.ctx, c.st, c.data
	if data.pointClusters == nil && data.clusterPositions == nil {
		return nil
	}

	if c.clearCentermassCommand == nil {
		prog, err := ctx.program(quadVert, clearFrag)
		if err != nil {
			return err
		}
		c.clearCentermassCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return c.centermassFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
	}
	if c.calculateCentermassCommand == nil {
		prog, err := ctx.program(clusterCentermassVert, clusterCentermassFrag)
		if err != nil {
			return err
		}
		c.calculateCentermassCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return c.centermassFbo },
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs:     indexAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":    func() uniformValue { return c.points.previousPositionFbo },
				"pointsTextureSize":   func() uniformValue { return float64(st.pointsTextureSize) },
				"clusterTexture":      func() uniformValue { return c.clusterFbo },
				"clustersTextureSize": func() uniformValue { return float64(c.clustersTextureSize) },
			},
			blend: "add", depthOff: true,
		}
	}
	if c.applyForcesCommand == nil {
		prog, err := ctx.program(quadVert, forceClusterFrag)
		if err != nil {
			return err
		}
		c.applyForcesCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return c.points.velocityFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":        func() uniformValue { return c.points.previousPositionFbo },
				"clusterTexture":          func() uniformValue { return c.clusterFbo },
				"centermassTexture":       func() uniformValue { return c.centermassFbo },
				"clusterPositionsTexture": func() uniformValue { return c.clusterPositionsFbo },
				"clusterForceCoefficient": func() uniformValue { return c.clusterForceCoefficientFbo },
				"alpha":                   func() uniformValue { return st.alpha },
				"clustersTextureSize":     func() uniformValue { return float64(c.clustersTextureSize) },
				"clusterCoefficient":      func() uniformValue { return c.cfg.SimulationCluster },
			},
		}
	}
	return nil
}

func (c *clusters) calculateCentermass() {
	if !c.created || c.clearCentermassCommand == nil {
		return
	}
	c.clearCentermassCommand.run()
	c.calculateCentermassCommand.run()
}

func (c *clusters) run() {
	if !c.created || c.applyForcesCommand == nil {
		return
	}
	if c.data.pointClusters == nil && c.data.clusterPositions == nil {
		return
	}
	c.calculateCentermass()
	c.applyForcesCommand.run()
}

func (c *clusters) destroy() {
	c.centermassFbo.destroy()
	c.clusterFbo.destroy()
	c.clusterPositionsFbo.destroy()
	c.clusterForceCoefficientFbo.destroy()
}
