//go:build js && wasm

package cosmos

import (
	"fmt"
	"syscall/js"
	"unsafe"
)

// glCtx is a thin layer over a WebGL 1 context covering the subset of regl
// the original library uses: float textures, framebuffers, static buffers,
// draw commands with blend/depth/cull state and instancing.
type glCtx struct {
	gl        js.Value
	canvas    js.Value
	instanced js.Value // ANGLE_instanced_arrays extension

	maxTextureSize int
	maxPointSize   float64

	// program cache keyed by shader sources (setData re-inits programs the
	// way the original re-creates regl commands; caching avoids recompiles)
	programs map[string]*program

	// cached capability state
	blendOn bool
	depthOn bool
	cullOn  bool
}

// program returns a cached (or newly compiled) program for a shader pair.
func (c *glCtx) program(vertSrc, fragSrc string) (*program, error) {
	key := vertSrc + "\x00" + fragSrc
	if p, ok := c.programs[key]; ok {
		return p, nil
	}
	p, err := c.newProgram(vertSrc, fragSrc)
	if err != nil {
		return nil, err
	}
	c.programs[key] = p
	return p, nil
}

func consoleWarn(msg string) {
	js.Global().Get("console").Call("warn", msg)
}

func f32ToJS(data []float32) js.Value {
	arr := js.Global().Get("Float32Array").New(len(data))
	if len(data) > 0 {
		// #nosec G103 -- viewing the float32 backing array as bytes is the point:
		// js.CopyBytesToJS takes a byte slice, and the length is derived from
		// the same allocation.
		bytes := unsafe.Slice((*byte)(unsafe.Pointer(&data[0])), len(data)*4)
		u8 := js.Global().Get("Uint8Array").New(arr.Get("buffer"), arr.Get("byteOffset"), len(data)*4)
		js.CopyBytesToJS(u8, bytes)
	}
	return arr
}

func f32FromJS(arr js.Value) []float32 {
	n := arr.Get("length").Int()
	out := make([]float32, n)
	if n > 0 {
		u8 := js.Global().Get("Uint8Array").New(arr.Get("buffer"), arr.Get("byteOffset"), n*4)
		// #nosec G103 -- see f32ToJS; the byte view is how the copy back is made.
		bytes := unsafe.Slice((*byte)(unsafe.Pointer(&out[0])), n*4)
		js.CopyBytesToGo(bytes, u8)
	}
	return out
}

func newGLCtx(canvas js.Value) (*glCtx, error) {
	attrs := js.Global().Get("Object").New()
	attrs.Set("antialias", false)
	attrs.Set("preserveDrawingBuffer", true)
	gl := canvas.Call("getContext", "webgl", attrs)
	if !gl.Truthy() {
		gl = canvas.Call("getContext", "experimental-webgl", attrs)
	}
	if !gl.Truthy() {
		return nil, fmt.Errorf("cosmos: WebGL is not supported")
	}
	if !gl.Call("getExtension", "OES_texture_float").Truthy() {
		return nil, fmt.Errorf("cosmos: required WebGL extension OES_texture_float is not supported")
	}
	inst := gl.Call("getExtension", "ANGLE_instanced_arrays")
	if !inst.Truthy() {
		return nil, fmt.Errorf("cosmos: required WebGL extension ANGLE_instanced_arrays is not supported")
	}

	c := &glCtx{gl: gl, canvas: canvas, instanced: inst, programs: map[string]*program{}}
	c.maxTextureSize = gl.Call("getParameter", gl.Get("MAX_TEXTURE_SIZE")).Int()
	pointSizeRange := gl.Call("getParameter", gl.Get("ALIASED_POINT_SIZE_RANGE"))
	c.maxPointSize = pointSizeRange.Index(1).Float()

	gl.Call("disable", gl.Get("BLEND"))
	gl.Call("disable", gl.Get("DEPTH_TEST"))
	gl.Call("disable", gl.Get("CULL_FACE"))
	gl.Call("disable", gl.Get("STENCIL_TEST"))
	gl.Call("disable", gl.Get("SCISSOR_TEST"))
	return c, nil
}

// ---------------------------------------------------------------- textures

type texture struct {
	ctx    *glCtx
	tex    js.Value
	w, h   int
	exists bool
}

