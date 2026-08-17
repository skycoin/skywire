//go:build js && wasm

package cosmos

import "math"

// manyBody abstracts ForceManyBody / ForceManyBodyQuadtree.
type manyBody interface {
	create(ctx *glCtx, st *store)
	initPrograms(ctx *glCtx, cfg *Config, st *store, data *graphData, pts *points, quadAttr, indexAttr []attrBinding) error
	run()
	destroy()
}

func createRandomValuesFbo(ctx *glCtx, st *store) *framebuffer {
	// random numbers prevent points from sticking together in one coordinate
	n := st.pointsTextureSize * st.pointsTextureSize
	randomValuesState := make([]float32, n*4)
	for i := 0; i < n; i++ {
		randomValuesState[i*4] = float32(st.getRandomFloat(-1, 1) * 0.00001)
		randomValuesState[i*4+1] = float32(st.getRandomFloat(-1, 1) * 0.00001)
	}
	return ctx.newFramebuffer(st.pointsTextureSize, st.pointsTextureSize, randomValuesState)
}

// forceManyBody is the port of the default (non-quadtree) ForceManyBody
// module: grid levels + a ring sweep per level.
type forceManyBody struct {
	randomValuesFbo            *framebuffer
	levelsFbos                 []*framebuffer
	clearLevelsCommand         *command
	clearVelocityCommand       *command
	calculateLevelsCommand     *command
	forceCommand               *command
	forceFromCentermassCommand *command
	quadtreeLevels             int
	spaceSize                  float64

	// per-call props
	propLevelFbo         *framebuffer
	propLevelTextureSize float64
	propCellSize         float64
	propLevel            int
}

func newForceManyBody() *forceManyBody { return &forceManyBody{} }

func (f *forceManyBody) create(ctx *glCtx, st *store) {
	if st.pointsTextureSize == 0 {
		return
	}
	f.quadtreeLevels = int(math.Log2(st.adjustedSpaceSize))
	f.spaceSize = st.adjustedSpaceSize
	for _, fbo := range f.levelsFbos {
		fbo.destroy()
	}
	f.levelsFbos = f.levelsFbos[:0]
	for i := 0; i < f.quadtreeLevels; i++ {
		levelTextureSize := 1 << uint(i+1)
		f.levelsFbos = append(f.levelsFbos, ctx.newFramebuffer(levelTextureSize, levelTextureSize, nil))
	}
	f.randomValuesFbo.destroy()
	f.randomValuesFbo = createRandomValuesFbo(ctx, st)
}

func (f *forceManyBody) initPrograms(ctx *glCtx, cfg *Config, st *store, data *graphData, pts *points, quadAttr, indexAttr []attrBinding) error {
	if f.clearLevelsCommand == nil {
		prog, err := ctx.program(quadVert, clearFrag)
		if err != nil {
			return err
		}
		f.clearLevelsCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return f.propLevelFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
		f.clearVelocityCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return pts.velocityFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
	}

	if f.calculateLevelsCommand == nil {
		prog, err := ctx.program(calculateLevelVert, calculateLevelFrag)
		if err != nil {
			return err
		}
		f.calculateLevelsCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return f.propLevelFbo },
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs:     indexAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":  func() uniformValue { return pts.previousPositionFbo },
				"pointsTextureSize": func() uniformValue { return float64(st.pointsTextureSize) },
				"levelTextureSize":  func() uniformValue { return f.propLevelTextureSize },
				"cellSize":          func() uniformValue { return f.propCellSize },
			},
			blend: "add", depthOff: true,
		}
	}

	if f.forceCommand == nil {
		prog, err := ctx.program(quadVert, forceLevelFrag)
		if err != nil {
			return err
		}
		f.forceCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return pts.velocityFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture": func() uniformValue { return pts.previousPositionFbo },
				"level":            func() uniformValue { return float64(f.propLevel) },
				"levels":           func() uniformValue { return float64(f.quadtreeLevels) },
				"levelFbo":         func() uniformValue { return f.propLevelFbo },
				"levelTextureSize": func() uniformValue { return f.propLevelTextureSize },
				"alpha":            func() uniformValue { return st.alpha },
				"repulsion":        func() uniformValue { return cfg.SimulationRepulsion },
				"spaceSize":        func() uniformValue { return st.adjustedSpaceSize },
				"theta":            func() uniformValue { return cfg.SimulationRepulsionTheta },
			},
			blend: "add", depthOff: true,
		}
	}

	if f.forceFromCentermassCommand == nil {
		prog, err := ctx.program(quadVert, forceCentermassFrag)
		if err != nil {
			return err
		}
		f.forceFromCentermassCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return pts.velocityFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture": func() uniformValue { return pts.previousPositionFbo },
				"randomValues":     func() uniformValue { return f.randomValuesFbo },
				"levelFbo":         func() uniformValue { return f.propLevelFbo },
				"levelTextureSize": func() uniformValue { return f.propLevelTextureSize },
				"alpha":            func() uniformValue { return st.alpha },
				"repulsion":        func() uniformValue { return cfg.SimulationRepulsion },
				"spaceSize":        func() uniformValue { return st.adjustedSpaceSize },
			},
			blend: "add", depthOff: true,
		}
	}
	return nil
}

