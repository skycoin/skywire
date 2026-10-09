//go:build arm64

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// pvqN2Energy follows celt/vq.c alg_unquant's N=2 expression. The selected
// arm64 libopus object computes iy[0]^2 + iy[1]^2 with FMADDS, rounding the
// iy[1]^2 product first and fusing iy[0]^2 with that value.
func pvqN2Energy(p0, p1 float32) float32 {
	return opusmath.FMA32(p0, p0, noFMA32Mul(p1, p1))
}
