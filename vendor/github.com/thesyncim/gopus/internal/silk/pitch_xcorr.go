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
	// Keep each correlation window within y. The SIMD kernel can load beyond
	// the current lane only when the next samples are within that window.
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
