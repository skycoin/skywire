//go:build js && wasm

package cosmos

import (
	"math"
	"syscall/js"
)

// points is the port of the Points module.
type points struct {
	ctx  *glCtx
	cfg  *Config
	st   *store
	data *graphData

	currentPositionFbo  *framebuffer
	previousPositionFbo *framebuffer
	velocityFbo         *framebuffer
	selectedFbo         *framebuffer
	hoveredFbo          *framebuffer
	greyoutStatusFbo    *framebuffer
	pinnedStatusFbo     *framebuffer
	sizeFbo             *framebuffer
	trackedIndicesFbo   *framebuffer
	trackedPositionsFbo *framebuffer
	sampledPointsFbo    *framebuffer
	polygonPathFbo      *framebuffer
	polygonPathLength   int

	// rescale scale functions (set when positions were rescaled)
	scaleXFn func(float64) float64
	scaleYFn func(float64) float64
	// one-time skip flag for SetPointPositions
	shouldSkipRescale bool
	hasSkipRescale    bool

	imageAtlasTexture           *texture
	imageAtlasCoordsTexture     *texture
	imageAtlasCoordsTextureSize int
	imageCount                  int

	colorBuffer        *buffer
	sizeBuffer         *buffer
	shapeBuffer        *buffer
	imageIndicesBuffer *buffer
	imageSizesBuffer   *buffer
	indexesBuffer      *buffer
	quadBuffer         *buffer

	drawCommand                         *command
	drawHighlightedCommand              *command
	updatePositionCommand               *command
	dragPointCommand                    *command
	findPointsOnAreaSelectionCommand    *command
	findPointsOnPolygonSelectionCommand *command
	findHoveredPointCommand             *command
	clearHoveredFboCommand              *command
	clearSampledPointsFboCommand        *command
	fillSampledPointsFboCommand         *command
	trackPointsCommand                  *command

	trackedIndices    []int
	trackedPositions  map[int][2]float64
	positionsUpToDate bool

	// draw props
	skipSelected   bool
	skipUnselected bool
	// highlight props
	hlColor      [4]float64
	hlWidth      float64
	hlPointIndex int
	hlSize       float64
}

func newPoints(ctx *glCtx, cfg *Config, st *store, data *graphData) *points {
	return &points{
		ctx:        ctx,
		cfg:        cfg,
		st:         st,
		data:       data,
		quadBuffer: ctx.newBuffer([]float32{-1, -1, 1, -1, -1, 1, 1, 1}),
	}
}

func createIndexesArray(textureSize int) []float32 {
	indexes := make([]float32, textureSize*textureSize*2)
	for y := 0; y < textureSize; y++ {
		for x := 0; x < textureSize; x++ {
			i := y*textureSize*2 + x*2
			indexes[i] = float32(x)
			indexes[i+1] = float32(y)
		}
	}
	return indexes
}

func (p *points) updatePositions() {
	st, data, cfg := p.st, p.data, p.cfg
	pointsTextureSize := st.pointsTextureSize
	if pointsTextureSize == 0 || data.pointPositions == nil {
		return
	}
	n := data.pointsNumber()

	shouldRescale := cfg.RescalePositions == RescaleAlways ||
		(cfg.RescalePositions == RescaleAuto && !cfg.EnableSimulation)
	if p.hasSkipRescale && p.shouldSkipRescale {
		shouldRescale = false
	}
	if shouldRescale {
		p.rescaleInitialPointPositions()
	} else if !(p.hasSkipRescale && p.shouldSkipRescale) {
		p.scaleXFn = nil
		p.scaleYFn = nil
	}
	p.hasSkipRescale = false
	p.shouldSkipRescale = false

	initialState := make([]float32, pointsTextureSize*pointsTextureSize*4)
	for i := 0; i < n; i++ {
		initialState[i*4+0] = data.pointPositions[i*2+0]
		initialState[i*4+1] = data.pointPositions[i*2+1]
		initialState[i*4+2] = float32(i)
	}

	p.currentPositionFbo.destroy()
	p.currentPositionFbo = p.ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, initialState)
	p.previousPositionFbo.destroy()
	p.previousPositionFbo = p.ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, initialState)

	if cfg.EnableSimulation {
		p.velocityFbo.destroy()
		p.velocityFbo = p.ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, nil)
	}

	p.selectedFbo.destroy()
	p.selectedFbo = p.ctx.newFramebuffer(pointsTextureSize, pointsTextureSize, initialState)

	if p.hoveredFbo == nil {
		p.hoveredFbo = p.ctx.newFramebuffer(2, 2, nil)
	}

	p.indexesBuffer.destroy()
	p.indexesBuffer = p.ctx.newBuffer(createIndexesArray(pointsTextureSize))

	p.updateGreyoutStatus()
	p.updatePinnedStatus()
	p.updateSampledPointsGrid()

	p.trackPointsByIndices(p.trackedIndices)
}

