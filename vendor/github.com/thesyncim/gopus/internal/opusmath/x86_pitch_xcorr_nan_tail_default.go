//go:build amd64 && goexperiment.simd && !nosimd && !purego && !amd64.v3

package opusmath

const x86PitchSSETailUsesFMA = false
