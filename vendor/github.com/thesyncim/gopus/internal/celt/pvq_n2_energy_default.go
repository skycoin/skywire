//go:build !arm64

package celt

// pvqN2Energy follows the scalar C expression in celt/vq.c alg_unquant when
// the selected target does not contract the N=2 multiply-add.
func pvqN2Energy(p0, p1 float32) float32 {
	return noFMA32Add(noFMA32Mul(p0, p0), noFMA32Mul(p1, p1))
}
