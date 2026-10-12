//go:build !amd64 || !goexperiment.simd || nosimd || purego

package lpcnetplc

const useX86SelectedPitchKernels = false
