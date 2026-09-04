//go:build js && wasm

package xterm

// Port of addons/addon-webgl/src — WebglRenderer, GlyphRenderer,
// RectangleRenderer, RenderModel and WebglUtils. Backgrounds and
// glyphs are drawn as instanced quads; glyphs sample the texture
// atlas. Divergences from upstream: single atlas page (one sampler in
// the fragment shader), no decoration or ligature-joiner overrides in
// the color resolver, cursor blink comes from the terminal's shared
// blink timer instead of CursorBlinkStateManager.
//
// The selection is the one place this does more than the addon rather
// than less. xterm.js leaves selecting to the DOM, which cannot work
// here because these rows are hidden and the text is a texture; see
// selection.go, which keeps the selection as buffer cells so that both
// renderers can draw it.

import (
	"errors"
	"syscall/js"
	"unsafe"

	"github.com/0magnet/xterm-go/vt"
)

// WebGL2 constants (spec-fixed values).
const (
	glDepthBufferBit     = 0x00000100
	glColorBufferBit     = 0x00004000
	glTriangleStrip      = 0x0005
	glBlend              = 0x0BE2
	glMaxTextureSize     = 0x0D33
	glTexture2D          = 0x0DE1
	glUnsignedByte       = 0x1401
	glFloat              = 0x1406
	glRGBA               = 0x1908
	glVertexShader       = 0x8B31
	glFragmentShader     = 0x8B30
	glCompileStatus      = 0x8B81
	glLinkStatus         = 0x8B82
	glArrayBuffer        = 0x8892
	glElementArrayBuffer = 0x8893
	glStaticDraw         = 0x88E4
	glStreamDraw         = 0x88E0
	glDynamicDraw        = 0x88E8
	glTexture0           = 0x84C0
	glTextureWrapS       = 0x2802
	glTextureWrapT       = 0x2803
	glClampToEdge        = 0x812F
	glSrcAlpha           = 0x0302
	glOneMinusSrcAlpha   = 0x0303
	glTextureMinFilter   = 0x2801
	glLinear             = 0x2601
)

// A matrix that translates 0-1 coordinates (left-right, top-bottom) to
// clip space.
var projectionMatrix = []float32{
	2, 0, 0, 0,
	0, -2, 0, 0,
	0, 0, 1, 0,
	-1, 1, 0, 1,
}

const glyphVertexShader = `#version 300 es
layout (location = 0) in vec2 a_unitquad;
layout (location = 1) in vec2 a_cellpos;
layout (location = 2) in vec2 a_offset;
layout (location = 3) in vec2 a_size;
layout (location = 4) in float a_texpage;
layout (location = 5) in vec2 a_texcoord;
layout (location = 6) in vec2 a_texsize;

uniform mat4 u_projection;
uniform vec2 u_resolution;

out vec2 v_texcoord;

void main() {
  vec2 zeroToOne = (a_offset / u_resolution) + a_cellpos + (a_unitquad * a_size);
  gl_Position = u_projection * vec4(zeroToOne, 0.0, 1.0);
  v_texcoord = a_texcoord + a_unitquad * a_texsize;
}`

// single atlas page: one sampler instead of the upstream page array
const glyphFragmentShader = `#version 300 es
precision lowp float;

in vec2 v_texcoord;

uniform sampler2D u_texture;

out vec4 outColor;

void main() {
  outColor = texture(u_texture, v_texcoord);
}`

const rectVertexShader = `#version 300 es
layout (location = 0) in vec2 a_position;
layout (location = 1) in vec2 a_size;
layout (location = 2) in vec4 a_color;
layout (location = 3) in vec2 a_unitquad;

uniform mat4 u_projection;

out vec4 v_color;

void main() {
  vec2 zeroToOne = a_position + (a_unitquad * a_size);
  gl_Position = u_projection * vec4(zeroToOne, 0.0, 1.0);
  v_color = a_color;
}`

const rectFragmentShader = `#version 300 es
precision lowp float;

in vec4 v_color;

out vec4 outColor;

void main() {
  outColor = v_color;
}`

func f32bytes(s []float32) []byte {
	if len(s) == 0 {
		return nil
	}
	// #nosec G103 -- reinterpreting the float32 backing array as bytes is the
	// point: WebGL buffer uploads take raw bytes, and the slice header is
	// built from the same allocation with a length derived from it.
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(s))), len(s)*4)
}

// jsF32 uploads a Go float32 slice into a persistent JS Float32Array,
// returning a subarray view of the right length.
type jsF32 struct {
	arr js.Value
	cap int
}

func (b *jsF32) view(data []float32) js.Value {
	if b.cap < len(data) || b.arr.IsUndefined() {
		alloc := len(data)
		if alloc == 0 {
			// always hold a real array: a zero-length view must not
			// leave b.arr undefined (an empty first frame would panic)
			alloc = 1
		}
		b.arr = js.Global().Get("Float32Array").New(alloc)
		b.cap = alloc
	}
	if len(data) > 0 {
		u8 := js.Global().Get("Uint8Array").New(b.arr.Get("buffer"), 0, len(data)*4)
		js.CopyBytesToJS(u8, f32bytes(data))
	}
	return b.arr.Call("subarray", 0, len(data))
}