func (f *forceManyBody) run() {
	for i := 0; i < f.quadtreeLevels; i++ {
		f.propLevelFbo = f.levelsFbos[i]
		f.clearLevelsCommand.run()
		levelTextureSize := 1 << uint(i+1)
		f.propLevelTextureSize = float64(levelTextureSize)
		f.propCellSize = f.spaceSize / float64(levelTextureSize)
		f.calculateLevelsCommand.run()
	}
	f.clearVelocityCommand.run()
	for i := 0; i < f.quadtreeLevels; i++ {
		f.propLevelFbo = f.levelsFbos[i]
		f.propLevelTextureSize = float64(int(1) << uint(i+1))
		f.propLevel = i
		f.forceCommand.run()

		if i == f.quadtreeLevels-1 {
			f.forceFromCentermassCommand.run()
		}
	}
}

func (f *forceManyBody) destroy() {
	f.randomValuesFbo.destroy()
	f.randomValuesFbo = nil
	for _, fbo := range f.levelsFbos {
		fbo.destroy()
	}
	f.levelsFbos = nil
}

// forceManyBodyQuadtree is the port of the ForceManyBodyQuadtree module
// (classic Barnes–Hut with a generated recursive shader).
type forceManyBodyQuadtree struct {
	randomValuesFbo        *framebuffer
	levelsFbos             []*framebuffer
	clearLevelsCommand     *command
	calculateLevelsCommand *command
	quadtreeCommand        *command
	quadtreeLevels         int
	spaceSize              float64

	propLevelFbo         *framebuffer
	propLevelTextureSize float64
	propCellSize         float64
}

func newForceManyBodyQuadtree() *forceManyBodyQuadtree { return &forceManyBodyQuadtree{} }

func (f *forceManyBodyQuadtree) create(ctx *glCtx, st *store) {
	if st.pointsTextureSize == 0 {
		return
	}
	f.quadtreeLevels = int(math.Log2(st.adjustedSpaceSize))
	f.spaceSize = st.adjustedSpaceSize
	for _, fbo := range f.levelsFbos {
		fbo.destroy()
	}
	f.levelsFbos = f.levelsFbos[:0]
	for i := 0; i < f.quadtreeLevels; i++ {
		levelTextureSize := 1 << uint(i+1)
		f.levelsFbos = append(f.levelsFbos, ctx.newFramebuffer(levelTextureSize, levelTextureSize, nil))
	}
	f.randomValuesFbo.destroy()
	f.randomValuesFbo = createRandomValuesFbo(ctx, st)
}

func (f *forceManyBodyQuadtree) initPrograms(ctx *glCtx, cfg *Config, st *store, data *graphData, pts *points, quadAttr, indexAttr []attrBinding) error {
	if f.clearLevelsCommand == nil {
		prog, err := ctx.program(quadVert, clearFrag)
		if err != nil {
			return err
		}
		f.clearLevelsCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return f.propLevelFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
	}
	if f.calculateLevelsCommand == nil {
		prog, err := ctx.program(calculateLevelVert, calculateLevelFrag)
		if err != nil {
			return err
		}
		f.calculateLevelsCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return f.propLevelFbo },
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs:     indexAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":  func() uniformValue { return pts.previousPositionFbo },
				"pointsTextureSize": func() uniformValue { return float64(st.pointsTextureSize) },
				"levelTextureSize":  func() uniformValue { return f.propLevelTextureSize },
				"cellSize":          func() uniformValue { return f.propCellSize },
			},
			blend: "add", depthOff: true,
		}
	}

	startLevel := cfg.SimulationRepulsionQuadtreeLevels
	uniforms := map[string]func() uniformValue{
		"positionsTexture": func() uniformValue { return pts.previousPositionFbo },
		"randomValues":     func() uniformValue { return f.randomValuesFbo },
		"spaceSize":        func() uniformValue { return st.adjustedSpaceSize },
		"repulsion":        func() uniformValue { return cfg.SimulationRepulsion },
		"theta":            func() uniformValue { return cfg.SimulationRepulsionTheta },
		"alpha":            func() uniformValue { return st.alpha },
	}
	for i := 0; i < f.quadtreeLevels; i++ {
		i := i
		uniforms[levelUniformName(i)] = func() uniformValue { return f.levelsFbos[i] }
	}
	prog, err := ctx.program(quadVert, quadtreeFrag(startLevel, f.quadtreeLevels))
	if err != nil {
		return err
	}
	f.quadtreeCommand = &command{
		ctx: ctx, prog: prog,
		fbo:       func() *framebuffer { return pts.velocityFbo },
		primitive: "triangle strip",
		count:     func() int { return 4 },
		attrs:     quadAttr,
		uniforms:  uniforms,
	}
	return nil
}

func levelUniformName(i int) string {
	return "level[" + itoa(i) + "]"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func (f *forceManyBodyQuadtree) run() {
	for i := 0; i < f.quadtreeLevels; i++ {
		f.propLevelFbo = f.levelsFbos[i]
		f.clearLevelsCommand.run()
		levelTextureSize := 1 << uint(i+1)
		f.propLevelTextureSize = float64(levelTextureSize)
		f.propCellSize = f.spaceSize / float64(levelTextureSize)
		f.calculateLevelsCommand.run()
	}
	f.quadtreeCommand.run()
}

func (f *forceManyBodyQuadtree) destroy() {
	f.randomValuesFbo.destroy()
	f.randomValuesFbo = nil
	for _, fbo := range f.levelsFbos {
		fbo.destroy()
	}
	f.levelsFbos = nil
}
