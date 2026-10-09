//go:build !arm64 || (goexperiment.simd && !nosimd && !purego)

package lace

const scalarOSCEGenericFMA = false