func createShader(gl js.Value, typ int, source string) (js.Value, error) {
	shader := gl.Call("createShader", typ)
	gl.Call("shaderSource", shader, source)
	gl.Call("compileShader", shader)
	if !gl.Call("getShaderParameter", shader, glCompileStatus).Bool() {
		log := gl.Call("getShaderInfoLog", shader).String()
		gl.Call("deleteShader", shader)
		return js.Value{}, errors.New("shader compile failed: " + log)
	}
	return shader, nil
}

func createProgram(gl js.Value, vertexSource, fragmentSource string) (js.Value, error) {
	vs, err := createShader(gl, glVertexShader, vertexSource)
	if err != nil {
		return js.Value{}, err
	}
	fs, err := createShader(gl, glFragmentShader, fragmentSource)
	if err != nil {
		return js.Value{}, err
	}
	program := gl.Call("createProgram")
	gl.Call("attachShader", program, vs)
	gl.Call("attachShader", program, fs)
	gl.Call("linkProgram", program)
	if !gl.Call("getProgramParameter", program, glLinkStatus).Bool() {
		log := gl.Call("getProgramInfoLog", program).String()
		gl.Call("deleteProgram", program)
		return js.Value{}, errors.New("program link failed: " + log)
	}
	return program, nil
}

// renderDimensions is the subset of IRenderDimensions used.
type renderDimensions struct {
	deviceCharWidth    int
	deviceCharHeight   int
	deviceCharLeft     int
	deviceCharTop      int
	deviceCellWidth    int
	deviceCellHeight   int
	deviceCanvasWidth  int
	deviceCanvasHeight int
	cssCanvasWidth     float64
	cssCanvasHeight    float64
}

// renderModel caches per-cell state to detect changes (RenderModel).
const (
	modelIndiciesPerCell = 4
	modelBgOffset        = 1
	modelFgOffset        = 2
	modelExtOffset       = 3
	combinedCharBitMask  = 0x80000000
)

type cursorRenderModel struct {
	x, y        int
	width       int
	style       string
	cursorWidth int
	dpr         float64
}

type renderModel struct {
	cells       []uint32
	lineLengths []int
	cursor      *cursorRenderModel
}

func (m *renderModel) resize(cols, rows int) {
	count := cols * rows * modelIndiciesPerCell
	if count != len(m.cells) {
		m.cells = make([]uint32, count)
		m.lineLengths = make([]int, rows)
	}
}

func (m *renderModel) clear() {
	for i := range m.cells {
		m.cells[i] = 0
	}
	for i := range m.lineLengths {
		m.lineLengths[i] = 0
	}
}

// glyphRenderer draws the glyph layer (GlyphRenderer).
const glyphIndicesPerCell = 11

type glyphRenderer struct {
	term *Terminal
	gl   js.Value
	dims *renderDimensions

	program        js.Value
	vao            js.Value
	projectionLoc  js.Value
	resolutionLoc  js.Value
	textureLoc     js.Value
	attributesBuf  js.Value
	atlasTexture   js.Value
	textureVersion int

	atlas      *textureAtlas
	attributes []float32
	upload     jsF32
	activeBuf  []float32
}

func newGlyphRenderer(term *Terminal, gl js.Value, dims *renderDimensions) (*glyphRenderer, error) {
	r := &glyphRenderer{term: term, gl: gl, dims: dims, textureVersion: -1}

	program, err := createProgram(gl, glyphVertexShader, glyphFragmentShader)
	if err != nil {
		return nil, err
	}
	r.program = program
	r.projectionLoc = gl.Call("getUniformLocation", program, "u_projection")
	r.resolutionLoc = gl.Call("getUniformLocation", program, "u_resolution")
	r.textureLoc = gl.Call("getUniformLocation", program, "u_texture")

	r.vao = gl.Call("createVertexArray")
	gl.Call("bindVertexArray", r.vao)

	// a_unitquad: the 4 vertices of a rectangle
	unitQuad := (&jsF32{}).view([]float32{0, 0, 1, 0, 0, 1, 1, 1})
	quadBuf := gl.Call("createBuffer")
	gl.Call("bindBuffer", glArrayBuffer, quadBuf)
	gl.Call("bufferData", glArrayBuffer, unitQuad, glStaticDraw)
	gl.Call("enableVertexAttribArray", 0)
	gl.Call("vertexAttribPointer", 0, 2, glFloat, false, 0, 0)

	// element indices for the triangle strip
	elem := js.Global().Get("Uint8Array").New(4)
	js.CopyBytesToJS(elem, []byte{0, 1, 2, 3})
	elemBuf := gl.Call("createBuffer")
	gl.Call("bindBuffer", glElementArrayBuffer, elemBuf)
	gl.Call("bufferData", glElementArrayBuffer, elem, glStaticDraw)

	// per-cell instanced attributes
	const bytesPerCell = glyphIndicesPerCell * 4
	r.attributesBuf = gl.Call("createBuffer")
	gl.Call("bindBuffer", glArrayBuffer, r.attributesBuf)
	attribs := []struct{ loc, size, offset int }{
		{2, 2, 0}, // a_offset
		{3, 2, 2}, // a_size
		{4, 1, 4}, // a_texpage
		{5, 2, 5}, // a_texcoord
		{6, 2, 7}, // a_texsize
		{1, 2, 9}, // a_cellpos
	}
	for _, a := range attribs {
		gl.Call("enableVertexAttribArray", a.loc)
		gl.Call("vertexAttribPointer", a.loc, a.size, glFloat, false, bytesPerCell, a.offset*4)
		gl.Call("vertexAttribDivisor", a.loc, 1)
	}

	gl.Call("useProgram", program)
	gl.Call("uniform1i", r.textureLoc, 0)
	gl.Call("uniformMatrix4fv", r.projectionLoc, false, (&jsF32{}).view(projectionMatrix))

	// 1x1 red placeholder texture (visible if an invalid page draws)
	r.atlasTexture = gl.Call("createTexture")
	gl.Call("activeTexture", glTexture0)
	gl.Call("bindTexture", glTexture2D, r.atlasTexture)
	gl.Call("texParameteri", glTexture2D, glTextureWrapS, glClampToEdge)
	gl.Call("texParameteri", glTexture2D, glTextureWrapT, glClampToEdge)
	gl.Call("texParameteri", glTexture2D, glTextureMinFilter, glLinear)
	red := js.Global().Get("Uint8Array").New(4)
	js.CopyBytesToJS(red, []byte{255, 0, 0, 255})
	gl.Call("texImage2D", glTexture2D, 0, glRGBA, 1, 1, 0, glRGBA, glUnsignedByte, red)

	// allow drawing of transparent texture
	gl.Call("enable", glBlend)
	gl.Call("blendFunc", glSrcAlpha, glOneMinusSrcAlpha)

	r.handleResize()
	return r, nil
}

