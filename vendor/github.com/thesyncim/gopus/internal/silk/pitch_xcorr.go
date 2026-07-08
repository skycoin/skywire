package silk

func celtPitchXcorrFloat(x, y []float32, out []float32, length, maxPitch int) {
	if maxPitch <= 0 || length <= 0 {
		return
	}
	if len(x) < length {
		length = len(x)
	}
	if length <= 0 || len(out) == 0 {
		return
	}
	// Need at least `length` samples for scalar correlation.
	// The assembly implementation might read up to length+3 for the kernel,
	// but it should handle bounds if we ensure maxPitch respects len(y).
	maxByY := len(y) - length + 1
	if maxByY <= 0 {
		return
	}
	if maxPitch > maxByY {
		maxPitch = maxByY
	}
	if maxPitch > len(out) {
		maxPitch = len(out)
	}
	if maxPitch <= 0 {
		return
	}

	celtPitchXcorrFloatImpl(x, y, out, length, maxPitch)
}
