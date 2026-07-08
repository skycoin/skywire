package silk

// Gain encoding matching libopus silk/encode_indices.c and silk/gain_quant.c
// Implements proper delta coding with double step size for large gains.

// encodeAbsoluteGainIndex encodes the absolute gain index for first subframe.
// Uses libopus tables: silk_gain_iCDF[signalType] for MSB, silk_uniform8_iCDF for LSB
// Matches libopus silk/encode_indices.c lines 77-79
func (e *Encoder) encodeAbsoluteGainIndex(gainIndex, signalType int) {
	// Clamp to valid range
	if gainIndex < 0 {
		gainIndex = 0
	}
	if gainIndex > nLevelsQGain-1 {
		gainIndex = nLevelsQGain - 1
	}

	// Split into MSB (0-7) and LSB (0-7)
	// MSB = gainIndex >> 3 (divide by 8)
	// LSB = gainIndex & 7 (modulo 8)
	msb := gainIndex >> 3
	lsb := gainIndex & 7

	// Clamp MSB to table size (silk_gain_iCDF tables have 8 symbols)
	if msb > 7 {
		msb = 7
	}

	// Encode MSB using libopus silk_gain_iCDF[signalType]
	// signalType: 0=inactive, 1=unvoiced, 2=voiced
	safeSignalType := signalType
	if safeSignalType < 0 || safeSignalType > 2 {
		safeSignalType = 0
	}
	e.rangeEncoder.EncodeICDF(msb, silk_gain_iCDF[safeSignalType], 8)

	// Encode LSB using libopus silk_uniform8_iCDF
	e.rangeEncoder.EncodeICDF(lsb, silk_uniform8_iCDF, 8)
}

// computeSubframeGains computes gains for each subframe from PCM.
// Returns gains in linear domain matching libopus energy computation.
// Uses scratch buffers for zero-allocation operation.
func (e *Encoder) computeSubframeGains(pcm []float32, numSubframes int) []float32 {
	if len(pcm) == 0 || numSubframes <= 0 {
		gains := ensureFloat32Slice(&e.scratchGains, numSubframes)
		for i := range gains {
			gains[i] = 0
		}
		return gains
	}

	subframeSamples := len(pcm) / numSubframes
	gains := ensureFloat32Slice(&e.scratchGains, numSubframes)

	for sf := range numSubframes {
		start := sf * subframeSamples
		end := min(start+subframeSamples, len(pcm))
		// Compute energy (sum of squares) in int16 scale to match SILK gain
		// quantization. PCM is normalized [-1, 1], so scale to int16 range
		// before RMS while staying in silk_float precision.
		var energy float32
		const pcmScale = float32(32768.0)
		for i := start; i < end; i++ {
			s := pcm[i] * pcmScale
			energy += s * s
		}

		// Normalize by number of samples
		n := end - start
		if n > 0 {
			energy /= float32(n)
		}

		// Convert to RMS gain (int16 amplitude domain)
		if energy > 0 {
			gains[sf] = sqrt32(energy)
		} else {
			gains[sf] = 1.0 // Minimum gain (1 LSB in int16 domain)
		}

		// Ensure minimum gain
		if gains[sf] < 1.0 {
			gains[sf] = 1.0
		}
	}

	return gains
}

