//go:build !arm64 || !goexperiment.simd || nosimd || purego

package dnnmath

// The ordinary and nosimd lanes use libopus's generic DNN kernels.
const dnnNEONEnabled = false
