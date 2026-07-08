package silk

// SILK stereo uses mid-side coding with prediction.
// Per RFC 6716 Section 4.2.8.
//
// Mid channel = (L + R) / 2
// Side channel = (L - R) / 2
// Plus stereo prediction weights for enhanced quality.
//
// libopus uses 80-level quantization (16 main levels x 5 sub-steps).
// The encoder quantizes and encodes the indices, the decoder decodes
// indices and dequantizes to get the prediction weights.

// stereoUnmix converts mid-side decoded samples to left-right in the float
// domain, per RFC 6716 Section 4.2.8. This is a simplified reference helper used
// by unit tests; the bit-exact decode path performs MS->LR with weight
// interpolation in silkStereoMSToLR (silk/stereo_MS_to_LR.c).
//
// Basic mid-side: L = M + S, R = M - S
// With prediction weights:
//   - pred = (w0 * M[n] + w1 * M[n-1]) >> 13
//   - S' = S + pred
//   - L = M + S', R = M - S'
//
// For simplicity, the basic formula is used when weights are zero.
func stereoUnmix(mid, side []float32, w0, w1 int16, left, right []float32) error {
	if len(mid) != len(side) || len(mid) != len(left) || len(mid) != len(right) {
		return ErrMismatchedLengths
	}

	// Previous mid sample for prediction
	var prevMid float32

	for i := range mid {
		m := mid[i]
		s := side[i]

		// Apply stereo prediction if weights are non-zero
		if w0 != 0 || w1 != 0 {
			// pred = (w0 * m + w1 * prevMid) / 8192.0 (Q13 to float)
			pred := (float32(w0)*m + float32(w1)*prevMid) / 8192.0
			s += pred
		}

		// Unmix to left/right
		// L = M + S
		// R = M - S
		left[i] = m + s
		right[i] = m - s

		// Clamp to valid range [-1, 1]
		if left[i] > 1.0 {
			left[i] = 1.0
		} else if left[i] < -1.0 {
			left[i] = -1.0
		}
		if right[i] > 1.0 {
			right[i] = 1.0
		} else if right[i] < -1.0 {
			right[i] = -1.0
		}

		prevMid = m
	}
	return nil
}