func (p *points) initPrograms() error {
	ctx, cfg, st, data := p.ctx, p.cfg, p.st, p.data

	quadAttr := []attrBinding{{name: "vertexCoord", buffer: func() *buffer { return p.quadBuffer }, size: 2}}
	indexAttr := []attrBinding{{name: "pointIndices", buffer: func() *buffer { return p.indexesBuffer }, size: 2}}

	if cfg.EnableSimulation && p.updatePositionCommand == nil {
		prog, err := ctx.program(quadVert, updatePositionFrag)
		if err != nil {
			return err
		}
		p.updatePositionCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.currentPositionFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":    func() uniformValue { return p.previousPositionFbo },
				"velocity":            func() uniformValue { return p.velocityFbo },
				"friction":            func() uniformValue { return cfg.SimulationFriction },
				"spaceSize":           func() uniformValue { return st.adjustedSpaceSize },
				"pinnedStatusTexture": func() uniformValue { return p.pinnedStatusFbo },
			},
		}
	}

	if p.dragPointCommand == nil {
		prog, err := ctx.program(quadVert, dragPointFrag)
		if err != nil {
			return err
		}
		p.dragPointCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.currentPositionFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture": func() uniformValue { return p.previousPositionFbo },
				"mousePos":         func() uniformValue { return p.st.mousePosition[:] },
				"index": func() uniformValue {
					if st.hoveredPoint != nil {
						return float64(st.hoveredPoint.index)
					}
					return -1.0
				},
			},
		}
	}

	if p.drawCommand == nil {
		prog, err := ctx.program(drawPointsVert, drawPointsFrag)
		if err != nil {
			return err
		}
		p.drawCommand = &command{
			ctx: ctx, prog: prog,
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs: []attrBinding{
				{name: "pointIndices", buffer: func() *buffer { return p.indexesBuffer }, size: 2},
				{name: "size", buffer: func() *buffer { return p.sizeBuffer }, size: 1},
				{name: "color", buffer: func() *buffer { return p.colorBuffer }, size: 4},
				{name: "shape", buffer: func() *buffer { return p.shapeBuffer }, size: 1},
				{name: "imageIndex", buffer: func() *buffer { return p.imageIndicesBuffer }, size: 1},
				{name: "imageSize", buffer: func() *buffer { return p.imageSizesBuffer }, size: 1},
			},
			uniforms: map[string]func() uniformValue{
				"positionsTexture":     func() uniformValue { return p.currentPositionFbo },
				"pointGreyoutStatus":   func() uniformValue { return p.greyoutStatusFbo },
				"ratio":                func() uniformValue { return cfg.PixelRatio },
				"sizeScale":            func() uniformValue { return cfg.PointSizeScale },
				"pointsTextureSize":    func() uniformValue { return float64(st.pointsTextureSize) },
				"transformationMatrix": func() uniformValue { return st.transform[:] },
				"spaceSize":            func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":           func() uniformValue { return st.screenSize[:] },
				"pointOpacity":         func() uniformValue { return cfg.PointOpacity },
				"greyoutOpacity":       func() uniformValue { return cfg.PointGreyoutOpacity },
				"greyoutColor":         func() uniformValue { return st.greyoutPointColor[:] },
				"backgroundColor":      func() uniformValue { return st.backgroundColor[:] },
				"isDarkenGreyout":      func() uniformValue { return st.isDarkenGreyout },
				"scalePointsOnZoom":    func() uniformValue { return cfg.ScalePointsOnZoom },
				"maxPointSize":         func() uniformValue { return st.maxPointSize },
				"skipSelected":         func() uniformValue { return p.skipSelected },
				"skipUnselected":       func() uniformValue { return p.skipUnselected },
				"imageAtlasTexture":    func() uniformValue { return p.imageAtlasTexture },
				"imageAtlasCoords":     func() uniformValue { return p.imageAtlasCoordsTexture },
				"hasImages":            func() uniformValue { return p.imageCount > 0 },
				"imageCount":           func() uniformValue { return float64(p.imageCount) },
				"imageAtlasCoordsTextureSize": func() uniformValue {
					return float64(p.imageAtlasCoordsTextureSize)
				},
			},
			blend: "alpha", depthOff: true,
		}
	}

	if p.findPointsOnAreaSelectionCommand == nil {
		prog, err := ctx.program(quadVert, findPointsOnAreaSelectionFrag)
		if err != nil {
			return err
		}
		p.findPointsOnAreaSelectionCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.selectedFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":     func() uniformValue { return p.currentPositionFbo },
				"pointSize":            func() uniformValue { return p.sizeFbo },
				"spaceSize":            func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":           func() uniformValue { return st.screenSize[:] },
				"sizeScale":            func() uniformValue { return cfg.PointSizeScale },
				"transformationMatrix": func() uniformValue { return st.transform[:] },
				"ratio":                func() uniformValue { return cfg.PixelRatio },
				"selection0":           func() uniformValue { return st.selectedArea[0][:] },
				"selection1":           func() uniformValue { return st.selectedArea[1][:] },
				"scalePointsOnZoom":    func() uniformValue { return cfg.ScalePointsOnZoom },
				"maxPointSize":         func() uniformValue { return st.maxPointSize },
			},
		}
	}

	if p.findPointsOnPolygonSelectionCommand == nil {
		prog, err := ctx.program(quadVert, findPointsOnPolygonSelectionFrag)
		if err != nil {
			return err
		}
		p.findPointsOnPolygonSelectionCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.selectedFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":     func() uniformValue { return p.currentPositionFbo },
				"spaceSize":            func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":           func() uniformValue { return st.screenSize[:] },
				"transformationMatrix": func() uniformValue { return st.transform[:] },
				"polygonPathTexture": func() uniformValue {
					if p.polygonPathFbo != nil {
						return p.polygonPathFbo
					}
					return nil
				},
				"polygonPathLength": func() uniformValue { return p.polygonPathLength },
			},
		}
	}

	if p.clearHoveredFboCommand == nil {
		prog, err := ctx.program(quadVert, clearFrag)
		if err != nil {
			return err
		}
		p.clearHoveredFboCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.hoveredFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
		p.clearSampledPointsFboCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.sampledPointsFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms:  map[string]func() uniformValue{},
		}
	}

	if p.findHoveredPointCommand == nil {
		prog, err := ctx.program(findHoveredPointVert, findHoveredPointFrag)
		if err != nil {
			return err
		}
		p.findHoveredPointCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.hoveredFbo },
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs: []attrBinding{
				{name: "pointIndices", buffer: func() *buffer { return p.indexesBuffer }, size: 2},
				{name: "size", buffer: func() *buffer { return p.sizeBuffer }, size: 1},
			},
			uniforms: map[string]func() uniformValue{
				"positionsTexture":     func() uniformValue { return p.currentPositionFbo },
				"ratio":                func() uniformValue { return cfg.PixelRatio },
				"sizeScale":            func() uniformValue { return cfg.PointSizeScale },
				"pointsTextureSize":    func() uniformValue { return float64(st.pointsTextureSize) },
				"transformationMatrix": func() uniformValue { return st.transform[:] },
				"spaceSize":            func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":           func() uniformValue { return st.screenSize[:] },
				"scalePointsOnZoom":    func() uniformValue { return cfg.ScalePointsOnZoom },
				"mousePosition":        func() uniformValue { return st.screenMousePosition[:] },
				"maxPointSize":         func() uniformValue { return st.maxPointSize },
			},
			depthOff: true,
		}
	}

	if p.fillSampledPointsFboCommand == nil {
		prog, err := ctx.program(fillSampledPointsVert, fillSampledPointsFrag)
		if err != nil {
			return err
		}
		p.fillSampledPointsFboCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.sampledPointsFbo },
			primitive: "points",
			count:     func() int { return data.pointsNumber() },
			attrs:     indexAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":     func() uniformValue { return p.currentPositionFbo },
				"pointsTextureSize":    func() uniformValue { return float64(st.pointsTextureSize) },
				"transformationMatrix": func() uniformValue { return st.transform[:] },
				"spaceSize":            func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":           func() uniformValue { return st.screenSize[:] },
			},
			depthOff: true,
		}
	}

	if p.drawHighlightedCommand == nil {
		prog, err := ctx.program(drawHighlightedVert, drawHighlightedFrag)
		if err != nil {
			return err
		}
		p.drawHighlightedCommand = &command{
			ctx: ctx, prog: prog,
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"color":                     func() uniformValue { return p.hlColor[:] },
				"width":                     func() uniformValue { return p.hlWidth },
				"pointIndex":                func() uniformValue { return float64(p.hlPointIndex) },
				"size":                      func() uniformValue { return p.hlSize },
				"positionsTexture":          func() uniformValue { return p.currentPositionFbo },
				"sizeScale":                 func() uniformValue { return cfg.PointSizeScale },
				"pointsTextureSize":         func() uniformValue { return float64(st.pointsTextureSize) },
				"transformationMatrix":      func() uniformValue { return st.transform[:] },
				"spaceSize":                 func() uniformValue { return st.adjustedSpaceSize },
				"screenSize":                func() uniformValue { return st.screenSize[:] },
				"scalePointsOnZoom":         func() uniformValue { return cfg.ScalePointsOnZoom },
				"maxPointSize":              func() uniformValue { return st.maxPointSize },
				"pointGreyoutStatusTexture": func() uniformValue { return p.greyoutStatusFbo },
				"universalPointOpacity":     func() uniformValue { return cfg.PointOpacity },
				"greyoutOpacity":            func() uniformValue { return cfg.PointGreyoutOpacity },
				"isDarkenGreyout":           func() uniformValue { return st.isDarkenGreyout },
				"backgroundColor":           func() uniformValue { return st.backgroundColor[:] },
				"greyoutColor":              func() uniformValue { return st.greyoutPointColor[:] },
			},
			blend: "alpha", depthOff: true,
		}
	}

	if p.trackPointsCommand == nil {
		prog, err := ctx.program(quadVert, trackPositionsFrag)
		if err != nil {
			return err
		}
		p.trackPointsCommand = &command{
			ctx: ctx, prog: prog,
			fbo:       func() *framebuffer { return p.trackedPositionsFbo },
			primitive: "triangle strip",
			count:     func() int { return 4 },
			attrs:     quadAttr,
			uniforms: map[string]func() uniformValue{
				"positionsTexture":  func() uniformValue { return p.currentPositionFbo },
				"trackedIndices":    func() uniformValue { return p.trackedIndicesFbo },
				"pointsTextureSize": func() uniformValue { return float64(st.pointsTextureSize) },
			},
		}
	}
	return nil
}

