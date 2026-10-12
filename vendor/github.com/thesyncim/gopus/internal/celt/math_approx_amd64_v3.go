//go:build amd64.v3

package celt

import "math"

// celtLog2 follows the v3 contraction order of celt/mathops.h:celt_log2.
func celtLog2(x float32) float32 {
	bits := math.Float32bits(x)
	integer := int32(bits>>23) - 127
	bitsInt := int32(bits)
	bitsInt -= int32(uint32(integer) << 23)
	bits = uint32(bitsInt)

	rangeIdx := (bits >> 20) & 0x7
	f := math.Float32frombits(bits)
	f = celtMathFMA32AMD64V3(f, log2XNormCoeff[rangeIdx], -1.0625)

	f = log2CoeffA0 + f*(log2CoeffA1+f*(log2CoeffA2+f*(log2CoeffA3+f*log2CoeffA4)))
	return round32(round32(float32(integer)+f) + log2YNormCoeff[rangeIdx])
}

// celtAtan2pNormF32 follows the v3 contraction order of
// celt/mathops.h:celt_atan2p_norm and its inlined celt_atan_norm call.
// celt_atan_norm is inlined into celt_atan2p_norm by the native compiler, so
// the complementary branch keeps its final scale multiply fused with 1 - x.
func celtAtan2pNormF32(y, x float32) float32 {
	ySq := round32(y * y)
	if x*x+ySq < 1e-18 {
		return 0
	}
	if y < x {
		return celtAtanNormF32(y / x)
	}

	const (
		a1  float32 = 0.636619772367581
		a3  float32 = -0.3333165943622589
		a5  float32 = 0.19962704181671143
		a7  float32 = -0.13976582884788513
		a9  float32 = 0.09794234484434128
		a11 float32 = -0.057773590087890625
		a13 float32 = 0.023040136322379112
		a15 float32 = -0.0043554059229791164
	)
	r := x / y
	rSq := round32(r * r)
	poly := celtFloatMulAdd(rSq, a15, a13)
	poly = celtFloatMulAdd(rSq, poly, a11)
	poly = celtFloatMulAdd(rSq, poly, a9)
	poly = celtFloatMulAdd(rSq, poly, a7)
	poly = celtFloatMulAdd(rSq, poly, a5)
	poly = celtFloatMulAdd(rSq, poly, a3)
	core := celtMathFMA32AMD64V3(round32(r*rSq), poly, r)
	return celtMathFMA32AMD64V3(-a1, core, 1)
}

// Keep the operands in registers so the v3 compiler emits the single-precision
// FMA operations used by the selected libopus archive.
//
//go:noinline
func celtMathFMA32AMD64V3(a, b, c float32) float32 {
	return a*b + c
}