func (r *glyphRenderer) beginFrame() bool {
	if r.atlas != nil {
		return r.atlas.beginFrame()
	}
	return true
}

func (r *glyphRenderer) setAtlas(atlas *textureAtlas) {
	r.atlas = atlas
	r.textureVersion = -1
}

func (r *glyphRenderer) updateCell(x, y int, code uint32, bg, fg, ext uint32, chars string, lastBg uint32) {
	i := (y*r.term.Core.Cols() + x) * glyphIndicesPerCell
	array := r.attributes

	// null cell: keep cellpos, zero the rest
	if code == vt.NullCellCode {
		for j := i; j < i+glyphIndicesPerCell-2; j++ {
			array[j] = 0
		}
		return
	}
	if r.atlas == nil {
		return
	}

	var glyph *rasterizedGlyph
	if len(chars) > 0 && len([]rune(chars)) > 1 {
		glyph = r.atlas.getRasterizedGlyphCombinedChar(chars, bg, fg, ext)
	} else {
		glyph = r.atlas.getRasterizedGlyph(code, bg, fg, ext)
	}

	leftCellPadding := float64((r.dims.deviceCellWidth - r.dims.deviceCharWidth) / 2)
	cw := float64(r.dims.deviceCanvasWidth)
	ch := float64(r.dims.deviceCanvasHeight)
	if bg != lastBg && glyph.offsetX > leftCellPadding {
		clipped := glyph.offsetX - leftCellPadding
		array[i] = float32(-(glyph.offsetX - clipped) + float64(r.dims.deviceCharLeft))
		array[i+1] = float32(-glyph.offsetY + float64(r.dims.deviceCharTop))
		array[i+2] = float32((glyph.sizeX - clipped) / cw)
		array[i+3] = float32(glyph.sizeY / ch)
		array[i+4] = 0 // texpage
		array[i+5] = float32(glyph.texClipX + clipped/float64(r.atlas.pageSize))
		array[i+6] = float32(glyph.texClipY)
		array[i+7] = float32(glyph.sizeClipX - clipped/float64(r.atlas.pageSize))
		array[i+8] = float32(glyph.sizeClipY)
	} else {
		array[i] = float32(-glyph.offsetX + float64(r.dims.deviceCharLeft))
		array[i+1] = float32(-glyph.offsetY + float64(r.dims.deviceCharTop))
		array[i+2] = float32(glyph.sizeX / cw)
		array[i+3] = float32(glyph.sizeY / ch)
		array[i+4] = 0
		array[i+5] = float32(glyph.texClipX)
		array[i+6] = float32(glyph.texClipY)
		array[i+7] = float32(glyph.sizeClipX)
		array[i+8] = float32(glyph.sizeClipY)
	}
}

func (r *glyphRenderer) clear() {
	term := r.term
	newCount := term.Core.Cols() * term.Core.Rows() * glyphIndicesPerCell
	if len(r.attributes) != newCount {
		r.attributes = make([]float32, newCount)
		r.activeBuf = make([]float32, newCount)
	} else {
		for i := range r.attributes {
			r.attributes[i] = 0
		}
	}
	i := 0
	cols := term.Core.Cols()
	rows := term.Core.Rows()
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			r.attributes[i+9] = float32(x) / float32(cols)
			r.attributes[i+10] = float32(y) / float32(rows)
			i += glyphIndicesPerCell
		}
	}
}

func (r *glyphRenderer) handleResize() {
	gl := r.gl
	gl.Call("useProgram", r.program)
	canvas := gl.Get("canvas")
	gl.Call("viewport", 0, 0, canvas.Get("width").Int(), canvas.Get("height").Int())
	gl.Call("uniform2f", r.resolutionLoc, float64(canvas.Get("width").Int()), float64(canvas.Get("height").Int()))
	r.clear()
}

