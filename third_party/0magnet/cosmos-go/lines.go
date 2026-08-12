//go:build js && wasm

package cosmos

import "math"

// lines is the port of the Lines module (instanced curve/line rendering
// with link hover picking).
type lines struct {
	ctx    *glCtx
	cfg    *Config
	st     *store
	data   *graphData
	points *points

	linkIndexFbo        *framebuffer
	hoveredLineIndexFbo *framebuffer

	drawCurveCommand        *command
	hoveredLineIndexCommand *command

	pointsBuffer      *buffer
	colorBuffer       *buffer
	widthBuffer       *buffer
	arrowBuffer       *buffer
	linkIndexBuffer   *buffer
	curveLineGeometry [][2]float64
	curveLineBuffer   *buffer
	quadBuffer        *buffer

	// draw props
	propRenderMode float64
	propToIndexFbo bool
}

func newLines(ctx *glCtx, cfg *Config, st *store, data *graphData, pts *points) *lines {
	return &lines{ctx: ctx, cfg: cfg, st: st, data: data, points: pts,
		quadBuffer: ctx.newBuffer([]float32{-1, -1, 1, -1, -1, 1, 1, 1})}
}

func (l *lines) initPrograms() error {
	ctx, cfg, st := l.ctx, l.cfg, l.st

	l.updateLinkIndexFbo()

	if l.hoveredLineIndexFbo == nil {
		l.hoveredLineIndexFbo = ctx.newFramebuffer(1, 1, nil)
	}

	if l.drawCurveCommand == nil {
		prog, err := ctx.program(drawLineVert, drawLineFrag)
		if err != nil {
			return err
		}
		l.drawCurveCommand = &command{
			ctx: ctx, prog: prog,
			fbo: func() *framebuffer {
				if l.propToIndexFbo {
					return l.linkIndexFbo
				}
				return nil
			},
			primitive: "triangle strip",
			count:     func() int { return len(l.curveLineGeometry) },
			instances: func() int { return l.data.linksNumber() },
			attrs: []attrBinding{
				{name: "position", buffer: func() *buffer { return l.curveLineBuffer }, size: 2},
				{name: "pointA", buffer: func() *buffer { return l.pointsBuffer }, size: 2, offset: 0, stride: 16, divisor: 1},
				{name: "pointB", buffer: func() *buffer { return l.pointsBuffer }, size: 2, offset: 8, stride: 16, divisor: 1},
				{name: "color", buffer: func() *buffer { return l.colorBuffer }, size: 4, offset: 0, stride: 16, divisor: 1},
				{name: "width", buffer: func() *buffer { return l.widthBuffer }, size: 1, offset: 0, stride: 4, divisor: 1},
				{name: "arrow", buffer: func() *buffer { return l.arrowBuffer }, size: 1, offset: 0, stride: 4, divisor: 1},
				{name: "linkIndices", buffer: func() *buffer { return l.linkIndexBuffer }, size: 1, offset: 0, stride: 4, divisor: 1},
			},
			uniforms: map[string]func() uniformValue{
				"positionsTexture":              func() uniformValue { return l.points.currentPositionFbo },
				"pointGreyoutStatus":            func() uniformValue { return l.points.greyoutStatusFbo },
				"transformationMatrix":          func() uniformValue { return st.transform[:] },
				"pointsTextureSize":             func() uniformValue { return float64(st.pointsTextureSize) },
				"widthScale":                    func() uniformValue { return cfg.LinkWidthScale },
				"linkArrowsSizeScale":           func() uniformValue { return cfg.LinkArrowsSizeScale },
				"spaceSize":                     func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":                    func() uniformValue { return st.screenSize[:] },
				"linkVisibilityDistanceRange":   func() uniformValue { return cfg.LinkVisibilityDistanceRange[:] },
				"linkVisibilityMinTransparency": func() uniformValue { return cfg.LinkVisibilityMinTransparency },
				"linkOpacity":                   func() uniformValue { return cfg.LinkOpacity },
				"greyoutOpacity":                func() uniformValue { return cfg.LinkGreyoutOpacity },
				"scaleLinksOnZoom":              func() uniformValue { return cfg.ScaleLinksOnZoom },
				"maxPointSize":                  func() uniformValue { return st.maxPointSize },
				"curvedWeight":                  func() uniformValue { return cfg.CurvedLinkWeight },
				"curvedLinkControlPointDistance": func() uniformValue {
					return cfg.CurvedLinkControlPointDistance
				},
				"curvedLinkSegments": func() uniformValue {
					if cfg.CurvedLinks {
						return float64(cfg.CurvedLinkSegments)
					}
					return 1.0
				},
				"hoveredLinkIndex":         func() uniformValue { return float64(st.hoveredLinkIndex) },
				"hoveredLinkColor":         func() uniformValue { return st.hoveredLinkColor[:] },
				"hoveredLinkWidthIncrease": func() uniformValue { return cfg.HoveredLinkWidthIncrease },
				"renderMode":               func() uniformValue { return l.propRenderMode },
			},
			blend: "alpha", depthOff: true, cullBack: true,
		}
	}

	if l.hoveredLineIndexCommand == nil {
		prog, err := ctx.program(hoveredLineIndexVert, hoveredLineIndexFrag)
		if err != nil {
			return err
		}
		l.hoveredLineIndexCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return l.hoveredLineIndexFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs: []attrBinding{
				{name: "position", buffer: func() *buffer { return l.quadBuffer }, size: 2},
			},
			uniforms: map[string]func() uniformValue{
				"linkIndexTexture": func() uniformValue { return l.linkIndexFbo },
				"mousePosition":    func() uniformValue { return st.screenMousePosition[:] },
				"screenSize":       func() uniformValue { return st.screenSize[:] },
			},
		}
	}
	return nil
}

