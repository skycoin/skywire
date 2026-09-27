//go:build arm64 && purego

package celt

// kissMulAddSource computes a*b + c*d using source-order semantics.
// In FMAlike mode this mirrors libopus arm64 codegen (round c*d first).
func kissMulAddSource(a, b, c, d float32) float32 {
	t := noFMA32Mul(c, d)
	return fma32(a, b, t)
}

// kissMulSubSource computes a*b - c*d using source-order semantics.
// In FMAlike mode this mirrors libopus arm64 codegen (round c*d first).
func kissMulSubSource(a, b, c, d float32) float32 {
	t := noFMA32Mul(c, d)
	return fma32(a, b, -t)
}
