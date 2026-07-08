//go:build arm64 && !purego

package silk

//go:noescape
func celtPitchXcorrFloatImplASM(x, y []float32, out []float32, length, maxPitch int)

func celtPitchXcorrFloatImpl(x, y []float32, out []float32, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	if length&3 != 0 {
		celtPitchXcorrFloatImplScalar(x, y, out, length, maxPitch)
		return
	}
	celtPitchXcorrFloatImplASM(x, y, out, length, maxPitch)
}