func (p *points) updateColor() {
	if p.st.pointsTextureSize == 0 || p.data.pointColors == nil {
		return
	}
	p.colorBuffer.destroy()
	p.colorBuffer = p.ctx.newBuffer(p.data.pointColors)
}

func (p *points) updateGreyoutStatus() {
	textureSize := p.st.pointsTextureSize
	if textureSize == 0 {
		return
	}
	// Greyout status: 0 - false, highlighted or normal point; 1 - true
	initialState := make([]float32, textureSize*textureSize*4)
	if p.st.hasSelection {
		for i := range initialState {
			initialState[i] = 1
		}
		for _, selectedIndex := range p.st.selectedIndices {
			if selectedIndex >= 0 && selectedIndex*4 < len(initialState) {
				initialState[selectedIndex*4] = 0
			}
		}
	}
	p.greyoutStatusFbo.destroy()
	p.greyoutStatusFbo = p.ctx.newFramebuffer(textureSize, textureSize, initialState)
}

func (p *points) updatePinnedStatus() {
	textureSize := p.st.pointsTextureSize
	if textureSize == 0 {
		return
	}
	// Pinned status: 0 - not pinned, 1 - pinned
	initialState := make([]float32, textureSize*textureSize*4)
	n := p.data.pointsNumber()
	for _, pinnedIndex := range p.data.inputPinnedPoints {
		if pinnedIndex >= 0 && pinnedIndex < n {
			initialState[pinnedIndex*4] = 1
		}
	}
	p.pinnedStatusFbo.destroy()
	p.pinnedStatusFbo = p.ctx.newFramebuffer(textureSize, textureSize, initialState)
}

