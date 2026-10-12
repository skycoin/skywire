//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

// analysisSpecVariabilityAddSquare preserves the pinned libopus 1.6.1
// GCC 13.3 x86-64-v3 SIMD caller's rounded products and ordered accumulation
// for the first 16 bands.
func analysisSpecVariabilityAddSquare(acc, difference float32) float32 {
	return acc + round32(difference*difference)
}

// analysisSpecVariabilityAddFinalSquare preserves the two scalar FMAs in the
// pinned libopus 1.6.1 GCC 13.3 x86-64-v3 SIMD caller's final two bands.
func analysisSpecVariabilityAddFinalSquare(acc, difference float32) float32 {
	return fma32(difference, difference, acc)
}