func (r *glyphRenderer) render(model *renderModel) {
	if r.atlas == nil {
		return
	}
	gl := r.gl
	gl.Call("useProgram", r.program)
	gl.Call("bindVertexArray", r.vao)

	// pack only used cells (up to each line length) for the GPU
	cols := r.term.Core.Cols()
	bufferLength := 0
	for y := 0; y < len(model.lineLengths); y++ {
		si := y * cols * glyphIndicesPerCell
		n := model.lineLengths[y] * glyphIndicesPerCell
		copy(r.activeBuf[bufferLength:], r.attributes[si:si+n])
		bufferLength += n
	}

	if bufferLength == 0 {
		return // nothing to draw (empty viewport)
	}
	gl.Call("bindBuffer", glArrayBuffer, r.attributesBuf)
	gl.Call("bufferData", glArrayBuffer, r.upload.view(r.activeBuf[:bufferLength]), glStreamDraw)

	// upload the atlas texture if it changed
	if r.atlas.version != r.textureVersion {
		gl.Call("activeTexture", glTexture0)
		gl.Call("bindTexture", glTexture2D, r.atlasTexture)
		gl.Call("texParameteri", glTexture2D, glTextureWrapS, glClampToEdge)
		gl.Call("texParameteri", glTexture2D, glTextureWrapT, glClampToEdge)
		gl.Call("texParameteri", glTexture2D, glTextureMinFilter, glLinear)
		gl.Call("texImage2D", glTexture2D, 0, glRGBA, glRGBA, glUnsignedByte, r.atlas.canvas)
		r.textureVersion = r.atlas.version
	}

	gl.Call("drawElementsInstanced", glTriangleStrip, 4, glUnsignedByte, 0, bufferLength/glyphIndicesPerCell)
}

// rectangleRenderer draws background rectangles and the cursor
// (RectangleRenderer).
const rectIndices = 8

type rectangleRenderer struct {
	term *Terminal
	gl   js.Value
	dims *renderDimensions

	program       js.Value
	vao           js.Value
	attributesBuf js.Value
	projectionLoc js.Value

	bgAttrs      []float32
	bgCount      int
	cursorAttrs  []float32
	cursorCount  int
	upload       jsF32
	uploadCursor jsF32

	colors *ColorSet
}

func newRectangleRenderer(term *Terminal, gl js.Value, dims *renderDimensions, colors *ColorSet) (*rectangleRenderer, error) {
	r := &rectangleRenderer{term: term, gl: gl, dims: dims, colors: colors}
	program, err := createProgram(gl, rectVertexShader, rectFragmentShader)
	if err != nil {
		return nil, err
	}
	r.program = program
	r.projectionLoc = gl.Call("getUniformLocation", program, "u_projection")

	r.vao = gl.Call("createVertexArray")
	gl.Call("bindVertexArray", r.vao)

	unitQuad := (&jsF32{}).view([]float32{0, 0, 1, 0, 0, 1, 1, 1})
	quadBuf := gl.Call("createBuffer")
	gl.Call("bindBuffer", glArrayBuffer, quadBuf)
	gl.Call("bufferData", glArrayBuffer, unitQuad, glStaticDraw)
	gl.Call("enableVertexAttribArray", 3)
	gl.Call("vertexAttribPointer", 3, 2, glFloat, false, 0, 0)

	elem := js.Global().Get("Uint8Array").New(4)
	js.CopyBytesToJS(elem, []byte{0, 1, 2, 3})
	elemBuf := gl.Call("createBuffer")
	gl.Call("bindBuffer", glElementArrayBuffer, elemBuf)
	gl.Call("bufferData", glElementArrayBuffer, elem, glStaticDraw)

	const bytesPerRect = rectIndices * 4
	r.attributesBuf = gl.Call("createBuffer")
	gl.Call("bindBuffer", glArrayBuffer, r.attributesBuf)
	gl.Call("enableVertexAttribArray", 0)
	gl.Call("vertexAttribPointer", 0, 2, glFloat, false, bytesPerRect, 0)
	gl.Call("vertexAttribDivisor", 0, 1)
	gl.Call("enableVertexAttribArray", 1)
	gl.Call("vertexAttribPointer", 1, 2, glFloat, false, bytesPerRect, 2*4)
	gl.Call("vertexAttribDivisor", 1, 1)
	gl.Call("enableVertexAttribArray", 2)
	gl.Call("vertexAttribPointer", 2, 4, glFloat, false, bytesPerRect, 4*4)
	gl.Call("vertexAttribDivisor", 2, 1)

	r.bgAttrs = make([]float32, 20*rectIndices)
	r.cursorAttrs = make([]float32, 8*rectIndices)
	r.updateViewportRectangle()
	return r, nil
}

func rgbToFloats(rgb uint32) (float32, float32, float32) {
	return float32(rgb>>16&0xff) / 255, float32(rgb>>8&0xff) / 255, float32(rgb&0xff) / 255
}

func (r *rectangleRenderer) renderBackgrounds() { r.renderVertices(r.bgAttrs, r.bgCount, &r.upload) }
func (r *rectangleRenderer) renderCursor() {
	r.renderVertices(r.cursorAttrs, r.cursorCount, &r.uploadCursor)
}

