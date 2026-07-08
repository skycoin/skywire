package silk

// SILK outputs at 8kHz (NB), 12kHz (MB), or 16kHz (WB).
// Opus API expects 48kHz output.
// Upsampling factors: NB=6x, MB=4x, WB=3x.

// upsampleTo48k resamples SILK output to 48 kHz using simple linear
// interpolation. The bit-exact decode path does NOT use this: it resamples
// through LibopusResampler (silk/resampler*.c). This helper is a lightweight
// approximation kept for tests and non-bit-exact callers.
func upsampleTo48k(samples []float32, srcRate int) ([]float32, error) {
	return upsampleTo48kWithScratch(samples, srcRate, nil)
}

// upsampleTo48kWithScratch is like upsampleTo48k but uses a pre-allocated scratch buffer.
func upsampleTo48kWithScratch(samples []float32, srcRate int, scratch []float32) ([]float32, error) {
	if srcRate == 48000 {
		return samples, nil // No resampling needed
	}

	factor := 48000 / srcRate
	if factor < 1 || factor > 6 {
		return nil, ErrInvalidResampleRate
	}

	if len(samples) == 0 {
		return nil, nil
	}

	outputLen := len(samples) * factor
	var output []float32
	if scratch != nil && len(scratch) >= outputLen {
		output = scratch[:outputLen]
	} else {
		output = make([]float32, outputLen)
	}

	for i := range samples {
		curr := samples[i]
		var next float32
		if i+1 < len(samples) {
			next = samples[i+1]
		} else {
			next = curr // Hold last sample
		}

		// Linear interpolation between curr and next
		for j := range factor {
			t := float32(j) / float32(factor)
			output[i*factor+j] = curr*(1-t) + next*t
		}
	}

	return output, nil
}

// upsampleTo48kStereo resamples stereo output to 48kHz.
func upsampleTo48kStereo(left, right []float32, srcRate int) (outLeft, outRight []float32, err error) {
	outLeft, err = upsampleTo48k(left, srcRate)
	if err != nil {
		return nil, nil, err
	}
	outRight, err = upsampleTo48k(right, srcRate)
	if err != nil {
		return nil, nil, err
	}
	return outLeft, outRight, nil
}

// getUpsampleFactor returns the upsampling factor from source rate to 48kHz.
func getUpsampleFactor(bandwidth Bandwidth) int {
	switch bandwidth {
	case BandwidthNarrowband:
		return 6 // 8kHz -> 48kHz
	case BandwidthMediumband:
		return 4 // 12kHz -> 48kHz
	case BandwidthWideband:
		return 3 // 16kHz -> 48kHz
	default:
		return 1
	}
}
