//go:build goexperiment.simd && !nosimd && !purego

package lpcnetplc

const analysisUseRoundedVectorProducts = true

const pitchDNNWindowRoundedProducts = true