func (p *points) updateSize() {
	textureSize := p.st.pointsTextureSize
	if textureSize == 0 || p.data.pointSizes == nil {
		return
	}
	p.sizeBuffer.destroy()
	p.sizeBuffer = p.ctx.newBuffer(p.data.pointSizes)

	initialState := make([]float32, textureSize*textureSize*4)
	for i, size := range p.data.pointSizes {
		initialState[i*4] = size
	}
	p.sizeFbo.destroy()
	p.sizeFbo = p.ctx.newFramebuffer(textureSize, textureSize, initialState)
}

func (p *points) updateShape() {
	if p.data.pointShapes == nil {
		return
	}
	p.shapeBuffer.destroy()
	p.shapeBuffer = p.ctx.newBuffer(p.data.pointShapes)
}

func (p *points) updateImageIndices() {
	if p.data.pointImageIndices == nil {
		return
	}
	p.imageIndicesBuffer.destroy()
	p.imageIndicesBuffer = p.ctx.newBuffer(p.data.pointImageIndices)
}

func (p *points) updateImageSizes() {
	if p.data.pointImageSizes == nil {
		return
	}
	p.imageSizesBuffer.destroy()
	p.imageSizesBuffer = p.ctx.newBuffer(p.data.pointImageSizes)
}

// createAtlas builds the image atlas from ImageData objects
// (the port of atlas-utils.ts).
func (p *points) createAtlas(imageData []js.Value) {
	p.imageAtlasTexture.destroy()
	p.imageAtlasTexture = nil
	p.imageAtlasCoordsTexture.destroy()
	p.imageAtlasCoordsTexture = nil
	p.imageCount = 0
	p.imageAtlasCoordsTextureSize = 0
	if len(imageData) == 0 {
		return
	}

	type img struct {
		w, h int
		data []byte
	}
	images := make([]img, len(imageData))
	maxDimension := 0
	for i, id := range imageData {
		w := id.Get("width").Int()
		h := id.Get("height").Int()
		data := make([]byte, w*h*4)
		u8 := js.Global().Get("Uint8Array").New(id.Get("data").Get("buffer"), id.Get("data").Get("byteOffset"), w*h*4)
		js.CopyBytesToGo(data, u8)
		images[i] = img{w: w, h: h, data: data}
		if d := max(w, h); d > maxDimension {
			maxDimension = d
		}
	}
	if maxDimension == 0 {
		consoleWarn("Invalid image dimensions: all images have zero width or height")
		return
	}

	atlasCoordsSize := int(math.Ceil(math.Sqrt(float64(len(images)))))
	atlasSize := atlasCoordsSize * maxDimension

	if atlasSize > p.st.webglMaxTextureSize {
		scalingFactor := float64(p.st.webglMaxTextureSize) / float64(atlasSize)
		maxDimension = max(1, int(float64(maxDimension)*scalingFactor))
		atlasSize = max(1, int(float64(atlasSize)*scalingFactor))
		consoleWarn("Atlas scaling required: original size exceeds the WebGL texture size limit")
	}

	atlasData := make([]byte, atlasSize*atlasSize*4)
	atlasCoords := make([]float32, atlasCoordsSize*atlasCoordsSize*4)
	for i := range atlasCoords {
		atlasCoords[i] = -1
	}

	for index, im := range images {
		if im.w == 0 || im.h == 0 {
			continue
		}
		individualScale := math.Min(1.0, float64(maxDimension)/float64(max(im.w, im.h)))
		scaledWidth := int(float64(im.w) * individualScale)
		scaledHeight := int(float64(im.h) * individualScale)

		row := index / atlasCoordsSize
		col := index % atlasCoordsSize
		atlasX := col * maxDimension
		atlasY := row * maxDimension

		atlasCoords[index*4+0] = float32(atlasX) / float32(atlasSize)
		atlasCoords[index*4+1] = float32(atlasY) / float32(atlasSize)
		atlasCoords[index*4+2] = float32(atlasX+scaledWidth) / float32(atlasSize)
		atlasCoords[index*4+3] = float32(atlasY+scaledHeight) / float32(atlasSize)

		for y := 0; y < scaledHeight; y++ {
			for x := 0; x < scaledWidth; x++ {
				srcX := x * im.w / scaledWidth
				srcY := y * im.h / scaledHeight
				srcIndex := (srcY*im.w + srcX) * 4
				atlasIndex := ((atlasY+y)*atlasSize + (atlasX + x)) * 4
				copy(atlasData[atlasIndex:atlasIndex+4], im.data[srcIndex:srcIndex+4])
			}
		}
	}

	p.imageCount = len(images)
	p.imageAtlasCoordsTextureSize = atlasCoordsSize
	p.imageAtlasTexture = p.ctx.newUint8Texture(atlasSize, atlasSize, atlasData)
	p.imageAtlasCoordsTexture = p.ctx.newFloatTexture(atlasCoordsSize, atlasCoordsSize, atlasCoords)
}

