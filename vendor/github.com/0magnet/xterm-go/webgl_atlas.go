//go:build js && wasm

package xterm

// Port of addons/addon-webgl/src/TextureAtlas.ts — rasterizes glyphs
// (with a 2D canvas) into an atlas texture the glyph renderer samples.
// Divergence from upstream: a single fixed-size 2048x2048 page is used
// instead of the multi-page grow/merge machinery; on overflow the
// atlas is cleared and glyphs re-rasterize lazily (upstream merges the
// 4 most-used pages instead). One page holds thousands of glyphs, far
// beyond what a session typically accumulates.

import (
	"strings"
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// atlasDebug exposes glyph placements on window.__atlasDebug.
const atlasDebug = false

const (
	// atlasPageSize is where a page starts. A terminal showing text needs a
	// fraction of it, so it is not worth allocating more up front; a page
	// that keeps overflowing grows itself, up to atlasPageSizeMax.
	atlasPageSize    = 2048
	atlasPageSizeMax = 4096
	// atlasGrowAfter is how many overflows in a row justify a bigger page.
	// One is ordinary -- the screen changed. Repeated ones mean the working
	// set does not fit, and every one of them costs a full re-render.
	atlasGrowAfter        = 2
	tmpCanvasGlyphPadding = 2
	dimOpacity            = 0.5
	// The amount of pixel padding allowed in each row before a new row
	// is started for a shorter glyph.
	rowPixelThreshold = 2
)

type atlasConfig struct {
	deviceCellWidth  int
	deviceCellHeight int
	deviceCharWidth  int
	deviceCharHeight int
	fontSize         float64
	fontFamily       string
	dpr              float64
	lineHeight       float64
	colors           *ColorSet
	// mirrorGlyph reports whether a glyph is drawn flipped left-to-right.
	// See Options.MirrorGlyph.
	mirrorGlyph func(string) bool
	// pageSize seeds the atlas page. A rebuilt atlas keeps whatever size
	// the last one had grown to: the font or the palette changing says
	// nothing about how many distinct glyphs the content needs.
	pageSize int
}

type rasterizedGlyph struct {
	// pixel position/size in the atlas texture
	texPosX, texPosY float64
	sizeX, sizeY     float64
	// clip space (0-1) position/size
	texClipX, texClipY   float64
	sizeClipX, sizeClipY float64
	// offset from the cell origin the glyph draws at
	offsetX, offsetY float64
}

var nullRasterizedGlyph = &rasterizedGlyph{}

type glyphKey struct {
	chars       string
	bg, fg, ext uint32
}

type atlasRow struct {
	x, y, height int
}

type textureAtlas struct {
	cfg atlasConfig

	// pageSize is the current page dimension, grown on repeated overflow.
	pageSize int
	// overflows counts consecutive fills since the last growth.
	overflows int

	canvas js.Value // the atlas page
	ctx    js.Value
	// version increments whenever the page content changes so the
	// renderer knows to re-upload the texture
	version int

	tmpCanvas js.Value
	tmpCtx    js.Value

	cache map[glyphKey]*rasterizedGlyph

	currentRow atlasRow
	fixedRows  []*atlasRow

	// requestClearModel asks the renderer to refresh the full model
	// (set after the atlas was cleared due to overflow)
	requestClearModel bool

	// bounding box of the last rasterized glyph (handoff between
	// findGlyphBoundingBox and the putImageData blit)
	boundLeft, boundTop float64

	workAttr *vt.AttributeData
}

func newTextureAtlas(cfg atlasConfig) *textureAtlas {
	a := &textureAtlas{
		cfg:      cfg,
		cache:    map[glyphKey]*rasterizedGlyph{},
		workAttr: vt.NewAttributeData(),
		pageSize: cfg.pageSize,
	}
	if a.pageSize < atlasPageSize {
		a.pageSize = atlasPageSize
	}
	if a.pageSize > atlasPageSizeMax {
		a.pageSize = atlasPageSizeMax
	}
	a.canvas = document.Call("createElement", "canvas")
	a.canvas.Set("width", a.pageSize)
	a.canvas.Set("height", a.pageSize)
	a.ctx = a.canvas.Call("getContext", "2d", map[string]any{"alpha": true})

	a.tmpCanvas = document.Call("createElement", "canvas")
	a.tmpCanvas.Set("width", cfg.deviceCellWidth*4+tmpCanvasGlyphPadding*2)
	a.tmpCanvas.Set("height", cfg.deviceCellHeight+tmpCanvasGlyphPadding*2)
	a.tmpCtx = a.tmpCanvas.Call("getContext", "2d", map[string]any{"alpha": false, "willReadFrequently": true})
	return a
}

func (a *textureAtlas) dispose() {
	a.tmpCanvas.Call("remove")
	a.canvas.Call("remove")
}

// beginFrame reports whether the model must be cleared (atlas was
// rebuilt).
func (a *textureAtlas) beginFrame() bool {
	r := a.requestClearModel
	a.requestClearModel = false
	return r
}

// growPage doubles the atlas page once a run of overflows shows the working
// set does not fit. Each overflow throws the whole cache away and forces
// every cell on screen to be rasterised again, so a workload that keeps
// filling the page pays that repeatedly — a full-screen truecolor image
// drawn in half-blocks hands the cache a distinct glyph per color pair and
// can turn over the entire page every frame.
//
// Growing is deliberately reluctant: a page starts small because ordinary
// terminal text needs a fraction of it, and one overflow only means the
// screen changed. Resizing the canvas clears it, and the renderer re-uploads
// the texture from the canvas itself, so it picks the new size up on the
// next version bump with nothing else to tell.
func (a *textureAtlas) growPage() {
	a.overflows++
	if a.overflows < atlasGrowAfter || a.pageSize >= atlasPageSizeMax {
		return
	}
	a.pageSize *= 2
	a.overflows = 0
	a.canvas.Set("width", a.pageSize)
	a.canvas.Set("height", a.pageSize)
	// Setting either dimension resets the canvas, so everything placed on
	// the old page is gone. Do the whole reset here rather than leaving it
	// to clearTexture: that returns early when the rows are already empty,
	// which is exactly the state this leaves behind, and the version bump
	// is what tells the renderer to re-upload at the new size.
	a.currentRow = atlasRow{}
	a.fixedRows = nil
	a.cache = map[glyphKey]*rasterizedGlyph{}
	a.version++
}
func (a *textureAtlas) clearTexture() {
	if a.currentRow.x == 0 && a.currentRow.y == 0 && len(a.fixedRows) == 0 {
		return
	}
	a.ctx.Call("clearRect", 0, 0, a.pageSize, a.pageSize)
	a.currentRow = atlasRow{}
	a.fixedRows = nil
	a.cache = map[glyphKey]*rasterizedGlyph{}
	a.version++
}

func (a *textureAtlas) getRasterizedGlyph(code uint32, bg, fg, ext uint32) *rasterizedGlyph {
	key := glyphKey{chars: string(rune(code)), bg: bg, fg: fg, ext: ext} // #nosec G115 -- a glyph code is a Unicode codepoint
	if g, ok := a.cache[key]; ok {
		return g
	}
	g := a.drawToCache(key.chars, code, bg, fg, ext)
	a.cache[key] = g
	return g
}

func (a *textureAtlas) getRasterizedGlyphCombinedChar(chars string, bg, fg, ext uint32) *rasterizedGlyph {
	key := glyphKey{chars: chars, bg: bg, fg: fg, ext: ext}
	if g, ok := a.cache[key]; ok {
		return g
	}
	g := a.drawToCache(chars, 0, bg, fg, ext)
	a.cache[key] = g
	return g
}

func (a *textureAtlas) ansiColor(idx int) (css string, rgb uint32) {
	css = a.cfg.colors.Ansi[idx&0xff]
	return css, cssToRGB(css)
}

// backgroundColor resolves the glyph background (always opaque; the
// transparency path of upstream is not ported).
func (a *textureAtlas) backgroundColor(bgColorMode uint32, bgColor int, inverse bool) (css string, rgb uint32) {
	switch bgColorMode {
	case vt.AttrCMP16, vt.AttrCMP256:
		return a.ansiColor(bgColor)
	case vt.AttrCMRGB:
		arr := vt.ToColorRGB(uint32(bgColor))                                           // #nosec G115 -- 24-bit RGB channels and palette indices
		return rgbCSS([3]int{arr[0], arr[1], arr[2]}), uint32(bgColor) & vt.AttrRGBMask // #nosec G115 -- 24-bit RGB channels and palette indices
	default:
		if inverse {
			return a.cfg.colors.Foreground, cssToRGB(a.cfg.colors.Foreground)
		}
		return a.cfg.colors.Background, cssToRGB(a.cfg.colors.Background)
	}
}

func (a *textureAtlas) foregroundColor(fgColorMode uint32, fgColor int, inverse, dim, bold bool) (css string, rgb uint32) {
	switch fgColorMode {
	case vt.AttrCMP16, vt.AttrCMP256:
		// drawBoldTextInBrightColors (always on, the xterm.js default)
		if bold && fgColor < 8 && fgColorMode == vt.AttrCMP16 {
			fgColor += 8
		}
		css, rgb = a.ansiColor(fgColor)
	case vt.AttrCMRGB:
		arr := vt.ToColorRGB(uint32(fgColor))                                             // #nosec G115 -- 24-bit RGB channels and palette indices
		css, rgb = rgbCSS([3]int{arr[0], arr[1], arr[2]}), uint32(fgColor)&vt.AttrRGBMask // #nosec G115 -- 24-bit RGB channels and palette indices
	default:
		if inverse {
			css, rgb = a.cfg.colors.Background, cssToRGB(a.cfg.colors.Background)
		} else {
			css, rgb = a.cfg.colors.Foreground, cssToRGB(a.cfg.colors.Foreground)
		}
	}
	if dim {
		// apply dim via opacity on the foreground color
		arr := [3]int{int(rgb >> 16 & 0xff), int(rgb >> 8 & 0xff), int(rgb & 0xff)}
		css = "rgba(" + itoaInt(arr[0]) + "," + itoaInt(arr[1]) + "," + itoaInt(arr[2]) + ",0.5)"
	}
	return css, rgb
}

func isPowerlineGlyph(codepoint uint32) bool {
	return 0xE0A4 <= codepoint && codepoint <= 0xE0D6
}

func isRestrictedPowerlineGlyph(codepoint uint32) bool {
	return 0xE0B0 <= codepoint && codepoint <= 0xE0B7
}

func computeNextVariantOffset(cellWidth, lineWidth float64, currentOffset float64) float64 {
	period := jsRound(lineWidth) * 2
	v := cellWidth - (jsRound(lineWidth)*2 - currentOffset)
	m := v - period*float64(int(v/period))
	if m < 0 {
		m += period
	}
	return m
}

func (a *textureAtlas) drawToCache(chars string, code uint32, bg, fg, ext uint32) *rasterizedGlyph {
	cfg := &a.cfg
	if code == 0 && chars != "" {
		code = uint32([]rune(chars)[0])
	}

	// grow the temp canvas for wide ligature-like content
	charCount := len([]rune(chars))
	if charCount < 2 {
		charCount = 2
	}
	allowedWidth := cfg.deviceCellWidth*charCount + tmpCanvasGlyphPadding*2
	if allowedWidth > a.pageSize {
		allowedWidth = a.pageSize
	}
	if a.tmpCanvas.Get("width").Int() < allowedWidth {
		a.tmpCanvas.Set("width", allowedWidth)
	}
	allowedHeight := cfg.deviceCellHeight + tmpCanvasGlyphPadding*4
	if a.tmpCanvas.Get("height").Int() < allowedHeight {
		a.tmpCanvas.Set("height", allowedHeight)
	}
	ctx := a.tmpCtx
	ctx.Call("save")

	attr := a.workAttr
	attr.Fg = fg
	attr.Bg = bg
	attr.Extended.SetExt(ext)

	if attr.IsInvisible() {
		ctx.Call("restore")
		return nullRasterizedGlyph
	}

	bold := attr.IsBold()
	inverse := attr.IsInverse()
	dim := attr.IsDim()
	italic := attr.IsItalic()
	underline := attr.IsUnderline()
	strikethrough := attr.IsStrikethrough()
	overline := attr.IsOverline()

	// an undecorated space never produces pixels — skip rasterization
	// (cell backgrounds are drawn by the rectangle renderer); this also
	// avoids canvas readback rounding dust surviving the background
	// clear when fg == bg (e.g. spaces on a white palette background)
	if (chars == " " || chars == " ") && !underline && !strikethrough && !overline {
		ctx.Call("restore")
		return nullRasterizedGlyph
	}
	fgColor := attr.GetFgColor()
	fgColorMode := attr.GetFgColorMode()
	bgColor := attr.GetBgColor()
	bgColorMode := attr.GetBgColorMode()
	if inverse {
		fgColor, bgColor = bgColor, fgColor
		fgColorMode, bgColorMode = bgColorMode, fgColorMode
	}

	// draw the background
	bgCSS, bgRGB := a.backgroundColor(bgColorMode, bgColor, inverse)
	ctx.Set("globalCompositeOperation", "copy")
	ctx.Set("fillStyle", bgCSS)
	ctx.Call("fillRect", 0, 0, a.tmpCanvas.Get("width").Int(), a.tmpCanvas.Get("height").Int())
	ctx.Set("globalCompositeOperation", "source-over")

	// set up the font
	fontWeight := "normal"
	if bold {
		fontWeight = "bold"
	}
	fontStyle := ""
	if italic {
		fontStyle = "italic"
	}
	ctx.Set("font", strings.TrimSpace(fontStyle+" "+fontWeight+" "+
		f(cfg.fontSize*cfg.dpr)+"px "+cfg.fontFamily))
	ctx.Set("textBaseline", "ideographic") // chromium baseline

	powerlineGlyph := charCount <= 2 && isPowerlineGlyph(code)
	restrictedPowerlineGlyph := isRestrictedPowerlineGlyph(code)
	fgCSS, fgRGB := a.foregroundColor(fgColorMode, fgColor, inverse, dim, bold)
	ctx.Set("fillStyle", fgCSS)

	padding := tmpCanvasGlyphPadding * 2
	if restrictedPowerlineGlyph {
		padding = 0
	}

	// draw custom (box drawing/block/powerline) glyphs procedurally
	customGlyph := tryDrawCustomChar(ctx, chars, float64(padding), float64(padding),
		float64(cfg.deviceCellWidth), float64(cfg.deviceCellHeight), cfg.fontSize, cfg.dpr)

	enableClearThresholdCheck := !powerlineGlyph

	chWidth := vt.GetStringCellWidth(chars)

	// underline
	if underline {
		ctx.Call("save")
		lineWidth := float64(int(cfg.fontSize * cfg.dpr / 15))
		if lineWidth < 1 {
			lineWidth = 1
		}
		yOffset := 0.0
		if int(lineWidth)%2 == 1 {
			yOffset = 0.5
		}
		ctx.Set("lineWidth", lineWidth)

		// underline color
		ucPacked := attr.Extended.UnderlineColor()
		ucMode := ucPacked & vt.AttrCMMask
		isDefaultUC := ucMode == 0 || ucPacked == (vt.AttrCMMask|vt.AttrRGBMask)
		if isDefaultUC {
			ctx.Set("strokeStyle", ctx.Get("fillStyle"))
		} else if ucMode == vt.AttrCMRGB {
			enableClearThresholdCheck = false
			arr := vt.ToColorRGB(uint32(attr.GetUnderlineColor())) // #nosec G115 -- 24-bit RGB channels and palette indices
			ctx.Set("strokeStyle", rgbCSS([3]int{arr[0], arr[1], arr[2]}))
		} else {
			enableClearThresholdCheck = false
			ufg := attr.GetUnderlineColor()
			if bold && ufg < 8 {
				ufg += 8
			}
			css, _ := a.ansiColor(ufg)
			ctx.Set("strokeStyle", css)
		}

		ctx.Call("beginPath")
		xLeft := float64(padding)
		yTop := jsCeil(float64(padding)+float64(cfg.deviceCharHeight)) - yOffset
		yMid := yTop + lineWidth
		yBot := yTop + lineWidth*2
		nextOffset := float64(attr.Extended.UnderlineVariantOffset())

		for i := 0; i < chWidth; i++ {
			ctx.Call("save")
			xChLeft := xLeft + float64(i*cfg.deviceCellWidth)
			xChRight := xLeft + float64((i+1)*cfg.deviceCellWidth)
			xChMid := xChLeft + float64(cfg.deviceCellWidth)/2
			switch attr.Extended.UnderlineStyle() {
			case vt.UnderlineDouble:
				ctx.Call("moveTo", xChLeft, yTop)
				ctx.Call("lineTo", xChRight, yTop)
				ctx.Call("moveTo", xChLeft, yBot)
				ctx.Call("lineTo", xChRight, yBot)
			case vt.UnderlineCurly:
				yCurlyBot := yBot
				yCurlyTop := yTop
				if lineWidth > 1 {
					yCurlyBot = jsCeil(float64(padding)+float64(cfg.deviceCharHeight)-lineWidth/2) - yOffset
					yCurlyTop = jsCeil(float64(padding)+float64(cfg.deviceCharHeight)+lineWidth/2) - yOffset
				}
				clip := js.Global().Get("Path2D").New()
				clip.Call("rect", xChLeft, yTop, float64(cfg.deviceCellWidth), yBot-yTop)
				ctx.Call("clip", clip)
				halfCell := float64(cfg.deviceCellWidth) / 2
				ctx.Call("moveTo", xChLeft-halfCell, yMid)
				ctx.Call("bezierCurveTo", xChLeft-halfCell, yCurlyTop, xChLeft, yCurlyTop, xChLeft, yMid)
				ctx.Call("bezierCurveTo", xChLeft, yCurlyBot, xChMid, yCurlyBot, xChMid, yMid)
				ctx.Call("bezierCurveTo", xChMid, yCurlyTop, xChRight, yCurlyTop, xChRight, yMid)
				ctx.Call("bezierCurveTo", xChRight, yCurlyBot, xChRight+halfCell, yCurlyBot, xChRight+halfCell, yMid)
			case vt.UnderlineDotted:
				offsetWidth := 0.0
				if nextOffset != 0 {
					if nextOffset >= lineWidth {
						offsetWidth = lineWidth*2 - nextOffset
					} else {
						offsetWidth = lineWidth - nextOffset
					}
				}
				isLineStart := nextOffset < lineWidth
				dash := jsRound(lineWidth)
				dashArr := js.Global().Get("Array").New()
				dashArr.Call("push", dash, dash)
				if !isLineStart || offsetWidth == 0 {
					ctx.Call("setLineDash", dashArr)
					ctx.Call("moveTo", xChLeft+offsetWidth, yTop)
					ctx.Call("lineTo", xChRight, yTop)
				} else {
					ctx.Call("setLineDash", dashArr)
					ctx.Call("moveTo", xChLeft, yTop)
					ctx.Call("lineTo", xChLeft+offsetWidth, yTop)
					ctx.Call("moveTo", xChLeft+offsetWidth+lineWidth, yTop)
					ctx.Call("lineTo", xChRight, yTop)
				}
				nextOffset = computeNextVariantOffset(xChRight-xChLeft, lineWidth, nextOffset)
			case vt.UnderlineDashed:
				xChWidth := xChRight - xChLeft
				line := float64(int(0.6 * xChWidth))
				gap := float64(int(0.3 * xChWidth))
				end := xChWidth - line - gap
				dashArr := js.Global().Get("Array").New()
				dashArr.Call("push", line, gap, end)
				ctx.Call("setLineDash", dashArr)
				ctx.Call("moveTo", xChLeft, yTop)
				ctx.Call("lineTo", xChRight, yTop)
			default: // single
				ctx.Call("moveTo", xChLeft, yTop)
				ctx.Call("lineTo", xChRight, yTop)
			}
			ctx.Call("stroke")
			ctx.Call("restore")
		}
		ctx.Call("restore")

		// stroke in the background color to give an outline between
		// the text and the underline
		if !customGlyph && cfg.fontSize >= 12 && chars != " " {
			ctx.Call("save")
			ctx.Set("textBaseline", "alphabetic")
			metrics := ctx.Call("measureText", chars)
			ctx.Call("restore")
			descent := metrics.Get("actualBoundingBoxDescent")
			if !descent.IsUndefined() && descent.Float() > 0 {
				ctx.Call("save")
				clip := js.Global().Get("Path2D").New()
				clip.Call("rect", xLeft, yTop-jsCeil(lineWidth/2), float64(cfg.deviceCellWidth*chWidth), yBot-yTop+jsCeil(lineWidth/2))
				ctx.Call("clip", clip)
				ctx.Set("lineWidth", cfg.dpr*3)
				ctx.Set("strokeStyle", bgCSS)
				ctx.Call("strokeText", chars, padding, padding+cfg.deviceCharHeight)
				ctx.Call("restore")
			}
		}
	}

	// overline
	if overline {
		lineWidth := float64(int(cfg.fontSize * cfg.dpr / 15))
		if lineWidth < 1 {
			lineWidth = 1
		}
		yOffset := 0.0
		if int(lineWidth)%2 == 1 {
			yOffset = 0.5
		}
		ctx.Set("lineWidth", lineWidth)
		ctx.Set("strokeStyle", ctx.Get("fillStyle"))
		ctx.Call("beginPath")
		ctx.Call("moveTo", float64(padding), float64(padding)+yOffset)
		ctx.Call("lineTo", float64(padding+cfg.deviceCharWidth*chWidth), float64(padding)+yOffset)
		ctx.Call("stroke")
	}

	// draw the character itself
	if !customGlyph {
		// A glyph the caller wants mirrored is drawn through a flip about the
		// middle of its cell. The canvas does the work, once, when the glyph is
		// first rasterised into the atlas — every frame after that samples the
		// same texture, so a mirrored terminal costs exactly as much to draw as
		// an ordinary one.
		//
		// The width flipped about is the glyph's own, chWidth cells, so a wide
		// character lands back on itself rather than beside itself.
		if cfg.mirrorGlyph != nil && cfg.mirrorGlyph(chars) {
			w := float64(cfg.deviceCellWidth * chWidth)
			ctx.Call("save")
			ctx.Call("translate", float64(padding)*2+w, 0)
			ctx.Call("scale", -1, 1)
			ctx.Call("fillText", chars, padding, padding+cfg.deviceCharHeight)
			ctx.Call("restore")
		} else {
			ctx.Call("fillText", chars, padding, padding+cfg.deviceCharHeight)
		}
	}

	// strikethrough
	if strikethrough {
		lineWidth := float64(int(cfg.fontSize * cfg.dpr / 10))
		if lineWidth < 1 {
			lineWidth = 1
		}
		yOffset := 0.0
		if int(ctx.Get("lineWidth").Float())%2 == 1 {
			yOffset = 0.5
		}
		ctx.Set("lineWidth", lineWidth)
		ctx.Set("strokeStyle", ctx.Get("fillStyle"))
		ctx.Call("beginPath")
		ctx.Call("moveTo", float64(padding), float64(padding+cfg.deviceCharHeight/2)-yOffset)
		ctx.Call("lineTo", float64(padding+cfg.deviceCharWidth*chWidth), float64(padding+cfg.deviceCharHeight/2)-yOffset)
		ctx.Call("stroke")
	}

	ctx.Call("restore")

	// pull the pixels into Go for background clearing + bounding box
	tmpW := a.tmpCanvas.Get("width").Int()
	tmpH := a.tmpCanvas.Get("height").Int()
	imageData := ctx.Call("getImageData", 0, 0, tmpW, tmpH)
	jsData := imageData.Get("data")
	pix := make([]byte, jsData.Get("length").Int())
	js.CopyBytesToGo(pix, jsData)

	isEmpty := clearColorPixels(pix, bgRGB, fgRGB, enableClearThresholdCheck)
	if isEmpty {
		return nullRasterizedGlyph
	}
	js.CopyBytesToJS(jsData, pix)

	g := a.findGlyphBoundingBox(pix, tmpW, allowedWidth, restrictedPowerlineGlyph, customGlyph, padding)

	// find a row in the (single) page
	if int(g.sizeX) > a.pageSize {
		g.sizeX = float64(a.pageSize)
	}
	row := a.findRow(int(g.sizeX), int(g.sizeY))
	if row == nil {
		// atlas full: clear everything and start over; the caller's
		// cache entry is still recorded, so re-add after clearing.
		// Every clear also re-renders the whole screen, so a page that
		// keeps filling is grown first -- see growPage.
		a.growPage()
		a.clearTexture()
		a.requestClearModel = true
		row = a.findRow(int(g.sizeX), int(g.sizeY))
		if row == nil {
			return nullRasterizedGlyph
		}
	}

	g.texPosX = float64(row.x)
	g.texPosY = float64(row.y)
	page := float64(a.pageSize)
	g.texClipX = float64(row.x) / page
	g.texClipY = float64(row.y) / page
	g.sizeClipX = g.sizeX / page
	g.sizeClipY = g.sizeY / page

	if atlasDebug {
		entry := map[string]any{
			"chars": chars, "bg": int(bg), "fg": int(fg), "ext": int(ext),
			"x": row.x, "y": row.y, "w": g.sizeX, "h": g.sizeY,
			"offX": g.offsetX, "offY": g.offsetY,
		}
		dbg := js.Global().Get("__atlasDebug")
		if dbg.IsUndefined() {
			dbg = js.Global().Get("Array").New()
			js.Global().Set("__atlasDebug", dbg)
		}
		dbg.Call("push", js.ValueOf(entry))
	}

	if int(g.sizeY) > row.height {
		row.height = int(g.sizeY)
	}
	row.x += int(g.sizeX)

	// blit the bounded region into the atlas (putImageData overwrites)
	a.ctx.Call("putImageData", imageData,
		g.texPosX-a.boundLeft, g.texPosY-a.boundTop,
		a.boundLeft, a.boundTop, g.sizeX, g.sizeY)
	a.version++

	return g
}

// findRow returns a row with enough remaining space, or nil when the
// page is full.
func (a *textureAtlas) findRow(w, h int) *atlasRow {
	// try fixed rows first (filling in short-glyph rows)
	for _, r := range a.fixedRows {
		if h <= r.height && r.height <= h+rowPixelThreshold && r.x+w <= a.pageSize {
			return r
		}
	}
	// use the current row if the height fits reasonably
	row := &a.currentRow
	if row.height == 0 || (h <= row.height+rowPixelThreshold && row.height <= h+rowPixelThreshold) || row.height >= h {
		if row.x+w <= a.pageSize {
			if row.y+maxIntv(row.height, h) <= a.pageSize {
				return row
			}
			return nil
		}
		// wrap to the next row
		if row.y+row.height+h <= a.pageSize {
			a.fixedRows = append(a.fixedRows, &atlasRow{x: row.x, y: row.y, height: row.height})
			row.x = 0
			row.y += row.height
			row.height = 0
			return row
		}
		return nil
	}
	// height mismatch: fix the current row and open a new one
	if row.y+row.height+h <= a.pageSize {
		a.fixedRows = append(a.fixedRows, &atlasRow{x: row.x, y: row.y, height: row.height})
		row.x = 0
		row.y += row.height
		row.height = 0
		return row
	}
	return nil
}

func maxIntv(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *textureAtlas) findGlyphBoundingBox(pix []byte, canvasW, allowedWidth int, restrictedGlyph, customGlyph bool, padding int) *rasterizedGlyph {
	cfg := &a.cfg
	height := a.tmpCanvas.Get("height").Int()
	width := allowedWidth
	if restrictedGlyph {
		height = cfg.deviceCellHeight
		width = cfg.deviceCellWidth
	}
	alpha := func(x, y int) byte { return pix[y*canvasW*4+x*4+3] }

	top := 0
found1:
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if alpha(x, y) != 0 {
				top = y
				break found1
			}
		}
	}
	left := 0
