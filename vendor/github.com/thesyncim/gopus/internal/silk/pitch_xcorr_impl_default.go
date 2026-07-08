//go:build !arm64 || purego

package silk

func celtPitchXcorrFloatImpl(x, y []float32, out []float32, length, maxPitch int) {
	celtPitchXcorrFloatImplScalar(x, y, out, length, maxPitch)
}