func (p *points) updateSampledPointsGrid() {
	dist := p.cfg.PointSamplingDistance
	if dist == 0 {
		dist = math.Min(p.st.screenSize[0], p.st.screenSize[1]) / 2
	}
	if dist == 0 {
		dist = 150
	}
	w := max(1, int(math.Ceil(p.st.screenSize[0]/dist)))
	h := max(1, int(math.Ceil(p.st.screenSize[1]/dist)))
	p.sampledPointsFbo.destroy()
	p.sampledPointsFbo = p.ctx.newFramebuffer(w, h, nil)
}

func (p *points) trackPoints() {
	if len(p.trackedIndices) == 0 || p.trackedPositionsFbo == nil {
		return
	}
	p.trackPointsCommand.run()
}

func (p *points) draw() {
	if p.colorBuffer == nil {
		p.updateColor()
	}
	if p.sizeBuffer == nil {
		p.updateSize()
	}
	if p.shapeBuffer == nil {
		p.updateShape()
	}
	if p.imageIndicesBuffer == nil {
		p.updateImageIndices()
	}
	if p.imageSizesBuffer == nil {
		p.updateImageSizes()
	}

	// render in layers: unselected points first (behind), then selected
	if p.st.hasSelection && len(p.st.selectedIndices) > 0 {
		p.skipSelected, p.skipUnselected = true, false
		p.drawCommand.run()
		p.skipSelected, p.skipUnselected = false, true
		p.drawCommand.run()
	} else {
		p.skipSelected, p.skipUnselected = false, false
		p.drawCommand.run()
	}
	if p.cfg.RenderHoveredPointRing && p.st.hoveredPoint != nil {
		p.hlWidth = 0.85
		p.hlColor = p.st.hoveredPointRingColor
		p.hlPointIndex = p.st.hoveredPoint.index
		p.hlSize = p.pointSizeAt(p.st.hoveredPoint.index)
		p.drawHighlightedCommand.run()
	}
	if p.st.focusedPointIndex >= 0 {
		p.hlWidth = 0.75
		p.hlColor = p.st.focusedPointRingColor
		p.hlPointIndex = p.st.focusedPointIndex
		p.hlSize = p.pointSizeAt(p.st.focusedPointIndex)
		p.drawHighlightedCommand.run()
	}
}

