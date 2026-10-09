//go:build amd64.v3 && !gopus_fixed_point

package celt

const stereoSplitUsesFMA = true

// stereoSplitScalarTarget matches GCC's scalar AMD64 v3 contraction in
// bands.c:stereo_split: round c*y once, then fuse c*x into each output.
func stereoSplitScalarTarget(x, y []celtNorm) {
	y = y[:len(x)]
	c := stereoSplitInvSqrt2
	for i, xv := range x {
		xf := float32(xv)
		r := noFMA32Mul(c, float32(y[i]))
		x[i] = celtNorm(fma32(c, xf, r))
		y[i] = celtNorm(fma32(-c, xf, r))
	}
}