func (r *rectangleRenderer) renderVertices(attrs []float32, count int, up *jsF32) {
	gl := r.gl
	gl.Call("useProgram", r.program)
	gl.Call("bindVertexArray", r.vao)
	gl.Call("uniformMatrix4fv", r.projectionLoc, false, up.view(projectionMatrix))
	gl.Call("bindBuffer", glArrayBuffer, r.attributesBuf)
	gl.Call("bufferData", glArrayBuffer, up.view(attrs), glDynamicDraw)
	gl.Call("drawElementsInstanced", glTriangleStrip, 4, glUnsignedByte, 0, count)
}

func (r *rectangleRenderer) handleResize() { r.updateViewportRectangle() }

// updateViewportRectangle sets the first rectangle that clears the
// whole screen to the background color.
func (r *rectangleRenderer) updateViewportRectangle() {
	bg := cssToRGB(r.colors.Background)
	br, bgc, bb := rgbToFloats(bg)
	r.addRectangle(r.bgAttrs, 0, 0, 0,
		float64(r.term.Core.Cols()*r.dims.deviceCellWidth),
		float64(r.term.Core.Rows()*r.dims.deviceCellHeight),
		br, bgc, bb)
}

func (r *rectangleRenderer) updateBackgrounds(model *renderModel) {
	term := r.term.Core
	cols := term.Cols()
	rows := term.Rows()

	rectangleCount := 1
	for y := 0; y < rows; y++ {
		currentStartX := -1
		var currentBg, currentFg uint32
		currentInverse := false
		for x := 0; x < cols; x++ {
			modelIndex := (y*cols + x) * modelIndiciesPerCell
			bg := model.cells[modelIndex+modelBgOffset]
			fg := model.cells[modelIndex+modelFgOffset]
			inverse := fg&vt.FgInverse != 0
			if bg != currentBg || (fg != currentFg && (currentInverse || inverse)) {
				// a rectangle is drawn when going from non-default to
				// another color
				if currentBg != 0 || (currentInverse && currentFg != 0) {
					offset := rectangleCount * rectIndices
					rectangleCount++
					r.growBg(offset + rectIndices)
					r.updateRectangle(r.bgAttrs, offset, currentFg, currentBg, currentStartX, x, y)
				}
				currentStartX = x
				currentBg = bg
				currentFg = fg
				currentInverse = inverse
			}
		}
		if currentBg != 0 || (currentInverse && currentFg != 0) {
			offset := rectangleCount * rectIndices
			rectangleCount++
			r.growBg(offset + rectIndices)
			r.updateRectangle(r.bgAttrs, offset, currentFg, currentBg, currentStartX, cols, y)
		}
	}
	r.bgCount = rectangleCount
}

func (r *rectangleRenderer) growBg(needed int) {
	for len(r.bgAttrs) < needed {
		r.bgAttrs = append(r.bgAttrs, make([]float32, len(r.bgAttrs))...)
	}
}

func (r *rectangleRenderer) updateCursor(model *renderModel) {
	cursor := model.cursor
	if cursor == nil || cursor.style == "block" {
		r.cursorCount = 0
		return
	}

	cursorRGB := cssToRGB(r.colors.Cursor)
	cr, cg, cb := rgbToFloats(cursorRGB)
	rectangleCount := 0
	cellW := float64(r.dims.deviceCellWidth)
	cellH := float64(r.dims.deviceCellHeight)

	if cursor.style == "bar" || cursor.style == "outline" {
		// left edge
		w := cursor.dpr
		if cursor.style == "bar" {
			w = cursor.dpr * float64(cursor.cursorWidth)
		}
		r.addRectangle(r.cursorAttrs, rectangleCount*rectIndices,
			float64(cursor.x)*cellW, float64(cursor.y)*cellH, w, cellH, cr, cg, cb)
		rectangleCount++
	}
	if cursor.style == "underline" || cursor.style == "outline" {
		// bottom edge
		r.addRectangle(r.cursorAttrs, rectangleCount*rectIndices,
			float64(cursor.x)*cellW, float64(cursor.y+1)*cellH-cursor.dpr,
			float64(cursor.width)*cellW, cursor.dpr, cr, cg, cb)
		rectangleCount++
	}
	if cursor.style == "outline" {
		// top edge
		r.addRectangle(r.cursorAttrs, rectangleCount*rectIndices,
			float64(cursor.x)*cellW, float64(cursor.y)*cellH,
			float64(cursor.width)*cellW, cursor.dpr, cr, cg, cb)
		rectangleCount++
		// right edge
		r.addRectangle(r.cursorAttrs, rectangleCount*rectIndices,
			float64(cursor.x+cursor.width)*cellW-cursor.dpr, float64(cursor.y)*cellH,
			cursor.dpr, cellH, cr, cg, cb)
		rectangleCount++
	}
	r.cursorCount = rectangleCount
}

