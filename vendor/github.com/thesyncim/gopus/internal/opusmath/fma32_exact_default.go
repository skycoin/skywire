//go:build !arm64

package opusmath

import "math"

// FMA32 is a correctly rounded float32 fused multiply-add: a*b+c with a single
// rounding to float32, the result of the FMADDS / VFMADD*PS instructions the
// libopus CELT AVX2 and arm64 NEON kernels use.
//
// The float32 product is exact in float64 (48 significant bits). The float64
// sum of the product and c can round once, so it is re-rounded to odd (TwoSum
// recovers the exact error; an inexact even result moves one float64 ulp toward
// the exact sum). Rounding a round-to-odd float64 to float32 then yields the
// correctly rounded float32 result, which float32(math.FMA(a, b, c)) does not
// guarantee: the double rounding of the float64 FMA result can land on a
// float32 tie (0x3fcca800*0x3f979800 + 0xa20c2545 is 0x3ff26137, not
// 0x3ff26138). The float64 values are transient and never stored as codec
// state or scratch.
func FMA32(a, b, c float32) float32 {
	p := float64(a) * float64(b)
	cc := float64(c)
	s := p + cc
	if s != s {
		// Delegate exceptional operands to math.FMA. Native callers that
		// require a particular NaN payload test that instruction path separately.
		return float32(math.FMA(float64(a), float64(b), cc))
	}
	v := s - p
	e := (p - (s - v)) + (cc - v)
	if e != 0 && !math.IsInf(s, 0) {
		bits := math.Float64bits(s)
		if bits&1 == 0 {
			if (e > 0) == (s > 0) {
				bits++
			} else {
				bits--
			}
			s = math.Float64frombits(bits)
		}
	}
	return float32(s)
}
