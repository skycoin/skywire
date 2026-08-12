//go:build js && wasm

package cosmos

// forceGravity is the port of the ForceGravity module.
type forceGravity struct {
	runCommand *command
}

func newForceGravity() *forceGravity { return &forceGravity{} }

func (f *forceGravity) initPrograms(ctx *glCtx, cfg *Config, st *store, pts *points, quadAttr []attrBinding) error {
	if f.runCommand != nil {
		return nil
	}
	prog, err := ctx.program(quadVert, forceGravityFrag)
	if err != nil {
		return err
	}
	f.runCommand = &command{
		ctx: ctx, prog: prog,
		fbo:       func() *framebuffer { return pts.velocityFbo },
		primitive: "triangle strip",
		count:     func() int { return 4 },
		attrs:     quadAttr,
		uniforms: map[string]func() uniformValue{
			"positionsTexture": func() uniformValue { return pts.previousPositionFbo },
			"gravity":          func() uniformValue { return cfg.SimulationGravity },
			"spaceSize":        func() uniformValue { return st.adjustedSpaceSize },
			"alpha":            func() uniformValue { return st.alpha },
		},
	}
	return nil
}

func (f *forceGravity) run() { f.runCommand.run() }

// forceCenter is the port of the ForceCenter module.
type forceCenter struct {
	centermassFbo          *framebuffer
	clearCentermassCommand *command
	calculateCentermassCmd *command
	runCommand             *command
}

func newForceCenter() *forceCenter { return &forceCenter{} }

func (f *forceCenter) create(ctx *glCtx) {
	f.centermassFbo.destroy()
	f.centermassFbo = ctx.newFramebuffer(1, 1, make([]float32, 4))
}

func (f *forceCenter) initPrograms(ctx *glCtx, cfg *Config, st *store, data *graphData, pts *points, quadAttr, indexAttr []attrBinding) error {
	if f.clearCentermassCommand == nil {
		prog, err := ctx.program(quadVert, clearFrag)
		if err != nil {
			return err
		}
		f.clearCentermassCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return f.centermassFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
	}
	if f.calculateCentermassCmd == nil {
		prog, err := ctx.program(calculateCentermassVert, calculateCentermassFrag)
		if err != nil {
			return err
		}
		f.calculateCentermassCmd = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return f.centermassFbo },
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs:     indexAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":  func() uniformValue { return pts.previousPositionFbo },
				"pointsTextureSize": func() uniformValue { return float64(st.pointsTextureSize) },
			},
			blend: "add", depthOff: true,
		}
	}
	if f.runCommand == nil {
		prog, err := ctx.program(quadVert, forceCenterFrag)
		if err != nil {
			return err
		}
		f.runCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return pts.velocityFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":  func() uniformValue { return pts.previousPositionFbo },
				"centermassTexture": func() uniformValue { return f.centermassFbo },
				"centerForce":       func() uniformValue { return cfg.SimulationCenter },
				"alpha":             func() uniformValue { return st.alpha },
			},
		}
	}
	return nil
}

func (f *forceCenter) run() {
	f.clearCentermassCommand.run()
	f.calculateCentermassCmd.run()
	f.runCommand.run()
}

func (f *forceCenter) destroy() {
	f.centermassFbo.destroy()
	f.centermassFbo = nil
}

// forceMouse is the port of the ForceMouse module.
type forceMouse struct {
	runCommand *command
}

func newForceMouse() *forceMouse { return &forceMouse{} }

func (f *forceMouse) initPrograms(ctx *glCtx, cfg *Config, st *store, pts *points, quadAttr []attrBinding) error {
	if f.runCommand != nil {
		return nil
	}
	prog, err := ctx.program(quadVert, forceMouseFrag)
	if err != nil {
		return err
	}
	f.runCommand = &command{
		ctx: ctx, prog: prog,
		fbo:       func() *framebuffer { return pts.velocityFbo },
		primitive: "triangle strip",
		count:     func() int { return 4 },
		attrs:     quadAttr,
		uniforms: map[string]func() uniformValue{
			"positionsTexture": func() uniformValue { return pts.previousPositionFbo },
			"mousePos":         func() uniformValue { return st.mousePosition[:] },
			"repulsion":        func() uniformValue { return cfg.SimulationRepulsionFromMouse },
		},
	}
	return nil
}

func (f *forceMouse) run() { f.runCommand.run() }
