//go:build !wasm || !goexperiment.simd || tinygo

package fft

// simdEnabled is false outside js/wasm with GOEXPERIMENT=simd, which compiles
// the vector path out of radix2FFT entirely.
const simdEnabled = false

func getRotFactors(int, []complex128) []complex128 { return nil }

func butterflySIMD(_, _, _, _ []complex128, _, _, _, _ int) {}
