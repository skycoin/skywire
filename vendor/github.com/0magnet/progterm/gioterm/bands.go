package gioterm

import (
	"bytes"
	"image"

	"github.com/0magnet/gio/op"
	"github.com/0magnet/gio/op/clip"
	"github.com/0magnet/gio/op/paint"
)

// bands paints an image as horizontal bands and keeps each band's ImageOp
// while its pixels are unchanged. Gio uploads a texture for every new
// ImageOp, so a keystroke uploads the rows it changed, not the window.
type bands struct {
	size image.Point
	h    int
	pix  [][]byte // each band's pixels as last painted, owned by its ImageOp
	ops  []paint.ImageOp
}

// paint paints img, whose bounds start at the origin, with its top left at
// at, in bands h pixels high.
func (b *bands) paint(ops *op.Ops, img *image.RGBA, at image.Point, h int) {
	size := img.Bounds().Size()
	if h <= 0 || size.X <= 0 || size.Y <= 0 {
		return
	}
	n := (size.Y + h - 1) / h
	if size != b.size || h != b.h {
		*b = bands{size: size, h: h, pix: make([][]byte, n), ops: make([]paint.ImageOp, n)}
	}
	for i := range n {
		r := image.Rect(0, i*h, size.X, min((i+1)*h, size.Y))
		px := img.Pix[r.Min.Y*img.Stride : r.Max.Y*img.Stride]
		if b.pix[i] == nil || !bytes.Equal(b.pix[i], px) {
			// A copy, as Gio may still be reading the band it replaces.
			band := image.NewRGBA(image.Rect(0, 0, size.X, r.Dy()))
			copy(band.Pix, px)
			b.pix[i], b.ops[i] = band.Pix, paint.NewImageOp(band)
		}
		off := op.Offset(at.Add(r.Min)).Push(ops)
		cl := clip.Rect(image.Rectangle{Max: r.Size()}).Push(ops)
		b.ops[i].Add(ops)
		paint.PaintOp{}.Add(ops)
		cl.Pop()
		off.Pop()
	}
}
