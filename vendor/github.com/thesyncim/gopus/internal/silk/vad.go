package silk

// classifyFrame determines the signal type for a PCM frame.
// Returns signalType (0=inactive, 1=unvoiced, 2=voiced) and quantOffset (0=low, 1=high).
//
// This follows the libopus two-phase approach:
// Phase 1: Energy-based activity detection (inactive vs active)
// Phase 2: Pitch analysis to distinguish voiced from unvoiced
//
// Per RFC 6716 Section 4.2.7.3, draft-vos-silk-01 Section 2.1.2.2.
// Reference: libopus silk/float/encode_frame_FLP.c
func (e *Encoder) classifyFrame(pcm []float32) (signalType, quantOffset int) {
	if len(pcm) == 0 {
		return 0, 0 // Inactive for empty input
	}

	// ============================================
	// Phase 1: Energy-based activity detection
	// ============================================

	// Compute frame energy and variance
	var sum, sumSq float32
	for _, s := range pcm {
		sum += s
		sumSq += s * s
	}
	n := float32(len(pcm))
	mean := sum / n
	energy := sumSq / n
	rmsEnergy := sqrt32(energy)

	// Compute variance to detect DC signals
	// DC signal has zero variance (all samples are the same)
	variance := energy - mean*mean
	if variance < 0 {
		variance = 0 // Numerical stability
	}
	stdDev := sqrt32(variance)

	// Activity detection thresholds
	const inactiveEnergyThreshold = 1e-4   // Very low energy = inactive
	const inactiveVarianceThreshold = 1e-6 // Very low variance = DC-like = inactive

	// Check for inactive signal (silence or DC)
	if rmsEnergy < inactiveEnergyThreshold {
		return 0, 0 // Inactive: too quiet
	}
	if stdDev < inactiveVarianceThreshold {
		return 0, 0 // Inactive: DC signal (zero variance)
	}

	// DC detection: A true DC signal has all its energy in the mean (variance near zero).
	// For a proper DC signal: variance / energy -> 0
	// For a sine wave: variance / energy = 0.5 (half the energy is AC component)
	// For noise: variance / energy = 1.0 (mean is zero, all energy in variance)
	//
	// We consider a signal "DC-like" if less than 1% of energy is in the AC component.
	// This correctly handles:
	// - Pure DC: variance/energy = 0 -> detected as DC
	// - DC + small noise: variance/energy is small -> detected as DC
	// - Sine wave: variance/energy = 0.5 -> NOT detected as DC
	// - Noise: variance/energy = 1.0 -> NOT detected as DC
	if rmsEnergy > inactiveEnergyThreshold && energy > 1e-10 {
		varianceToEnergyRatio := variance / energy
		if varianceToEnergyRatio < 0.01 {
			// Less than 1% of energy is in AC component = DC signal
			return 0, 0 // Inactive: DC signal
		}
	}

	// ============================================
	// Phase 2: Voiced/Unvoiced classification
	// Default to UNVOICED for active frames (like libopus)
	// ============================================
	signalType = 1 // Default: UNVOICED

	// Compute periodicity using pitch analysis
	config := GetBandwidthConfig(e.bandwidth)
	periodicity := e.computePeriodicity(pcm, config.PitchLagMin, config.PitchLagMax)

	// Compute adaptive threshold for voiced detection (like libopus find_pitch_lags_FLP.c)
	// Base threshold
	voicedThreshold := float32(0.6)

	// Adjust based on LPC order (higher order = more confident, lower threshold)
	voicedThreshold -= float32(0.004) * float32(e.LPCOrder())

	// Adjust based on previous frame type (hysteresis: easier to stay voiced)
	if e.isPreviousFrameVoiced {
		voicedThreshold -= 0.15 // ~0.15 reduction for continuity
	}

	// Spectral tilt adjustment: signals with more high-frequency content are less likely voiced
	spectralTilt := e.computeSpectralTilt(pcm)
	voicedThreshold -= 0.1 * spectralTilt // tilt in [-1, 1]

	// Clamp threshold to reasonable range
	if voicedThreshold < 0.25 {
		voicedThreshold = 0.25
	}
	if voicedThreshold > 0.8 {
		voicedThreshold = 0.8
	}

	// Only classify as VOICED if periodicity exceeds adaptive threshold
	// AND the signal shows genuine periodic structure (not just high autocorrelation)
	if periodicity > voicedThreshold {
		// Additional check: verify it's not just high autocorrelation due to smoothness
		// True voiced signals should have periodicity at pitch lags, not everywhere
		shortTermCorr := e.computeShortTermCorr(pcm)

		// If short-term correlation is very high (>0.99), it might be a smooth signal
		// like a slowly varying DC or very low frequency, not true voiced
		if shortTermCorr < 0.995 || periodicity > 0.8 {
			signalType = 2 // VOICED
		}
	}

	// ============================================
	// Quantization offset selection
	// ============================================
	// Higher offset for cleaner, more tonal signals
	if signalType == 2 && periodicity > 0.7 {
		quantOffset = 1 // High offset for strongly voiced
	} else {
		quantOffset = 0 // Low offset otherwise
	}

	return signalType, quantOffset
}

