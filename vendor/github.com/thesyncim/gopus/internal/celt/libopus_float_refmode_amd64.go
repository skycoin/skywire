//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

const libopusFloatInnerProdUsesSSEOrder = true
