//go:build js && wasm

package xterm

import (
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// Inline images: a picture laid into the text at the cursor, as iTerm2's
// inline images and kitty's graphics protocol put them, which scrolls with
// its line and stays in the scrollback after the program that printed it is
// gone. The protocols are the embedder's to read (an OSC or APC handler);
// this is the part only a terminal can do: anchor the picture to a buffer
// line, keep it there as the text scrolls, and let it go with its line.
//
// A picture is an element over the cells it covers, in a layer of its own
// under anything an embedder lays over the screen. It goes when its line
// leaves the scrollback, when the part of the buffer it is on is erased (ED
// 2 for the screen, ED 3 for the scrollback), or at a full reset.

type inlineImage struct {
	el     js.Value
	marker *vt.Marker
	buf    *vt.Buffer // the buffer it is in: the normal one, or the alternate
	col    int
	cols   int
	rows   int
	// w and h, when set, are the picture's own size in CSS pixels, which it
	// is drawn at instead of being fitted to its cells: a sixel picture is
	// pixels, and stretching it to whole cells would blur it.
	w, h float64
}

// AddImage lays the picture at src (any URL an <img> takes: a blob: one
// for bytes) over cols×rows cells at the cursor, and moves the cursor past
// it as a character that wide and tall would: to its last row, after its
// right edge. It returns a handle for RemoveImage, or nil when there is
// nothing to lay.
func (t *Terminal) AddImage(src string, cols, rows int) *InlineImage {
	if !t.opened || cols <= 0 || rows <= 0 {
		return nil
	}
	el := js.Global().Get("document").Call("createElement", "img")
	el.Set("alt", "")
	el.Set("src", src)
	el.Get("style").Set("cssText", "position:absolute;object-fit:contain;pointer-events:none")
	img := t.layImage(el, cols, rows)
	t.Core.Buffer().X = min(img.col+cols, t.Core.Cols()-1)
	t.placeImages()
	return &InlineImage{img: img}
}

// layImage puts el over cols×rows cells at the cursor and carries the text
// on below it, leaving the cursor on the picture's last row for the caller
// to put in the column it wants.
func (t *Terminal) layImage(el js.Value, cols, rows int) *inlineImage {
	t.wireImages()
	b := t.Core.Buffer()
	img := &inlineImage{buf: b, col: b.X, cols: cols, rows: rows, el: el}
	img.marker = b.AddMarker(b.YBase + b.Y)
	t.imagesLayer().Call("append", img.el)
	t.images = append(t.images, img)
	ih := t.Core.InputHandler()
	for i := 1; i < rows; i++ {
		ih.LineFeed()
	}
	return img
}

// InlineImage is a picture AddImage laid.
type InlineImage struct{ img *inlineImage }

// RemoveImage takes a picture away.
func (t *Terminal) RemoveImage(h *InlineImage) {
	if h == nil || h.img == nil {
		return
	}
	h.img.marker.Dispose()
	t.placeImages()
}

// ClearImages takes every picture away.
func (t *Terminal) ClearImages() {
	for _, img := range t.images {
		img.marker.Dispose()
	}
	t.placeImages()
}

// imagesLayer is the layer the pictures are in, made on first use.
func (t *Terminal) imagesLayer() js.Value {
	if !t.imgLayer.Truthy() {
		t.imgLayer = js.Global().Get("document").Call("createElement", "div")
		t.imgLayer.Get("style").Set("cssText", "position:absolute;inset:0;pointer-events:none;z-index:4;overflow:hidden")
		t.screen.Call("appendChild", t.imgLayer)
	}
	return t.imgLayer
}

// wireImages, once, has erasing and resetting take pictures with the text.
// Each handler lets the terminal's own run after it.
func (t *Terminal) wireImages() {
	if t.imagesWired {
		return
	}
	t.imagesWired = true
	p := t.Core.InputHandler().Parser()
	p.RegisterCsiHandler(vt.FunctionID{Final: "J"}, func(params *vt.Params) bool {
		b := t.Core.Buffer()
		switch params.Params[0] {
		case 2: // the screen
			t.dropImages(func(img *inlineImage) bool {
				return img.buf == b && img.marker.Line+img.rows > b.YBase
			})
		case 3: // the scrollback
			t.dropImages(func(img *inlineImage) bool { return img.buf == b && img.marker.Line < b.YBase })
		}
		return false
	})
	p.RegisterEscHandler(vt.FunctionID{Final: "c"}, func() bool {
		t.dropImages(func(*inlineImage) bool { return true })
		return false
	})
}

func (t *Terminal) dropImages(which func(*inlineImage) bool) {
	for _, img := range t.images {
		if which(img) {
			img.marker.Dispose()
		}
	}
	t.placeImages()
}

// placeImages puts each picture over its cells as the buffer now scrolls,
// and lets go of those whose line is gone.
func (t *Terminal) placeImages() {
	if len(t.images) == 0 {
		return
	}
	b := t.Core.Buffer()
	rows := t.Core.Rows()
	kept := t.images[:0]
	for _, img := range t.images {
		if img.marker.IsDisposed() {
			img.el.Call("remove")
			continue
		}
		kept = append(kept, img)
		row := img.marker.Line - b.YDisp
		st := img.el.Get("style")
		if img.buf != b || row+img.rows <= 0 || row >= rows {
			st.Set("display", "none")
			continue
		}
		st.Set("display", "")
		st.Set("left", jsPx(float64(img.col)*t.cellW))
		st.Set("top", jsPx(float64(row)*t.cellH))
		if img.w > 0 {
			st.Set("width", jsPx(img.w))
			st.Set("height", jsPx(img.h))
		} else {
			st.Set("width", jsPx(float64(img.cols)*t.cellW))
			st.Set("height", jsPx(float64(img.rows)*t.cellH))
		}
	}
	clear(t.images[len(kept):])
	t.images = kept
}

// CellSize is one cell's size in CSS pixels: what an embedder needs to say
// how many cells a picture of a given size covers.
func (t *Terminal) CellSize() (w, h float64) { return t.cellW, t.cellH }