// newFloatTexture creates a w×h RGBA float texture; data may be nil.
func (c *glCtx) newFloatTexture(w, h int, data []float32) *texture {
	gl := c.gl
	tex := gl.Call("createTexture")
	gl.Call("bindTexture", gl.Get("TEXTURE_2D"), tex)
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_MIN_FILTER"), gl.Get("NEAREST"))
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_MAG_FILTER"), gl.Get("NEAREST"))
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_WRAP_S"), gl.Get("CLAMP_TO_EDGE"))
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_WRAP_T"), gl.Get("CLAMP_TO_EDGE"))
	jsData := js.Null()
	if data != nil {
		jsData = f32ToJS(data)
	}
	gl.Call("texImage2D", gl.Get("TEXTURE_2D"), 0, gl.Get("RGBA"), w, h, 0, gl.Get("RGBA"), gl.Get("FLOAT"), jsData)
	return &texture{ctx: c, tex: tex, w: w, h: h, exists: true}
}

// newUint8Texture creates a w×h RGBA uint8 texture (used for the image
// atlas).
func (c *glCtx) newUint8Texture(w, h int, data []byte) *texture {
	gl := c.gl
	tex := gl.Call("createTexture")
	gl.Call("bindTexture", gl.Get("TEXTURE_2D"), tex)
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_MIN_FILTER"), gl.Get("LINEAR"))
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_MAG_FILTER"), gl.Get("LINEAR"))
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_WRAP_S"), gl.Get("CLAMP_TO_EDGE"))
	gl.Call("texParameteri", gl.Get("TEXTURE_2D"), gl.Get("TEXTURE_WRAP_T"), gl.Get("CLAMP_TO_EDGE"))
	jsData := js.Null()
	if data != nil {
		arr := js.Global().Get("Uint8Array").New(len(data))
		js.CopyBytesToJS(arr, data)
		jsData = arr
	}
	gl.Call("texImage2D", gl.Get("TEXTURE_2D"), 0, gl.Get("RGBA"), w, h, 0, gl.Get("RGBA"), gl.Get("UNSIGNED_BYTE"), jsData)
	return &texture{ctx: c, tex: tex, w: w, h: h, exists: true}
}

func (t *texture) destroy() {
	if t == nil || !t.exists {
		return
	}
	t.ctx.gl.Call("deleteTexture", t.tex)
	t.exists = false
}

// ---------------------------------------------------------------- framebuffers

type framebuffer struct {
	ctx    *glCtx
	fb     js.Value
	tex    *texture
	w, h   int
	exists bool
}

// newFramebuffer creates a float-color framebuffer of size w×h with the
// given initial data (nil for zeroes).
func (c *glCtx) newFramebuffer(w, h int, data []float32) *framebuffer {
	gl := c.gl
	tex := c.newFloatTexture(w, h, data)
	fb := gl.Call("createFramebuffer")
	gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), fb)
	gl.Call("framebufferTexture2D", gl.Get("FRAMEBUFFER"), gl.Get("COLOR_ATTACHMENT0"), gl.Get("TEXTURE_2D"), tex.tex, 0)
	gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), js.Null())
	return &framebuffer{ctx: c, fb: fb, tex: tex, w: w, h: h, exists: true}
}

func (f *framebuffer) destroy() {
	if f == nil || !f.exists {
		return
	}
	f.ctx.gl.Call("deleteFramebuffer", f.fb)
	f.tex.destroy()
	f.exists = false
}

// readPixels reads the full framebuffer as float RGBA values.
func (f *framebuffer) readPixels() []float32 {
	gl := f.ctx.gl
	gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), f.fb)
	out := js.Global().Get("Float32Array").New(f.w * f.h * 4)
	gl.Call("readPixels", 0, 0, f.w, f.h, gl.Get("RGBA"), gl.Get("FLOAT"), out)
	gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), js.Null())
	return f32FromJS(out)
}

// ---------------------------------------------------------------- buffers

type buffer struct {
	ctx    *glCtx
	buf    js.Value
	exists bool
}

func (c *glCtx) newBuffer(data []float32) *buffer {
	gl := c.gl
	buf := gl.Call("createBuffer")
	gl.Call("bindBuffer", gl.Get("ARRAY_BUFFER"), buf)
	gl.Call("bufferData", gl.Get("ARRAY_BUFFER"), f32ToJS(data), gl.Get("STATIC_DRAW"))
	return &buffer{ctx: c, buf: buf, exists: true}
}

func (b *buffer) destroy() {
	if b == nil || !b.exists {
		return
	}
	b.ctx.gl.Call("deleteBuffer", b.buf)
	b.exists = false
}

// ---------------------------------------------------------------- programs

type program struct {
	ctx  *glCtx
	prog js.Value
	// cached locations
	attribLocs  map[string]int
	uniformLocs map[string]js.Value
}

func (c *glCtx) compileShader(kind js.Value, src string) (js.Value, error) {
	gl := c.gl
	sh := gl.Call("createShader", kind)
	gl.Call("shaderSource", sh, src)
	gl.Call("compileShader", sh)
	if !gl.Call("getShaderParameter", sh, gl.Get("COMPILE_STATUS")).Bool() {
		log := gl.Call("getShaderInfoLog", sh).String()
		return js.Value{}, fmt.Errorf("cosmos: shader compile error: %s", log)
	}
	return sh, nil
}

