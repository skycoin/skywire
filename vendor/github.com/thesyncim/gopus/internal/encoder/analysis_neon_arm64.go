//go:build arm64 && goexperiment.simd && !nosimd && !purego

package encoder

// analysisNEONReductions selects the arithmetic order of the NEON libopus build
// that the arm64 Go SIMD build pairs with. The selected C object rounds the
// tonality and noisiness products in four-bin groups before adding them in
// order; the remaining bins use scalar fused accumulation.
const analysisNEONReductions = true
