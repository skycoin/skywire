//go:build amd64.v3

package celt

import "math"

// kissMulSourceNaNFixup is false because the v3 path evaluates each complex
// multiply with the same fused source product as the libopus C build.
const kissMulSourceNaNFixup = false

func kissMulAddSource(a, b, c, d float32) (float32, bool) {
	return fma32(a, b, noFMA32Mul(c, d)), false
}

func kissMulSubSource(a, b, c, d float32) (float32, bool) {
	return fma32(a, b, -noFMA32Mul(c, d)), false
}

func kissMulSourceIsNaN(x float32) bool {
	const exponent = uint32(0x7f800000)
	return math.Float32bits(x)&0x7fffffff > exponent
}

func kissMulAddSourceNonFinite(a, b, c, d float32) float32 {
	value, _ := kissMulAddSource(a, b, c, d)
	return value
}

func kissMulSubSourceNonFinite(a, b, c, d float32) float32 {
	value, _ := kissMulSubSource(a, b, c, d)
	return value
}

func kissMulSubFast(a, b, c, d float32) float32 {
	return fma32(a, b, -noFMA32Mul(c, d))
}

func kissMulAddFast(a, b, c, d float32) float32 {
	return fma32(a, b, noFMA32Mul(c, d))
}
