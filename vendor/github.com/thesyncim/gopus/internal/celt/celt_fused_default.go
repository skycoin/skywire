//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// celtFusedFloat is false outside the arm64 Go SIMD build. The amd64 kernels
// use their separate x86 dispatch, and arm64 ordinary and nosimd builds pair
// with the scalar libopus reference.
const celtFusedFloat = false
