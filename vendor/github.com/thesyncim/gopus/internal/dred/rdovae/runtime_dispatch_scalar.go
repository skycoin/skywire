//go:build !arm64 || !goexperiment.simd || nosimd || purego

package rdovae

const rdovaeNEONEnabled = false
