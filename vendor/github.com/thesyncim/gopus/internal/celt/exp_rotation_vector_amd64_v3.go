//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// expRotation1VectorPair follows libopus's v3 MAC16_16 contraction order:
// fma(c, x2, round(s*x1)) and fma(c, x1, round(-s*x2)).
func expRotation1VectorPair(x1, x2, c, s, ms archsimd.Float32x4) (nextStride, current archsimd.Float32x4) {
	return x2.MulAdd(c, x1.Mul(s)), x1.MulAdd(c, x2.Mul(ms))
}