func (r *rectangleRenderer) updateRectangle(attrs []float32, offset int, fg, bg uint32, startX, endX, y int) {
	var rgb uint32
	if fg&vt.FgInverse != 0 {
		switch fg & vt.AttrCMMask {
		case vt.AttrCMP16, vt.AttrCMP256:
			rgb = cssToRGB(r.colors.Ansi[fg&vt.AttrPColorMask])
		case vt.AttrCMRGB:
			rgb = fg & vt.AttrRGBMask
		default:
			rgb = cssToRGB(r.colors.Foreground)
		}
	} else {
		switch bg & vt.AttrCMMask {
		case vt.AttrCMP16, vt.AttrCMP256:
			rgb = cssToRGB(r.colors.Ansi[bg&vt.AttrPColorMask])
		case vt.AttrCMRGB:
			rgb = bg & vt.AttrRGBMask
		default:
			rgb = cssToRGB(r.colors.Background)
		}
	}
	x1 := float64(startX * r.dims.deviceCellWidth)
	y1 := float64(y * r.dims.deviceCellHeight)
	rr, rg, rb := rgbToFloats(rgb)
	r.addRectangle(attrs, offset, x1, y1,
		float64((endX-startX)*r.dims.deviceCellWidth), float64(r.dims.deviceCellHeight),
		rr, rg, rb)
}

func (r *rectangleRenderer) addRectangle(attrs []float32, offset int, x1, y1, width, height float64, cr, cg, cb float32) {
	cw := float64(r.dims.deviceCanvasWidth)
	ch := float64(r.dims.deviceCanvasHeight)
	if cw == 0 || ch == 0 {
		return
	}
	attrs[offset] = float32(x1 / cw)
	attrs[offset+1] = float32(y1 / ch)
	attrs[offset+2] = float32(width / cw)
	attrs[offset+3] = float32(height / ch)
	attrs[offset+4] = cr
	attrs[offset+5] = cg
	attrs[offset+6] = cb
	attrs[offset+7] = 1 // alpha: rectangles are always drawn opaque
}

// webglRenderer coordinates the model, atlas and both sub-renderers
// (WebglRenderer). It implements the terminal's renderer interface.
type webglRenderer struct {
	term   *Terminal
	canvas js.Value
	gl     js.Value

	dims   renderDimensions
	model  renderModel
	rects  *rectangleRenderer
	glyphs *glyphRenderer
	atlas  *textureAtlas

	// contextLost is set between webglcontextlost and webglcontextrestored.
	// Every GL call in that window is a no-op that the driver still has to
	// reject, and the picture it would produce is not on screen anyway.
	contextLost bool
	lossFns     []js.Func

	workCell *vt.CellData
}

func newWebglRenderer(term *Terminal) (r *webglRenderer, err error) {
	r = &webglRenderer{term: term, workCell: vt.NewCellData()}

	r.canvas = document.Call("createElement", "canvas")
	r.canvas.Set("className", "xterm-webgl-canvas")
	gl := r.canvas.Call("getContext", "webgl2", map[string]any{
		"antialias": false,
		"depth":     false,
	})
	if gl.IsNull() || gl.IsUndefined() {
		return nil, errors.New("WebGL2 not supported")
	}
	r.gl = gl

	r.updateDimensions()
	r.canvas.Get("style").Set("position", "absolute")
	r.canvas.Get("style").Set("left", "0")
	r.canvas.Get("style").Set("top", "0")
	term.screen.Call("insertBefore", r.canvas, term.screen.Get("firstChild"))

	r.rects, err = newRectangleRenderer(term, gl, &r.dims, term.colors)
	if err != nil {
		r.canvas.Call("remove")
		return nil, err
	}
	r.glyphs, err = newGlyphRenderer(term, gl, &r.dims)
	if err != nil {
		r.canvas.Call("remove")
		return nil, err
	}

	r.wireContextLoss()
	r.onResize()
	return r, nil
}

// wireContextLoss keeps the terminal alive across a lost GPU context.
//
// A browser caps how many WebGL contexts a page may hold — Chrome's limit is
// around sixteen — and when a new one is asked for past the cap it EVICTS THE
// OLDEST. A page that puts a terminal in a window and lets people open windows
// reaches that on its own: measured here at fifteen terminals, where the first
// one's context was taken away while the page went on running.
//
// preventDefault on the loss event is the load-bearing line. Without it the
// browser will never restore the context, and no amount of later work can bring
// the terminal back — it stays a rectangle that used to be a shell, with the
// renderer issuing draw calls into a dead context that silently do nothing.
//
// With it, the browser may restore, and then the GPU-side objects have to be
// built again: everything made from the context — programs, buffers, the glyph
// atlas — died with it, while the terminal's own state (its buffer, its shell)
// never depended on the GPU and is still exactly where it was.
func (r *webglRenderer) wireContextLoss() {
	lost := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("preventDefault")
		}
		r.contextLost = true
		return nil
	})
	restored := js.FuncOf(func(js.Value, []js.Value) any {
		r.contextLost = false
		r.rebuildGPUState()
		return nil
	})
	r.lossFns = append(r.lossFns, lost, restored)
	r.canvas.Call("addEventListener", "webglcontextlost", lost)
	r.canvas.Call("addEventListener", "webglcontextrestored", restored)
}

// rebuildGPUState remakes everything that lived in the lost context.
//
// The context object itself is reused by the browser and is valid again by the
// time this runs, so it is not fetched afresh; what has to be rebuilt is what
// was created FROM it. A failure here leaves contextLost set rather than
// half-built, so the terminal stays quiet instead of drawing garbage.
func (r *webglRenderer) rebuildGPUState() {
	if r.atlas != nil {
		r.atlas.dispose()
		r.atlas = nil
	}
	rects, err := newRectangleRenderer(r.term, r.gl, &r.dims, r.term.colors)
	if err != nil {
		r.contextLost = true
		return
	}
	glyphs, err := newGlyphRenderer(r.term, r.gl, &r.dims)
	if err != nil {
		r.contextLost = true
		return
	}
	r.rects, r.glyphs = rects, glyphs
	// onResize rebuilds the dimensions and the atlas and clears the model, and
	// a cleared atlas makes the next frame a full refresh — so one draw here
	// puts the whole terminal back rather than waiting for output that may
	// never come. A shell sitting at a prompt produces nothing on its own.
	r.onResize()
	r.renderRows(0, r.term.Core.Rows()-1)
}

