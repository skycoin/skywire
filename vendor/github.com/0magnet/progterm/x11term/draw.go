//go:build unix && !android && !ios

package x11term

import (
	"bytes"
	"fmt"
	"image"
	"math/bits"

	"github.com/jezek/xgb/xproto"

	"github.com/0magnet/progterm/glterm"
)

// pixfmt is how the server lays out a pixel of the window's visual in a
// ZPixmap image.
type pixfmt struct {
	depth      byte
	bpp        int // bits per pixel: 16, 24 or 32
	pad        int // a scanline is a multiple of this many bits
	msb        bool
	shift      [3]int // red, green, blue
	width      [3]int
	plainBGRX  bool // 32 bits, least significant byte first, 8 bits a color
	maxRequest int  // the largest request, in bytes
}

func pixelFormat(s *xproto.SetupInfo, scr *xproto.ScreenInfo) (pixfmt, error) {
	p := pixfmt{depth: scr.RootDepth, msb: s.ImageByteOrder == xproto.ImageOrderMSBFirst, maxRequest: int(s.MaximumRequestLength) * 4}
	for _, f := range s.PixmapFormats {
		if f.Depth == scr.RootDepth {
			p.bpp, p.pad = int(f.BitsPerPixel), int(f.ScanlinePad)
		}
	}
	var vis *xproto.VisualInfo
	for _, d := range scr.AllowedDepths {
		for i, v := range d.Visuals {
			if v.VisualId == scr.RootVisual {
				vis = &d.Visuals[i]
			}
		}
	}
	if vis == nil || (vis.Class != xproto.VisualClassTrueColor && vis.Class != xproto.VisualClassDirectColor) {
		return p, fmt.Errorf("the screen's visual is not true color")
	}
	if p.bpp != 16 && p.bpp != 24 && p.bpp != 32 {
		return p, fmt.Errorf("%d bits a pixel is not supported", p.bpp)
	}
	for i, m := range []uint32{vis.RedMask, vis.GreenMask, vis.BlueMask} {
		p.shift[i], p.width[i] = bits.TrailingZeros32(m), bits.OnesCount32(m)
	}
	p.plainBGRX = p.bpp == 32 && !p.msb && p.shift == [3]int{16, 8, 0} && p.width == [3]int{8, 8, 8}
	return p, nil
}

// stride is the bytes in a scanline w pixels wide.
func (p pixfmt) stride(w int) int {
	b := w * p.bpp
	if p.pad > 0 {
		b = (b + p.pad - 1) / p.pad * p.pad
	}
	return b / 8
}

// convert writes r of img into out in the server's layout, and returns it.
func (p pixfmt) convert(out []byte, img *image.RGBA, r image.Rectangle) []byte {
	stride := p.stride(r.Dx())
	n := stride * r.Dy()
	if cap(out) < n {
		out = make([]byte, n)
	}
	out = out[:n]
	bpp := p.bpp / 8
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		dst := out[(y-r.Min.Y)*stride:]
		if p.plainBGRX {
			for i := 0; i+3 < len(src); i += 4 {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 0
			}
			continue
		}
		for i, j := 0, 0; i+3 < len(src); i, j = i+4, j+bpp {
			var v uint32
			for c := 0; c < 3; c++ {
				v |= uint32(src[i+c]) >> (8 - p.width[c]) << p.shift[c] //nolint:gosec // widths are at most 8
			}
			for k := 0; k < bpp; k++ {
				at := k
				if p.msb {
					at = bpp - 1 - k
				}
				dst[j+at] = byte(v >> (8 * k)) //nolint:gosec // a byte of the pixel
			}
		}
	}
	return out
}

// region is part of the window drawn from an image, with a copy of what the
// server was last sent so a frame sends only what changed.
type region struct {
	img, shown *image.RGBA
	at         image.Point
}

// fit makes the region size, at at, forgetting what was sent if it moved.
func (r *region) fit(size, at image.Point) {
	if r.img == nil || r.img.Bounds().Size() != size || r.at != at {
		r.img = image.NewRGBA(image.Rectangle{Max: size})
		r.shown = image.NewRGBA(image.Rectangle{Max: size}) // clear, which no frame is
		r.at = at
	}
}

// forget marks rect, in window pixels, as not on the server.
func (r *region) forget(rect image.Rectangle) {
	if r.shown == nil {
		return
	}
	rect = rect.Sub(r.at).Intersect(r.shown.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		clear(r.shown.Pix[r.shown.PixOffset(rect.Min.X, y):r.shown.PixOffset(rect.Max.X, y)])
	}
}

// changed are the bands of rows of r that differ from what was sent, each
// narrowed to the columns that do.
func (r *region) changed() []image.Rectangle {
	var out []image.Rectangle
	b := r.img.Bounds()
	cur := image.Rectangle{}
	for y := 0; y < b.Dy(); y++ {
		a := r.img.Pix[y*r.img.Stride : y*r.img.Stride+4*b.Dx()]
		s := r.shown.Pix[y*r.shown.Stride : y*r.shown.Stride+4*b.Dx()]
		if bytes.Equal(a, s) {
			if !cur.Empty() {
				out = append(out, cur)
				cur = image.Rectangle{}
			}
			continue
		}
		first, last := 0, len(a)-1
		for a[first] == s[first] {
			first++
		}
		for a[last] == s[last] {
			last--
		}
		row := image.Rect(first/4, y, last/4+1, y+1)
		if cur.Empty() {
			cur = row
		} else {
			cur = cur.Union(row)
		}
	}
	if !cur.Empty() {
		out = append(out, cur)
	}
	return out
}

// send puts what changed in r on the server.
func (w *window) send(r *region) {
	for _, band := range r.changed() {
		perRow := w.px.stride(band.Dx())
		rows := max(1, (w.px.maxRequest-64)/perRow)
		for y := band.Min.Y; y < band.Max.Y; y += rows {
			part := image.Rect(band.Min.X, y, band.Max.X, min(band.Max.Y, y+rows))
			w.buf = w.px.convert(w.buf, r.img, part)
			xproto.PutImage(w.conn, xproto.ImageFormatZPixmap, xproto.Drawable(w.win), w.gc,
				uint16(part.Dx()), uint16(part.Dy()), int16(part.Min.X+r.at.X), int16(part.Min.Y+r.at.Y), //nolint:gosec // window sizes
				0, w.px.depth, w.buf)
			for yy := part.Min.Y; yy < part.Max.Y; yy++ {
				o := r.img.PixOffset(part.Min.X, yy)
				e := r.img.PixOffset(part.Max.X, yy)
				copy(r.shown.Pix[o:e], r.img.Pix[o:e])
			}
		}
	}
}

// expose marks rect, in window pixels, as needing to be sent again.
func (w *window) expose(rect image.Rectangle) {
	w.bar.forget(rect)
	w.term.forget(rect)
}

// paint draws the strip and the current tab and sends what changed.
func (w *window) paint() {
	t := w.ts.Active()
	if t == nil {
		return
	}
	w.bar.fit(image.Pt(w.size.X, w.stripH), image.Point{})
	w.term.fit(w.area(), image.Pt(0, w.stripH))
	glterm.DrawStrip(w.bar.img, w.grid, &w.ts, glterm.LayoutStrip(w.ts.Labels(), w.grid.CellW, w.size.X, w.stripH))
	t.Term.Draw(w.term.img)
	w.send(&w.bar)
	w.send(&w.term)
}
