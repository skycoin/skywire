package silk

// lpcSynthesis applies LPC synthesis filter to excitation to produce output.
// Per RFC 6716 Section 4.2.7.9.2.
//
// LPC is an all-pole filter that shapes the excitation spectrum to match
// the speech spectral envelope:
//
//	out[n] = exc[n] + sum(a[k] * out[n-k-1]) for k=0..order-1
//
// Parameters:
//   - excitation: input excitation signal (already gain-scaled, Q0 PCM units)
//   - lpcCoeffs: Q12 LPC coefficients
//   - gain: Q16 subframe gain (unused; gain already applied to excitation)
//   - out: output buffer (must be pre-allocated with same length as excitation)
//
// The filter maintains state across subframes via d.prevLPCValues.
func (d *Decoder) lpcSynthesis(excitation []int32, lpcCoeffs []int16, gain int32, out []float32) {
	_ = gain

	order := len(lpcCoeffs)
	if order == 0 || len(excitation) == 0 {
		return
	}

	for i, exc := range excitation {
		// Convert excitation from Q0 PCM to normalized float.
		sample := float32(exc) / 32768.0

		// Add LPC prediction from previous outputs
		for k := range order {
			// Get previous output from current buffer or state
			var prev float32
			if i-k-1 >= 0 {
				prev = out[i-k-1]
			} else {
				// Use state from previous frame/subframe
				stateIdx := order - 1 - (k - i)
				if stateIdx >= 0 && stateIdx < len(d.prevLPCValues) {
					prev = d.prevLPCValues[stateIdx]
				}
			}

			// Q12 coeff applied to normalized sample.
			sample += float32(lpcCoeffs[k]) * prev / 4096.0
		}

		// Clamp to normalized range.
		if sample > 1.0 {
			sample = 1.0
		}
		if sample < -1.0 {
			sample = -1.0
		}

		out[i] = sample
	}

	// Update LPC state for next subframe/frame
	d.updateLPCState(out, order)
}

// updateLPCState updates the LPC filter state with new output samples.
// This ensures continuity across subframe/frame boundaries.
func (d *Decoder) updateLPCState(samples []float32, order int) {
	if len(samples) >= order {
		// Copy last 'order' samples directly
		copy(d.prevLPCValues[:order], samples[len(samples)-order:])
	} else {
		// Shift existing state and append new samples
		shift := order - len(samples)
		copy(d.prevLPCValues[:shift], d.prevLPCValues[len(samples):order])
		copy(d.prevLPCValues[shift:], samples)
	}
}

// limitLPCFilterGain applies bandwidth expansion to ensure filter stability.
// Per RFC 6716 Section 4.2.7.5.5.
//
// If the LPC filter has poles too close to the unit circle, the output
// can become unstable (exponential growth). This function iteratively
// applies bandwidth expansion (coefficient decay) until the filter gain
// is within safe bounds.
//
// The function modifies the LPC coefficients in place.
func limitLPCFilterGain(lpc []int16) {
	// Maximum iterations to prevent infinite loop
	// Per RFC 6716, up to 16 rounds of bandwidth expansion may be needed
	const maxIterations = 30

	// Gain threshold in Q24 format
	// This corresponds to a maximum filter gain that keeps poles
	// safely inside the unit circle
	const gainThreshold = 1 << 24

	for range maxIterations {
		// Compute sum of squared coefficients as a proxy for filter gain
		// This is a simplified stability check; full check would compute
		// reflection coefficients and verify all are < 1
		var sumSq int64
		for _, c := range lpc {
			sumSq += int64(c) * int64(c)
		}

		// Check if gain is acceptable
		if sumSq < gainThreshold {
			return
		}

		// Apply bandwidth expansion: a[k] *= chirp^k
		// Using chirp = 0.96 in Q15 = 31457
		// This pushes poles toward origin, increasing stability margin
		// More aggressive chirp (0.96 vs 0.99) ensures faster convergence
		applyBandwidthExpansion(lpc, 31457)
	}
}

// applyBandwidthExpansion scales LPC coefficients to move poles toward origin.
// chirpQ15 is the expansion factor in Q15 format (32768 = 1.0).
//
// Each coefficient is scaled: a[k] = a[k] * chirp^k
// This effectively applies a frequency-dependent decay that prevents
// poles from being too close to the unit circle.
func applyBandwidthExpansion(lpc []int16, chirpQ15 int32) {
	// Start with chirp^1
	factor := chirpQ15

	for k := range lpc {
		// Scale coefficient: a[k] * factor / 32768
		lpc[k] = int16((int32(lpc[k]) * factor) >> 15)

		// Update factor for next coefficient: factor = factor * chirp / 32768
		factor = (factor * chirpQ15) >> 15
	}
}

// lpcInterpolate interpolates LPC coefficients between two sets.
// Per RFC 6716 Section 4.2.7.9, LPC coefficients can be interpolated
// between subframes for smoother transitions.
//
// Parameters:
//   - lpc0: LPC coefficients at start
//   - lpc1: LPC coefficients at end
//   - alpha: interpolation factor in Q8 (0=lpc0, 256=lpc1)
//
// Returns interpolated LPC coefficients.
func lpcInterpolate(lpc0, lpc1 []int16, alpha int32) []int16 {
	if len(lpc0) != len(lpc1) {
		return nil
	}

	result := make([]int16, len(lpc0))
	beta := 256 - alpha // Complement

	for i := range lpc0 {
		// Weighted average: (lpc0 * beta + lpc1 * alpha + 128) >> 8
		val := (int32(lpc0[i])*beta + int32(lpc1[i])*alpha + 128) >> 8
		result[i] = int16(val)
	}

	return result
}
