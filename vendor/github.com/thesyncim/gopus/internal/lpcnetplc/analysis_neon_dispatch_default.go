//go:build !arm64 || !goexperiment.simd || nosimd || purego

package lpcnetplc

const useNEONAnalysisKernels = false
