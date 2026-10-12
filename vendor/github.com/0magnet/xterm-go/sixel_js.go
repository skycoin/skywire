//go:build js && wasm

package xterm

import (
	"math"
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// Sixel pictures, decoded by the core (vt/sixel.go), laid into the text here
// as inline images: on a canvas over the cells they cover, scrolling with
// their lines.
//
// A sixel pixel is a device pixel. That is the unit CSI 14 t and 16 t report
// sizes in, so a program that sizes its picture to the cells it was told
// about gets a picture that fits them, at any zoom, and a picture is drawn
// at its own size rather than stretched to whole cells.

func (t *Terminal) wireSixel() {
	t.Core.OnSixel = t.drawSixel
	t.Core.OnSixelGeometry = func() (int, int) {
		dpr := t.dpr()
		return int(t.cellW * float64(t.Core.Cols()) * dpr), int(t.cellH * float64(t.Core.Rows()) * dpr)
	}
}

func (t *Terminal) dpr() float64 {
	if d := window.Get("devicePixelRatio").Float(); d > 0 {
		return d
	}
	return 1
}

// drawSixel lays a picture at the cursor and leaves the cursor where a VT340
// leaves it, as xterm.js does: on the picture's last row, in its first
// column.
func (t *Terminal) drawSixel(img *vt.SixelImage) {
	if !t.opened || t.cellW == 0 || t.cellH == 0 {
		return
	}
	if !img.Transparent {
		_, bg := t.colors.ResolveCellColors(&img.Attr)
		if bg == "" {
			bg = t.colors.Background
		}
		img.Fill(cssToRGB(bg))
	}
	canvas := document.Call("createElement", "canvas")
	canvas.Set("width", img.Width)
	canvas.Set("height", img.Height)
	canvas.Get("style").Set("cssText", "position:absolute;pointer-events:none")
	data := js.Global().Get("Uint8ClampedArray").New(len(img.Pix))
	js.CopyBytesToJS(data, img.Pix)
	imageData := js.Global().Get("ImageData").New(data, img.Width, img.Height)
	canvas.Call("getContext", "2d").Call("putImageData", imageData, 0, 0)

	dpr := t.dpr()
	w, h := float64(img.Width)/dpr, float64(img.Height)/dpr
	cols := int(math.Ceil(w / t.cellW))
	rows := int(math.Ceil(h / t.cellH))
	origin := t.Core.Buffer().X
	laid := t.layImage(canvas, max(cols, 1), max(rows, 1))
	laid.w, laid.h = w, h
	t.Core.Buffer().X = origin
	t.placeImages()
}
