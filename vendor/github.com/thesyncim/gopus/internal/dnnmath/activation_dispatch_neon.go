//go:build arm64 && goexperiment.simd && !nosimd && !purego

package dnnmath

// The native NEON DNN reference is selected only for the Go SIMD lane.
const dnnNEONEnabled = true
