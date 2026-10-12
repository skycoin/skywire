//go:build !arm64 && !amd64.v3

package celt

import "math"

func kissMulAddSource(a, b, c, d float32) (float32, bool) {
	ab := round32(a * b)
	cd := round32(c * d)
	result := ab + cd
	return result, kissMulSourceIsNaN(result)
}

func kissMulSubSource(a, b, c, d float32) (float32, bool) {
	ab := round32(a * b)
	cd := round32(c * d)
	result := ab - cd
	return result, kissMulSourceIsNaN(result)
}

func kissMulSourceIsNaN(x float32) bool {
	const exponent = uint32(0x7f800000)
	return math.Float32bits(x)&0x7fffffff > exponent
}

//go:noinline
func kissMulAddSourceNonFinite(a, b, c, d float32) float32 {
	return a*b + c*d
}

//go:noinline
func kissMulSubSourceNonFinite(a, b, c, d float32) float32 {
	return a*b - c*d
}

// kissMulSubFast returns a*b - c*d with both products rounded to float32, the
// kissMulSubSource value without its NaN flag. The Fast butterflies use it on
// bounded input, where no product is NaN.
func kissMulSubFast(a, b, c, d float32) float32 {
	return float32(a*b) - float32(c*d)
}

// kissMulAddFast returns a*b + c*d like kissMulSubFast.
func kissMulAddFast(a, b, c, d float32) float32 {
	return float32(a*b) + float32(c*d)
}
