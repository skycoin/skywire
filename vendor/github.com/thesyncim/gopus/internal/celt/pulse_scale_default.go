//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

// scalePulsesInto sets out[i] = float32(pulses[i]) * g for every pulse, the
// normalise_residual() product loop.
func scalePulsesInto(out []celtNorm, pulses []int32, g float32) {
	scalePulsesIntoScalar(out, pulses, g)
}
