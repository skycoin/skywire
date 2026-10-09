//go:build arm64 && !nosimd && !purego

package celt

// kissMulSourceNaNFixup is false: the fused products have no separate
// non-finite path.
const kissMulSourceNaNFixup = false

func kissMulAddSource(a, b, c, d float32) (float32, bool) {
	return fma32(a, b, noFMA32Mul(c, d)), false
}

func kissMulSubSource(a, b, c, d float32) (float32, bool) {
	return fma32(a, b, -noFMA32Mul(c, d)), false
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
