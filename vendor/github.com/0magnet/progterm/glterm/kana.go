package glterm

import (
	"bytes"
	"compress/gzip"
	_ "embed" // kana.gz
	"image"
	"io"
	"sync"

	xdraw "golang.org/x/image/draw"
)

// kanaGz is the half-width katakana forms, U+FF61 to U+FF9F, which a Matrix
// rain is made of, drawn by internal/kanagen from Source Han Sans: for a
// terminal none of whose fonts have them, as on a phone, where Go Mono has
// none and x/image cannot read the system's CJK font. See kana.LICENSE.
//
//go:embed kana.gz
var kanaGz []byte

const kanaFirst, kanaLast = 0xff61, 0xff9f

var (
	kanaOnce     sync.Once
	kanaW, kanaH int
	kanaPix      []byte
)

// kanaBitmap is r's glyph, its em box with the baseline 0.88 down, or nil for
// a rune it does not have.
func kanaBitmap(r rune) *image.Alpha {
	if r < kanaFirst || r > kanaLast {
		return nil
	}
	kanaOnce.Do(func() {
		z, err := gzip.NewReader(bytes.NewReader(kanaGz))
		if err != nil {
			return
		}
		b, err := io.ReadAll(z)
		if err != nil || len(b) < 2 {
			return
		}
		w, h := int(b[0]), int(b[1])
		if len(b) != 2+w*h*(kanaLast-kanaFirst+1) {
			return
		}
		kanaW, kanaH, kanaPix = w, h, b[2:]
	})
	if kanaPix == nil {
		return nil
	}
	n := kanaW * kanaH
	i := int(r-kanaFirst) * n
	return &image.Alpha{Pix: kanaPix[i : i+n], Stride: kanaW, Rect: image.Rect(0, 0, kanaW, kanaH)}
}

// drawKana scales k into the cell m, its em most of the cell's height and its
// baseline on the terminal's.
func (g *Grid) drawKana(m *image.Alpha, k *image.Alpha) {
	em := g.CellH * 82 / 100
	top := g.Ascent - em*88/100
	xdraw.CatmullRom.Scale(m, image.Rect(0, top, g.CellW, top+em), k, k.Bounds(), xdraw.Over, nil)
}