func (p *points) pointSizeAt(index int) float64 {
	if index >= 0 && index < len(p.data.pointSizes) {
		return float64(p.data.pointSizes[index])
	}
	return p.cfg.PointDefaultSize
}

func (p *points) updatePosition() {
	p.updatePositionCommand.run()
	p.swapFbo()
	p.positionsUpToDate = false
}

func (p *points) drag() {
	p.dragPointCommand.run()
	p.swapFbo()
	p.positionsUpToDate = false
}

func (p *points) findPointsOnAreaSelection() {
	p.findPointsOnAreaSelectionCommand.run()
}

func (p *points) findPointsOnPolygonSelection() {
	p.findPointsOnPolygonSelectionCommand.run()
}

func (p *points) updatePolygonPath(polygonPath [][2]float64) {
	p.polygonPathLength = len(polygonPath)
	if len(polygonPath) == 0 {
		p.polygonPathFbo.destroy()
		p.polygonPathFbo = nil
		return
	}
	textureSize := int(math.Ceil(math.Sqrt(float64(len(polygonPath)))))
	textureData := make([]float32, textureSize*textureSize*4)
	for i, point := range polygonPath {
		textureData[i*4] = float32(point[0])
		textureData[i*4+1] = float32(point[1])
	}
	p.polygonPathFbo.destroy()
	p.polygonPathFbo = p.ctx.newFramebuffer(textureSize, textureSize, textureData)
}

