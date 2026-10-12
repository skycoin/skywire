//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

const libopusFloatInnerProdUsesNeonOrder = true