func (c *glCtx) newProgram(vertSrc, fragSrc string) (*program, error) {
	gl := c.gl
	vert, err := c.compileShader(gl.Get("VERTEX_SHADER"), vertSrc)
	if err != nil {
		return nil, err
	}
	frag, err := c.compileShader(gl.Get("FRAGMENT_SHADER"), fragSrc)
	if err != nil {
		return nil, err
	}
	prog := gl.Call("createProgram")
	gl.Call("attachShader", prog, vert)
	gl.Call("attachShader", prog, frag)
	gl.Call("linkProgram", prog)
	if !gl.Call("getProgramParameter", prog, gl.Get("LINK_STATUS")).Bool() {
		log := gl.Call("getProgramInfoLog", prog).String()
		return nil, fmt.Errorf("cosmos: program link error: %s", log)
	}
	gl.Call("deleteShader", vert)
	gl.Call("deleteShader", frag)
	return &program{
		ctx:         c,
		prog:        prog,
		attribLocs:  map[string]int{},
		uniformLocs: map[string]js.Value{},
	}, nil
}

func (p *program) attribLoc(name string) int {
	if loc, ok := p.attribLocs[name]; ok {
		return loc
	}
	loc := p.ctx.gl.Call("getAttribLocation", p.prog, name).Int()
	p.attribLocs[name] = loc
	return loc
}

func (p *program) uniformLoc(name string) js.Value {
	if loc, ok := p.uniformLocs[name]; ok {
		return loc
	}
	loc := p.ctx.gl.Call("getUniformLocation", p.prog, name)
	p.uniformLocs[name] = loc
	return loc
}

// ---------------------------------------------------------------- commands

// attrBinding describes one vertex attribute. Stride and offset are in
// bytes; size is the number of float components. A nil bufferFn buffer is
// skipped (matches regl behavior with missing buffers).
type attrBinding struct {
	name    string
	buffer  func() *buffer
	size    int
	stride  int
	offset  int
	divisor int
}

// uniformValue is the value of a uniform at draw time.
//
// Supported kinds: float64 (float), int (int), bool (bool),
// []float64 of len 2/4 (vec2/vec4), []float32 of len 9 (mat3),
// *framebuffer / *texture (sampler2D).
type uniformValue interface{}

type command struct {
	ctx       *glCtx
	prog      *program
	fbo       func() *framebuffer // nil → draw to canvas
	primitive string              // "points" or "triangle strip"
	count     func() int
	instances func() int // nil → non-instanced
	attrs     []attrBinding
	uniforms  map[string]func() uniformValue
	blend     string // "", "alpha" (premultiplied-style src alpha blend) or "add" (one/one)
	depthOff  bool
	cullBack  bool
}

func (c *glCtx) primitiveEnum(p string) js.Value {
	if p == "points" {
		return c.gl.Get("POINTS")
	}
	return c.gl.Get("TRIANGLE_STRIP")
}