func (p *points) findHoveredPoint() {
	p.clearHoveredFboCommand.run()
	p.findHoveredPointCommand.run()
}

func (p *points) trackPointsByIndices(indices []int) {
	p.trackedIndices = indices
	p.trackedPositions = nil
	p.positionsUpToDate = false

	pointsTextureSize := p.st.pointsTextureSize
	if len(indices) == 0 || pointsTextureSize == 0 {
		return
	}
	textureSize := int(math.Ceil(math.Sqrt(float64(len(indices)))))
	initialState := make([]float32, textureSize*textureSize*4)
	for i := range initialState {
		initialState[i] = -1
	}
	for i, index := range indices {
		initialState[i*4+0] = float32(index % pointsTextureSize)
		initialState[i*4+1] = float32(index / pointsTextureSize)
		initialState[i*4+2] = 0
		initialState[i*4+3] = 0
	}
	p.trackedIndicesFbo.destroy()
	p.trackedIndicesFbo = p.ctx.newFramebuffer(textureSize, textureSize, initialState)
	p.trackedPositionsFbo.destroy()
	p.trackedPositionsFbo = p.ctx.newFramebuffer(textureSize, textureSize, nil)

	p.trackPoints()
}

// getTrackedPositionsMap returns the tracked point positions, using a cache
// when the simulation is inactive to avoid GPU readbacks.
func (p *points) getTrackedPositionsMap() map[int][2]float64 {
	if len(p.trackedIndices) == 0 {
		return map[int][2]float64{}
	}
	simInactive := !p.cfg.EnableSimulation || !p.st.isSimulationRunning
	if simInactive && p.positionsUpToDate && p.trackedPositions != nil {
		return p.trackedPositions
	}

	pixels := p.trackedPositionsFbo.readPixels()
	tracked := map[int][2]float64{}
	for i := 0; i < len(pixels)/4 && i < len(p.trackedIndices); i++ {
		tracked[p.trackedIndices[i]] = [2]float64{float64(pixels[i*4]), float64(pixels[i*4+1])}
	}
	if simInactive {
		p.trackedPositions = tracked
		p.positionsUpToDate = true
	}
	return tracked
}