func (l *lines) draw() {
	if l.pointsBuffer == nil {
		return
	}
	if l.colorBuffer == nil {
		l.updateColor()
	}
	if l.widthBuffer == nil {
		l.updateWidth()
	}
	if l.arrowBuffer == nil {
		l.updateArrow()
	}
	if l.curveLineGeometry == nil {
		l.updateCurveLineGeometry()
	}

	// render normal links (renderMode: 0.0 = normal rendering)
	l.propToIndexFbo = false
	l.propRenderMode = 0
	l.drawCurveCommand.run()
}

// updateLinkIndexFbo (re)creates the screen-sized link index framebuffer
// used for link hover picking.
func (l *lines) updateLinkIndexFbo() {
	if !l.st.isLinkHoveringEnabled {
		return
	}
	w := max(1, int(l.st.screenSize[0]))
	h := max(1, int(l.st.screenSize[1]))
	l.linkIndexFbo.destroy()
	l.linkIndexFbo = l.ctx.newFramebuffer(w, h, nil)
}

func (l *lines) updatePointsBuffer() {
	data, st := l.data, l.st
	if data.links == nil {
		return
	}
	n := data.linksNumber()
	instancePoints := make([]float32, n*4)
	for i := 0; i < n; i++ {
		fromIndex := int(data.links[i*2])
		toIndex := int(data.links[i*2+1])
		instancePoints[i*4+0] = float32(fromIndex % st.pointsTextureSize)
		instancePoints[i*4+1] = float32(fromIndex / st.pointsTextureSize)
		instancePoints[i*4+2] = float32(toIndex % st.pointsTextureSize)
		instancePoints[i*4+3] = float32(toIndex / st.pointsTextureSize)
	}
	l.pointsBuffer.destroy()
	l.pointsBuffer = l.ctx.newBuffer(instancePoints)

	linkIndices := make([]float32, n)
	for i := range linkIndices {
		linkIndices[i] = float32(i)
	}
	l.linkIndexBuffer.destroy()
	l.linkIndexBuffer = l.ctx.newBuffer(linkIndices)
}

func (l *lines) updateColor() {
	l.colorBuffer.destroy()
	l.colorBuffer = l.ctx.newBuffer(l.data.linkColors)
}

func (l *lines) updateWidth() {
	l.widthBuffer.destroy()
	l.widthBuffer = l.ctx.newBuffer(l.data.linkWidths)
}

func (l *lines) updateArrow() {
	l.arrowBuffer.destroy()
	l.arrowBuffer = l.ctx.newBuffer(l.data.linkArrows)
}

// getCurveLineGeometry is the port of Lines/geometry.ts: a power-scale
// distributed triangle strip along the hodograph of the curve.
func getCurveLineGeometry(segments int) [][2]float64 {
	// d3 scalePow().exponent(2).range([0,1]).domain([-1,1]) maps
	// v → (sign(v)*|v|² + 1) / 2
	scale := func(v float64) float64 {
		p := math.Abs(v) * math.Abs(v)
		if v < 0 {
			p = -p
		}
		return (p + 1) / 2
	}
	hodographValues := make([]float64, 0, segments+1)
	for d := 0; d < segments; d++ {
		hodographValues = append(hodographValues, -0.5+float64(d)/float64(segments))
	}
	hodographValues = append(hodographValues, 0.5)
	result := make([][2]float64, len(hodographValues)*2)
	for i, d := range hodographValues {
		result[i*2] = [2]float64{scale(d * 2), 0.5}
		result[i*2+1] = [2]float64{scale(d * 2), -0.5}
	}
	return result
}

func (l *lines) updateCurveLineGeometry() {
	segments := 1
	if l.cfg.CurvedLinks {
		segments = l.cfg.CurvedLinkSegments
	}
	l.curveLineGeometry = getCurveLineGeometry(segments)
	flat := make([]float32, 0, len(l.curveLineGeometry)*2)
	for _, v := range l.curveLineGeometry {
		flat = append(flat, float32(v[0]), float32(v[1]))
	}
	l.curveLineBuffer.destroy()
	l.curveLineBuffer = l.ctx.newBuffer(flat)
}

func (l *lines) findHoveredLine() {
	if l.data.linksNumber() == 0 || !l.st.isLinkHoveringEnabled {
		return
	}
	if l.pointsBuffer == nil || l.linkIndexFbo == nil || l.linkIndexBuffer == nil ||
		l.colorBuffer == nil || l.widthBuffer == nil || l.arrowBuffer == nil ||
		l.curveLineGeometry == nil || l.curveLineBuffer == nil {
		return
	}

	l.ctx.clearTarget(l.linkIndexFbo, 0, 0, 0, 0)
	// render to the index buffer for picking (renderMode: 1.0)
	l.propToIndexFbo = true
	l.propRenderMode = 1
	l.drawCurveCommand.run()

	// read the link index at the mouse position
	l.hoveredLineIndexCommand.run()
}

func (l *lines) destroy() {
	l.colorBuffer.destroy()
	l.widthBuffer.destroy()
	l.arrowBuffer.destroy()
	l.curveLineBuffer.destroy()
	l.pointsBuffer.destroy()
	l.linkIndexBuffer.destroy()
	l.linkIndexFbo.destroy()
	l.hoveredLineIndexFbo.destroy()
}
