//go:build !amd64.v3

package celt

import "math"

// celtLog2 approximates log2(x) using libopus's FLOAT_APPROX polynomial.
// This uses the libopus float-path expression order for non-v3 targets.
func celtLog2(x float32) float32 {
	// Libopus assumes x > 0 and does not handle denormals/NaN/inf.
	// Our callers ensure a small epsilon, so keep the same behavior.
	bits := math.Float32bits(x)
	integer := int32(bits>>23) - 127
	// Normalize mantissa to [1, 2) by removing exponent bits.
	bitsInt := int32(bits)
	bitsInt -= int32(uint32(integer) << 23)
	bits = uint32(bitsInt)

	rangeIdx := (bits >> 20) & 0x7
	f := math.Float32frombits(bits)
	f = f*log2XNormCoeff[rangeIdx] - 1.0625

	f = log2CoeffA0 + f*(log2CoeffA1+f*(log2CoeffA2+f*(log2CoeffA3+f*log2CoeffA4)))
	return float32(integer) + f + log2YNormCoeff[rangeIdx]
}

// celtAtan2pNormF32 uses the libopus float-path expression order for non-v3 targets.
func celtAtan2pNormF32(y, x float32) float32 {
	if x*x+y*y < 1e-18 {
		return 0
	}
	if y < x {
		return celtAtanNormF32(y / x)
	}
	return 1 - celtAtanNormF32(x/y)
}