// computeSubframeGainsFromResidual computes gains from LPC prediction residual energy.
// This matches libopus behavior where gains are sqrt(residual_energy), not sqrt(input_energy).
// The residual energy is computed during LPC (Burg) analysis and stored in encoder state.
//
// Key insight: libopus computes gains from the Schur/Burg residual energy, which is
// much smaller than the raw signal energy because LPC prediction removes the predictable
// component. This keeps gains within the quantizable range (max ~636 linear).
//
// Uses scratch buffers for zero-allocation operation.
func (e *Encoder) computeSubframeGainsFromResidual(pcm []float32, numSubframes int) []float32 {
	if len(pcm) == 0 || numSubframes <= 0 {
		gains := ensureFloat32Slice(&e.scratchGains, numSubframes)
		for i := range gains {
			gains[i] = 1.0
		}
		return gains
	}

	gains := ensureFloat32Slice(&e.scratchGains, numSubframes)

	// Get residual energy from LPC analysis (set by burgModifiedFLPZeroAlloc)
	// residualEnergy = C0 * invGain where C0 is total energy and invGain is inverse prediction gain
	totalEnergy := e.lastTotalEnergy
	invGain := e.lastInvGain
	numSamples := int(e.lastNumSamples)

	if numSamples <= 0 || totalEnergy <= 0 || invGain <= 0 {
		// Fallback to minimum gain if LPC analysis data not available
		for i := range gains {
			gains[i] = 1.0
		}
		return gains
	}

	// Compute average residual energy per sample
	// residualEnergy = totalEnergy * invGain
	// averageResidualEnergy = residualEnergy / numSamples
	residualEnergy := totalEnergy * invGain
	avgResidualPerSample := residualEnergy / float32(numSamples)

	// Each subframe gets approximately the same average residual energy
	// Gain = sqrt(avgResidualPerSample) which is the RMS of the prediction residual
	subframeSamples := len(pcm) / numSubframes
	for sf := range numSubframes {
		// Compute per-subframe energy for more accurate per-subframe gains
		// Scale the average by subframe-specific energy ratio
		start := sf * subframeSamples
		end := min(start+subframeSamples, len(pcm))

		// Compute subframe energy to scale the average residual
		var subframeEnergy float32
		const pcmScale = float32(32768.0)
		for i := start; i < end; i++ {
			s := pcm[i] * pcmScale
			subframeEnergy += s * s
		}
		n := end - start
		if n > 0 {
			subframeEnergy /= float32(n)
		}

		// Scale residual by ratio of subframe energy to average frame energy
		avgFrameEnergy := totalEnergy / float32(numSamples)
		if avgFrameEnergy > 0 {
			// Subframe residual energy ≈ avgResidualPerSample * (subframeEnergy / avgFrameEnergy)
			subframeResidual := avgResidualPerSample * (subframeEnergy / avgFrameEnergy)
			gains[sf] = sqrt32(subframeResidual)
		} else {
			gains[sf] = sqrt32(avgResidualPerSample)
		}

		// Ensure minimum gain (1 LSB in int16 domain)
		if gains[sf] < 1.0 {
			gains[sf] = 1.0
		}
		// Cap at maximum (libopus caps at 32767.0)
		if gains[sf] > 32767.0 {
			gains[sf] = 32767.0
		}
	}

	return gains
}

// computeSubframeGainsFromLPCResidual computes gains from LPC residual energy using
// the provided LPC coefficients. This more closely matches libopus by measuring
// residual energy directly from the analysis filter instead of Burg's inverse gain.
// computeLogGainIndexQ16 converts Q16 linear gain to log gain index [0, 63].
// Uses the libopus logarithmic quantization.
func computeLogGainIndexQ16(gainQ16 int32) int {
	if gainQ16 <= 0 {
		return 0
	}

	// Use libopus log2lin/lin2log for accurate conversion
	logGain := silkLin2Log(gainQ16)

	// Scale to index: ind = SMULWB(SCALE_Q16, logGain - OFFSET)
	ind := silkSMULWB(int32(gainScaleQ16), logGain-int32(gainOffsetQ7))

	// Clamp to valid range
	if ind < 0 {
		return 0
	}
	if ind > nLevelsQGain-1 {
		return nLevelsQGain - 1
	}

	return int(ind)
}

// computeLogGainIndex converts linear gain to log gain index [0, 63].
// Uses the libopus logarithmic quantization.
func computeLogGainIndex(gain float32) int {
	if gain <= 0 {
		return 0
	}

	// Convert float gain to Q16
	gainQ16 := int32(gain * 65536.0)

	return computeLogGainIndexQ16(gainQ16)
}
