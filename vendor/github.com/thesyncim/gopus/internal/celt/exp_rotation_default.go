//go:build !arm64 || purego

package celt

// expRotationUsesNeon is false off the fused arm64 build, so expRotation1Norm
// keeps its scalar loops and the purego/amd64 byte-exact oracle holds.
const expRotationUsesNeon = false

// expRotation1StrideNeon is never called off arm64 (guarded by
// expRotationUsesNeon); the stub keeps the package building on all targets.
func expRotation1StrideNeon(x []celtNorm, length, stride int, c, s opusVal16) {
	expRotation1NormScalar(x, length, stride, c, s)
}