found2:
	for x := 0; x < padding+width; x++ {
		for y := 0; y < height; y++ {
			if alpha(x, y) != 0 {
				left = x
				break found2
			}
		}
	}
	right := width
found3:
	for x := padding + width - 1; x >= padding; x-- {
		for y := 0; y < height; y++ {
			if alpha(x, y) != 0 {
				right = x
				break found3
			}
		}
	}
	bottom := height
found4:
	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			if alpha(x, y) != 0 {
				bottom = y
				break found4
			}
		}
	}

	a.boundLeft = float64(left)
	a.boundTop = float64(top)

	offX := float64(-left + padding)
	offY := float64(-top + padding)
	if restrictedGlyph || customGlyph {
		offX += float64((cfg.deviceCellWidth - cfg.deviceCharWidth) / 2)
		if cfg.lineHeight != 1 {
			offY += jsRound(float64(cfg.deviceCellHeight-cfg.deviceCharHeight) / 2)
		}
	}
	return &rasterizedGlyph{
		sizeX:     float64(right - left + 1),
		sizeY:     float64(bottom - top + 1),
		sizeClipX: float64(right - left + 1),
		sizeClipY: float64(bottom - top + 1),
		offsetX:   offX,
		offsetY:   offY,
	}
}

// clearColorPixels makes the background color (and colors within a
// contrast-relative threshold of it) fully transparent. Returns true
// if the glyph is empty.
func clearColorPixels(pix []byte, bg, fg uint32, enableThresholdCheck bool) bool {
	r := byte(bg >> 16) // #nosec G115 -- 24-bit RGB channels and palette indices
	g := byte(bg >> 8)  // #nosec G115 -- 24-bit RGB channels and palette indices
	b := byte(bg)       // #nosec G115 -- 24-bit RGB channels and palette indices
	fgR := int(fg >> 16 & 0xff)
	fgG := int(fg >> 8 & 0xff)
	fgB := int(fg & 0xff)

	threshold := (absInt(int(r)-fgR) + absInt(int(g)-fgG) + absInt(int(b)-fgB)) / 12
	// floor the threshold so the ±1-per-channel rounding dust that
	// Chromium's opaque-canvas getImageData readback produces always
	// clears, even when fg and bg are (nearly) the same color
	if threshold < 4 {
		threshold = 4
	}

	isEmpty := true
	for off := 0; off+3 < len(pix); off += 4 {
		if pix[off] == r && pix[off+1] == g && pix[off+2] == b {
			pix[off+3] = 0
		} else if enableThresholdCheck &&
			absInt(int(pix[off])-int(r))+absInt(int(pix[off+1])-int(g))+absInt(int(pix[off+2])-int(b)) < threshold {
			pix[off+3] = 0
		} else {
			isEmpty = false
		}
	}
	return isEmpty
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func itoaInt(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func jsCeil(v float64) float64 {
	i := float64(int(v))
	if v > i {
		return i + 1
	}
	return i
}

// cssToRGB parses #rgb / #rrggbb to 0xRRGGBB (white on failure).
func cssToRGB(css string) uint32 {
	if rgb, ok := vt.ParseColor(css); ok {
		return uint32(rgb[0])<<16 | uint32(rgb[1])<<8 | uint32(rgb[2]) // #nosec G115 -- 24-bit RGB channels and palette indices
	}
	return 0xFFFFFF
}
