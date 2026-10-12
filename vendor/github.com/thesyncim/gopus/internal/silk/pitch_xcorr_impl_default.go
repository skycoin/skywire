//go:build !amd64 || nosimd || purego || !goexperiment.simd

package silk

func celtPitchXcorrFloatImpl(x, y []float32, out []float32, length, maxPitch int) {
	if usePitchXcorrArm64SIMD {
		celtPitchXcorrFloatImplArm64SIMD(x, y, out, length, maxPitch)
		return
	}
	celtPitchXcorrFloatImplScalar(x, y, out, length, maxPitch)
}