func (cmd *command) run() {
	c := cmd.ctx
	gl := c.gl

	// target framebuffer & viewport
	var fb *framebuffer
	if cmd.fbo != nil {
		fb = cmd.fbo()
	}
	if fb != nil {
		gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), fb.fb)
		gl.Call("viewport", 0, 0, fb.w, fb.h)
	} else {
		gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), js.Null())
		gl.Call("viewport", 0, 0, gl.Get("drawingBufferWidth").Int(), gl.Get("drawingBufferHeight").Int())
	}

	gl.Call("useProgram", cmd.prog.prog)

	// blend state
	switch cmd.blend {
	case "alpha":
		if !c.blendOn {
			gl.Call("enable", gl.Get("BLEND"))
			c.blendOn = true
		}
		gl.Call("blendFuncSeparate", gl.Get("SRC_ALPHA"), gl.Get("ONE_MINUS_SRC_ALPHA"), gl.Get("ONE"), gl.Get("ONE_MINUS_SRC_ALPHA"))
		gl.Call("blendEquation", gl.Get("FUNC_ADD"))
	case "add":
		if !c.blendOn {
			gl.Call("enable", gl.Get("BLEND"))
			c.blendOn = true
		}
		gl.Call("blendFunc", gl.Get("ONE"), gl.Get("ONE"))
		gl.Call("blendEquation", gl.Get("FUNC_ADD"))
	default:
		if c.blendOn {
			gl.Call("disable", gl.Get("BLEND"))
			c.blendOn = false
		}
	}

	// depth state: none of the render targets carry depth buffers except the
	// canvas, and every canvas draw disables depth — a global disable matches
	// the original's effective behavior
	if c.depthOn {
		gl.Call("disable", gl.Get("DEPTH_TEST"))
		c.depthOn = false
	}

	// cull state
	if cmd.cullBack {
		if !c.cullOn {
			gl.Call("enable", gl.Get("CULL_FACE"))
			c.cullOn = true
		}
		gl.Call("cullFace", gl.Get("BACK"))
	} else if c.cullOn {
		gl.Call("disable", gl.Get("CULL_FACE"))
		c.cullOn = false
	}

	// attributes
	enabled := make([]int, 0, len(cmd.attrs))
	divisors := make([]int, 0, len(cmd.attrs))
	for i := range cmd.attrs {
		a := &cmd.attrs[i]
		buf := a.buffer()
		if buf == nil || !buf.exists {
			continue
		}
		loc := cmd.prog.attribLoc(a.name)
		if loc < 0 {
			continue
		}
		gl.Call("bindBuffer", gl.Get("ARRAY_BUFFER"), buf.buf)
		gl.Call("enableVertexAttribArray", loc)
		gl.Call("vertexAttribPointer", loc, a.size, gl.Get("FLOAT"), false, a.stride, a.offset)
		if a.divisor != 0 {
			c.instanced.Call("vertexAttribDivisorANGLE", loc, a.divisor)
			divisors = append(divisors, loc)
		}
		enabled = append(enabled, loc)
	}

	// uniforms (textures get sequential units)
	texUnit := 0
	for name, get := range cmd.uniforms {
		loc := cmd.prog.uniformLoc(name)
		if !loc.Truthy() {
			continue
		}
		switch v := get().(type) {
		case float64:
			gl.Call("uniform1f", loc, v)
		case int:
			gl.Call("uniform1i", loc, v)
		case bool:
			b := 0
			if v {
				b = 1
			}
			gl.Call("uniform1i", loc, b)
		case []float64:
			switch len(v) {
			case 2:
				gl.Call("uniform2f", loc, v[0], v[1])
			case 3:
				gl.Call("uniform3f", loc, v[0], v[1], v[2])
			case 4:
				gl.Call("uniform4f", loc, v[0], v[1], v[2], v[3])
			}
		case []float32: // mat3
			if len(v) == 9 {
				gl.Call("uniformMatrix3fv", loc, false, f32ToJS(v))
			}
		case *framebuffer:
			if v == nil || !v.exists {
				continue
			}
			gl.Call("activeTexture", gl.Get("TEXTURE0").Int()+texUnit)
			gl.Call("bindTexture", gl.Get("TEXTURE_2D"), v.tex.tex)
			gl.Call("uniform1i", loc, texUnit)
			texUnit++
		case *texture:
			if v == nil || !v.exists {
				continue
			}
			gl.Call("activeTexture", gl.Get("TEXTURE0").Int()+texUnit)
			gl.Call("bindTexture", gl.Get("TEXTURE_2D"), v.tex)
			gl.Call("uniform1i", loc, texUnit)
			texUnit++
		case nil:
			// skip
		}
	}

	// draw
	count := cmd.count()
	if count > 0 {
		prim := c.primitiveEnum(cmd.primitive)
		if cmd.instances != nil {
			n := cmd.instances()
			if n > 0 {
				c.instanced.Call("drawArraysInstancedANGLE", prim, 0, count, n)
			}
		} else {
			gl.Call("drawArrays", prim, 0, count)
		}
	}

	// reset attribute state so subsequent commands start clean
	for _, loc := range divisors {
		c.instanced.Call("vertexAttribDivisorANGLE", loc, 0)
	}
	for _, loc := range enabled {
		gl.Call("disableVertexAttribArray", loc)
	}
}

// clearTarget clears a framebuffer (or the canvas when fb is nil).
func (c *glCtx) clearTarget(fb *framebuffer, r, g, b, a float64) {
	gl := c.gl
	if fb != nil {
		gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), fb.fb)
		gl.Call("viewport", 0, 0, fb.w, fb.h)
	} else {
		gl.Call("bindFramebuffer", gl.Get("FRAMEBUFFER"), js.Null())
		gl.Call("viewport", 0, 0, gl.Get("drawingBufferWidth").Int(), gl.Get("drawingBufferHeight").Int())
	}
	gl.Call("clearColor", r, g, b, a)
	gl.Call("clearDepth", 1)
	gl.Call("clearStencil", 0)
	gl.Call("clear", gl.Get("COLOR_BUFFER_BIT").Int()|gl.Get("DEPTH_BUFFER_BIT").Int()|gl.Get("STENCIL_BUFFER_BIT").Int())
}
