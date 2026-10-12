//go:build amd64.v3 && !gopus_fixed_point

package celt

const stereoMergeUsesFMA = true

// stereoMergeScalarMAC matches the serial celt_inner_prod_c MAC16_16 loop in
// libopus celt/pitch.h: the scalar v3 reference rounds each product before
// adding it to the accumulator.
func stereoMergeScalarMAC(a, b, sum float32) float32 {
	return noFMA32Add(noFMA32Mul(a, b), sum)
}

// stereoMergeEnergy matches the contraction order in libopus 1.6.1
// celt/bands.c:stereo_merge when compiled for AMD64 v3: mid*mid+side, then
// sum±2*xp each use one rounding.
func stereoMergeEnergy(mid, side, xp float32) (el, er float32) {
	sum := fma32(mid, mid, side)
	el = fma32(-2, xp, sum)
	er = fma32(2, xp, sum)
	return el, er
}
