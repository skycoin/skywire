//go:build !goexperiment.simd || nosimd || purego

package lpcnetplc

const analysisUseRoundedVectorProducts = false

const pitchDNNWindowRoundedProducts = false
