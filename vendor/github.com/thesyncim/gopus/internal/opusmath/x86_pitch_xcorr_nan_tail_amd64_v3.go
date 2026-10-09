//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego

package opusmath

const x86PitchSSETailUsesFMA = true
