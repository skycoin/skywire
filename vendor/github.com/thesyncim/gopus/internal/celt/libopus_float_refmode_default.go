//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

const libopusFloatInnerProdUsesNeonOrder = false