// computeShortTermCorr computes normalized autocorrelation at lag 1.
// High values (close to 1) indicate smooth, slowly varying signals.
func (e *Encoder) computeShortTermCorr(pcm []float32) float32 {
	if len(pcm) < 2 {
		return 0
	}

	var corr, norm float32
	for i := 1; i < len(pcm); i++ {
		corr += pcm[i] * pcm[i-1]
		norm += pcm[i-1] * pcm[i-1]
	}

	if norm < 1e-10 {
		return 0
	}
	return corr / norm
}

// computeSpectralTilt estimates the spectral tilt of the signal.
// Returns a value in [-1, 1] where:
// - Positive values indicate high-frequency emphasis (noise-like)
// - Negative values indicate low-frequency emphasis (voiced-like)
func (e *Encoder) computeSpectralTilt(pcm []float32) float32 {
	if len(pcm) < 2 {
		return 0
	}

	// Compute first-order prediction coefficient
	// This approximates spectral tilt: a1 > 0 means low-freq emphasis
	var r0, r1 float32
	for i := range pcm {
		r0 += pcm[i] * pcm[i]
		if i > 0 {
			r1 += pcm[i] * pcm[i-1]
		}
	}

	if r0 < 1e-10 {
		return 0
	}

	// First-order prediction coefficient: a1 = r1/r0
	a1 := r1 / r0

	// Clamp to [-1, 1]
	if a1 > 1 {
		a1 = 1
	} else if a1 < -1 {
		a1 = -1
	}

	// Return negative of a1 so positive = high-freq, negative = low-freq
	return -a1
}

// computePeriodicity computes normalized autocorrelation in pitch range.
// Returns max normalized correlation (0 to 1, higher = more periodic/voiced).
// This is used to detect true pitch periodicity at specific pitch lags.
func (e *Encoder) computePeriodicity(pcm []float32, minLag, maxLag int) float32 {
	n := len(pcm)
	if maxLag >= n {
		maxLag = n - 1
	}
	if minLag < 1 {
		minLag = 1
	}
	if minLag > maxLag {
		return 0
	}

	// Only search for periodicity at reasonable pitch lags
	// Avoid very short lags which could pick up sample-to-sample correlation
	if minLag < 16 {
		minLag = 16 // Minimum reasonable pitch lag
	}

	var maxCorr float32 = 0
	var maxCorrLag int = 0

	for lag := minLag; lag <= maxLag; lag++ {
		var corr, energy1, energy2 float32
		for i := lag; i < n; i++ {
			corr += pcm[i] * pcm[i-lag]
			energy1 += pcm[i] * pcm[i]
			energy2 += pcm[i-lag] * pcm[i-lag]
		}

		if energy1 > 1e-10 && energy2 > 1e-10 {
			normCorr := corr / sqrt32(energy1*energy2)
			if normCorr > maxCorr {
				maxCorr = normCorr
				maxCorrLag = lag
			}
		}
	}

	// Verify this is a true pitch peak, not just overall high correlation
	// Check that correlation drops at non-harmonic lags
	if maxCorr > 0.3 && maxCorrLag > 0 {
		// Check correlation at half the lag (should be lower if true pitch)
		halfLag := maxCorrLag / 2
		if halfLag >= minLag {
			var corrHalf, energy1, energy2 float32
			for i := halfLag; i < n; i++ {
				corrHalf += pcm[i] * pcm[i-halfLag]
				energy1 += pcm[i] * pcm[i]
				energy2 += pcm[i-halfLag] * pcm[i-halfLag]
			}
			if energy1 > 1e-10 && energy2 > 1e-10 {
				normCorrHalf := corrHalf / sqrt32(energy1*energy2)
				// If correlation at half lag is almost as high as at full lag,
				// it might not be true pitch (could be DC-like or very smooth signal)
				if normCorrHalf > 0.98*maxCorr && maxCorr < 0.9 {
					// Penalize: correlation doesn't drop at half lag
					maxCorr *= 0.7
				}
			}
		}
	}

	return maxCorr
}
