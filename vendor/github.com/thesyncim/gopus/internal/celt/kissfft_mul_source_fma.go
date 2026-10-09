//go:build arm64 && (nosimd || purego)

package celt

// kissMulSourceNaNFixup is false: the fused products have no separate
// non-finite path.
const kissMulSourceNaNFixup = false

// kissMulAddSource computes a*b + c*d using source-order semantics.
// In FMAlike mode this mirrors libopus arm64 codegen (round c*d first).
func kissMulAddSource(a, b, c, d float32) (float32, bool) {
	t := noFMA32Mul(c, d)
	return fma32(a, b, t), false
}

// kissMulSubSource computes a*b - c*d using source-order semantics.
// In FMAlike mode this mirrors libopus arm64 codegen (round c*d first).
func kissMulSubSource(a, b, c, d float32) (float32, bool) {
	t := noFMA32Mul(c, d)
	return fma32(a, b, -t), false
}

func kissMulAddSourceNonFinite(a, b, c, d float32) float32 {
	value, _ := kissMulAddSource(a, b, c, d)
	return value
}

func kissMulSubSourceNonFinite(a, b, c, d float32) float32 {
	value, _ := kissMulSubSource(a, b, c, d)
	return value
}

// kissMulSubFast returns the kissMulSubSource value for the Fast butterflies.
func kissMulSubFast(a, b, c, d float32) float32 {
	return fma32(a, b, -noFMA32Mul(c, d))
}

// kissMulAddFast returns the kissMulAddSource value for the Fast butterflies.
func kissMulAddFast(a, b, c, d float32) float32 {
	return fma32(a, b, noFMA32Mul(c, d))
}
