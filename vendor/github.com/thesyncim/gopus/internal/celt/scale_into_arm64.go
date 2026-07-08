//go:build arm64 && !purego

package celt

// scaleFloat32IntoNEON computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements. Each lane is a bare FMUL, so it is bit-exact on every build.
//
//go:noescape
func scaleFloat32IntoNEON(dst, src []float32, gain float32)
