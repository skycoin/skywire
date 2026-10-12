//go:build !(amd64 || arm64) || nosimd || purego || !goexperiment.simd

package celt

// expRotationUsesSIMD is false off the Go SIMD builds, so expRotation1Norm
// keeps its scalar loops.
const expRotationUsesSIMD = false

// expRotation1StrideSIMD is only reached when expRotationUsesSIMD is true.
func expRotation1StrideSIMD(x []celtNorm, length, stride int, c, s opusVal16) {
	expRotation1NormScalar(x, length, stride, c, s)
}