// snapPx clears the floating-point error out of a device-pixel measurement
// before it is rounded to a whole pixel.
//
// devicePixelRatio is a float32 round-trip of the zoom level, so it is not
// the number it prints as: at 80% zoom the browser reports
// 0.800000011920929, whose relative error is 2^-26. A cell height that
// lands exactly on a device-pixel boundary therefore computes as that
// boundary plus a few parts in ten million — 21.25 * dpr is
// 17.00000025331974, not 17 — and jsCeil turns it into 18.
//
// One extra device pixel per cell is ~6% at this size, so the canvas comes
// out taller than the element Fit() sized the grid against and the last
// rows land outside the viewport: the terminal draws a footer nobody can
// see. Truncation has the mirror failure, dropping a whole pixel from a
// value that fell a hair short.
//
// The tolerance is relative because the error scales with the measurement,
// and 1e-6 sits far above the 1.5e-8 that dpr contributes while staying far
// below any sub-pixel difference a caller could mean.
func snapPx(v float64) float64 {
	r := jsRound(v)
	d := v - r
	if d < 0 {
		d = -d
	}
	scale := v
	if scale < 1 {
		scale = 1
	}
	if d < 1e-6*scale {
		return r
	}
	return v
}

func (r *webglRenderer) updateDimensions() {
	term := r.term
	dpr := window.Get("devicePixelRatio").Float()
	if dpr == 0 {
		dpr = 1
	}
	d := &r.dims
	d.deviceCharWidth = int(snapPx(term.cellW * dpr))
	d.deviceCharHeight = int(jsCeil(snapPx(term.cellH / maxF(term.Core.Options.LineHeight, 1) * dpr)))
	d.deviceCellHeight = int(float64(d.deviceCharHeight) * maxF(term.Core.Options.LineHeight, 1))
	d.deviceCharTop = 0
	if term.Core.Options.LineHeight != 1 {
		d.deviceCharTop = int(jsRound(float64(d.deviceCellHeight-d.deviceCharHeight) / 2))
	}
	d.deviceCellWidth = d.deviceCharWidth + int(jsRound(term.Core.Options.LetterSpacing))
	d.deviceCharLeft = int(term.Core.Options.LetterSpacing / 2)
	d.deviceCanvasHeight = term.Core.Rows() * d.deviceCellHeight
	d.deviceCanvasWidth = term.Core.Cols() * d.deviceCellWidth
	d.cssCanvasHeight = jsRound(float64(d.deviceCanvasHeight) / dpr)
	d.cssCanvasWidth = jsRound(float64(d.deviceCanvasWidth) / dpr)
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func (r *webglRenderer) refreshCharAtlas() {
	dpr := window.Get("devicePixelRatio").Float()
	if dpr == 0 {
		dpr = 1
	}
	cfg := atlasConfig{
		deviceCellWidth:  r.dims.deviceCellWidth,
		deviceCellHeight: r.dims.deviceCellHeight,
		deviceCharWidth:  r.dims.deviceCharWidth,
		deviceCharHeight: r.dims.deviceCharHeight,
		fontSize:         r.term.Core.Options.FontSize,
		fontFamily:       r.term.Core.Options.FontFamily,
		dpr:              dpr,
		lineHeight:       maxF(r.term.Core.Options.LineHeight, 1),
		colors:           r.term.colors,
		mirrorGlyph:      r.term.Core.Options.MirrorGlyph,
	}
	if r.atlas != nil {
		cfg.pageSize = r.atlas.pageSize
		r.atlas.dispose()
	}
	r.atlas = newTextureAtlas(cfg)
	r.glyphs.setAtlas(r.atlas)
}

// renderer interface

func (r *webglRenderer) onResize() {
	r.updateDimensions()
	r.model.resize(r.term.Core.Cols(), r.term.Core.Rows())

	r.canvas.Set("width", r.dims.deviceCanvasWidth)
	r.canvas.Set("height", r.dims.deviceCanvasHeight)
	r.canvas.Get("style").Set("width", jsPx(r.dims.cssCanvasWidth))
	r.canvas.Get("style").Set("height", jsPx(r.dims.cssCanvasHeight))

	r.rects.handleResize()
	r.glyphs.handleResize()
	r.refreshCharAtlas()
	r.model.clear()
}

func (r *webglRenderer) onColorsChanged() {
	r.refreshCharAtlas()
	r.model.clear()
	r.glyphs.clear()
	r.rects.updateViewportRectangle()
}

func (r *webglRenderer) renderRows(start, end int) {
	if r.contextLost {
		return // nothing drawn into a lost context ever reaches the screen
	}
	// the frame begins; a cleared atlas forces a full model refresh
	if r.glyphs.beginFrame() {
		r.model.clear()
		r.glyphs.clear()
		r.updateModel(0, r.term.Core.Rows()-1)
	} else {
		r.updateModel(start, end)
	}

	r.rects.renderBackgrounds()
	r.glyphs.render(&r.model)
	if r.term.blinkVisible {
		r.rects.renderCursor()
	}
}

func (r *webglRenderer) dispose() {
	if r.atlas != nil {
		r.atlas.dispose()
	}
	for _, fn := range r.lossFns {
		fn.Release()
	}
	r.lossFns = nil
	r.canvas.Call("remove")
}

func (r *webglRenderer) updateModel(start, end int) {
	term := r.term
	core := term.Core
	cols := core.Cols()
	rows := core.Rows()
	b := core.Buffer()
	cell := r.workCell

	start = clampInt(start, rows-1)
	end = clampInt(end, rows-1)

	cursorStyle := term.cursorStyle()
	cursorY := b.YBase + b.Y
	viewportRelativeCursorY := cursorY - b.YDisp
	cursorX := b.X
	if cursorX > cols-1 {
		cursorX = cols - 1
	}
	lastCursorX := -1
	isCursorVisible := !core.CoreService().IsCursorHidden && term.blinkVisible
	r.model.cursor = nil
	modelUpdated := false
	dpr := window.Get("devicePixelRatio").Float()
	if dpr == 0 {
		dpr = 1
	}

	cursorAccentRGB := cssToRGB(term.colors.CursorAccent)
	cursorRGB := cssToRGB(term.colors.Cursor)
	selected := term.sel.valid()

	var lastBg uint32
	for y := start; y <= end; y++ {
		row := y + b.YDisp
		if row >= b.Lines.Length() {
			continue
		}
		line := b.Lines.Get(row)
		r.model.lineLengths[y] = 0
		for x := 0; x < cols; x++ {
			if x >= line.Length {
				break
			}
			prevBg := lastBg
			line.LoadCell(x, cell)

			chars := cell.GetChars()
			code := uint32(cell.GetCode()) // #nosec G115 -- a cell code is a Unicode codepoint
			i := (y*cols + x) * modelIndiciesPerCell

			// resolve colors (no decoration overrides)
			resFg := cell.Fg
			resBg := cell.Bg
			// A selected cell is drawn in the selection's colors, which the
			// background rectangles and the glyphs then pick up on their own —
			// there is no separate selection layer to keep in step, and the
			// model's unchanged-cell check redraws exactly the cells whose
			// selected-ness changed.
			if selected && term.sel.contains(x, row) {
				resFg, resBg = term.selectionColors(resFg, resBg)
			}
			var resExt uint32
			if cell.Bg&vt.BgHasExtended != 0 && cell.Extended != nil {
				resExt = cell.Extended.Ext()
			}
			// dotted underline variant offset
			if code != vt.NullCellCode && cell.Extended != nil && cell.Extended.UnderlineStyle() == vt.UnderlineDotted {
				lineWidth := float64(int(core.Options.FontSize * dpr / 15))
				if lineWidth < 1 {
					lineWidth = 1
				}
				period := jsRound(lineWidth) * 2
				vo := float64(x*r.dims.deviceCellWidth) - period*float64(int(float64(x*r.dims.deviceCellWidth)/period))
				resExt &= ^vt.ExtVariantOffset
				resExt |= (uint32(vo) << 29) & vt.ExtVariantOffset
			}

			// cursor cell override
			if isCursorVisible && row == cursorY {
				if x == cursorX {
					r.model.cursor = &cursorRenderModel{
						x:           cursorX,
						y:           viewportRelativeCursorY,
						width:       cell.GetWidth(),
						style:       cursorStyle,
						cursorWidth: 1,
						dpr:         dpr,
					}
					lastCursorX = cursorX + cell.GetWidth() - 1
				}
				if x >= cursorX && x <= lastCursorX && cursorStyle == "block" {
					resFg = vt.AttrCMRGB | (cursorAccentRGB & vt.AttrRGBMask)
					resBg = vt.AttrCMRGB | (cursorRGB & vt.AttrRGBMask)
				}
			}

			if code != vt.NullCellCode {
				r.model.lineLengths[y] = x + 1
			}

			lastBg = resBg

			// skip unchanged cells
			storedCode := code
			if len([]rune(chars)) > 1 {
				storedCode |= combinedCharBitMask
			}
			if r.model.cells[i] == storedCode &&
				r.model.cells[i+modelBgOffset] == resBg &&
				r.model.cells[i+modelFgOffset] == resFg &&
				r.model.cells[i+modelExtOffset] == resExt {
				continue
			}
			modelUpdated = true

			r.model.cells[i] = storedCode
			r.model.cells[i+modelBgOffset] = resBg
			r.model.cells[i+modelFgOffset] = resFg
			r.model.cells[i+modelExtOffset] = resExt

			r.glyphs.updateCell(x, y, code, resBg, resFg, resExt, chars, prevBg)
		}
	}
	if modelUpdated {
		r.rects.updateBackgrounds(&r.model)
	}
	r.rects.updateCursor(&r.model)
}

// clampInt confines a value to 0..max. Every caller clamps an index into a
// row, column or line count, so the lower bound was always zero.
func clampInt(value, max int) int {
	if value > max {
		return max
	}
	if value < 0 {
		return 0
	}
	return value
}
