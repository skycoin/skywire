//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

// celtFusedFloat selects the arithmetic order of selected CELT float kernels
// for the arm64 Go SIMD build paired with libopus NEON. Other CELT kernels have
// their own dispatch decisions; this constant does not describe their parity
// coverage.
const celtFusedFloat = true