func (p *points) getTrackedPositionsArray() []float64 {
	if len(p.trackedIndices) == 0 {
		return nil
	}
	positions := make([]float64, len(p.trackedIndices)*2)
	pixels := p.trackedPositionsFbo.readPixels()
	for i := 0; i < len(pixels)/4 && i < len(p.trackedIndices); i++ {
		positions[i*2] = float64(pixels[i*4])
		positions[i*2+1] = float64(pixels[i*4+1])
	}
	return positions
}

func (p *points) getSampledPoints() (indices []int, positions []float64) {
	if p.sampledPointsFbo == nil {
		return nil, nil
	}
	p.clearSampledPointsFboCommand.run()
	p.fillSampledPointsFboCommand.run()
	pixels := p.sampledPointsFbo.readPixels()
	for i := 0; i < len(pixels)/4; i++ {
		isNotEmpty := pixels[i*4+1] != 0
		if isNotEmpty {
			indices = append(indices, int(pixels[i*4]))
			positions = append(positions, float64(pixels[i*4+2]), float64(pixels[i*4+3]))
		}
	}
	return indices, positions
}

func (p *points) swapFbo() {
	p.previousPositionFbo, p.currentPositionFbo = p.currentPositionFbo, p.previousPositionFbo
}

// rescaleInitialPointPositions is the port of rescaleInitialNodePositions:
// scales input positions into the simulation space based on density.
func (p *points) rescaleInitialPointPositions() {
	spaceSize := float64(p.cfg.SpaceSize)
	positions := p.data.pointPositions
	if positions == nil || spaceSize == 0 {
		return
	}
	pointsNumber := len(positions) / 2
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for i := 0; i < len(positions); i += 2 {
		x := float64(positions[i])
		y := float64(positions[i+1])
		minX = math.Min(minX, x)
		maxX = math.Max(maxX, x)
		minY = math.Min(minY, y)
		maxY = math.Max(maxY, y)
	}
	w := maxX - minX
	h := maxY - minY
	rangeSize := math.Max(w, h)

	// do not rescale if the range is greater than the space size
	if rangeSize > spaceSize {
		p.scaleXFn = nil
		p.scaleYFn = nil
		return
	}

	// density threshold - points per pixel ratio (0.001 = 0.1%)
	densityThreshold := spaceSize * spaceSize * 0.001
	var effectiveSpaceSize float64
	if float64(pointsNumber) > densityThreshold {
		// dense datasets: scale up based on point count, minimum 120% of space
		effectiveSpaceSize = spaceSize * math.Max(1.2, math.Sqrt(float64(pointsNumber))/spaceSize)
	} else {
		// sparse datasets: use 10% of space to cluster points closer
		effectiveSpaceSize = spaceSize * 0.1
	}

	scaleFactor := effectiveSpaceSize / rangeSize
	offsetX := ((rangeSize - w) / 2) * scaleFactor
	offsetY := ((rangeSize - h) / 2) * scaleFactor

	p.scaleXFn = func(x float64) float64 { return (x-minX)*scaleFactor + offsetX }
	p.scaleYFn = func(y float64) float64 { return (y-minY)*scaleFactor + offsetY }

	for i := 0; i < pointsNumber; i++ {
		positions[i*2] = float32(p.scaleXFn(float64(positions[i*2])))
		positions[i*2+1] = float32(p.scaleYFn(float64(positions[i*2+1])))
	}
}

func (p *points) destroy() {
	p.currentPositionFbo.destroy()
	p.previousPositionFbo.destroy()
	p.velocityFbo.destroy()
	p.selectedFbo.destroy()
	p.hoveredFbo.destroy()
	p.greyoutStatusFbo.destroy()
	p.pinnedStatusFbo.destroy()
	p.sizeFbo.destroy()
	p.trackedIndicesFbo.destroy()
	p.trackedPositionsFbo.destroy()
	p.sampledPointsFbo.destroy()
	p.polygonPathFbo.destroy()
	p.colorBuffer.destroy()
	p.sizeBuffer.destroy()
	p.shapeBuffer.destroy()
	p.imageIndicesBuffer.destroy()
	p.imageSizesBuffer.destroy()
	p.imageAtlasTexture.destroy()
	p.imageAtlasCoordsTexture.destroy()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
