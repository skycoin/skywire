//go:build amd64 && goexperiment.simd && !nosimd && !purego && !amd64.v3

package celt

import "simd/archsimd"

// expRotation1VectorPair evaluates both spread-rotation outputs with rounded
// products and an add, matching the selected non-v3 x86 libopus build.
func expRotation1VectorPair(x1, x2, c, s, ms archsimd.Float32x4) (nextStride, current archsimd.Float32x4) {
	return x2.Mul(c).Add(x1.Mul(s)), x1.Mul(c).Add(x2.Mul(ms))
}
