package silk

// Gain quantization matching libopus silk/gain_quant.c
// Implements logarithmic gain quantization with hysteresis and delta coding.
// This file contains the encoder-side functions (silkGainsQuant).
// The decoder-side functions (silkGainsDequant) are in libopus_gain.go.
// The log conversion functions (silkLin2Log, silkLog2Lin) are in libopus_log.go.

// Constants for gain quantization
// Using the constants from libopus_consts.go and libopus_gain.go:
// nLevelsQGain = 64, maxDeltaGainQuant = 36, minDeltaGainQuant = -4
// minQGainDb = 2, maxQGainDb = 88
// gainOffsetQ7 = (minQGainDb*128)/6 + 16*128 = 2090
// invScaleQ16Val = (1 << 16) * qgainRangeQ7 / (nLevelsQGain - 1)

// Derived constants matching libopus silk/gain_quant.c:
// SCALE_Q16 = (65536 * (N_LEVELS_QGAIN - 1)) / (((MAX_QGAIN_DB - MIN_QGAIN_DB) * 128) / 6)
//
//	= (65536 * 63) / ((86 * 128) / 6) = 4128768 / 1834 = 2251
const (
	gainScaleQ16 = (65536 * (nLevelsQGain - 1)) / (((maxQGainDb - minQGainDb) * 128) / 6)
)

// silkGainsQuantInto quantizes gains matching libopus silk/gain_quant.c:silk_gains_quant
// Parameters:
//   - ind: output buffer for gain indices (must have length >= nbSubfr)
//   - gainQ16: input gains in Q16 format, will be modified to quantized values
//   - prevInd: previous index from last frame
//   - conditional: if true, first gain is delta-coded from prevInd
//   - nbSubfr: number of subframes
//
// Returns:
//   - newPrevInd: updated previous index for next frame
func silkGainsQuantInto(ind []int8, gainQ16 []int32, prevInd int8, conditional bool, nbSubfr int) int8 {
	currentPrevInd := int(prevInd)

	for k := range nbSubfr {
		// Convert to log scale, scale, floor()
		logGain := silkLin2Log(gainQ16[k])

		rawInd := silkSMULWB(int32(gainScaleQ16), logGain-int32(gainOffsetQ7))

		// Round towards previous quantized gain (hysteresis)
		if int(rawInd) < currentPrevInd {
			rawInd++
		}
		rawInd = silkLimit32(rawInd, 0, nLevelsQGain-1)
		ind[k] = int8(rawInd)

		// Compute delta indices and limit
		if k == 0 && !conditional {
			// Full index - limit to not go down too fast
			ind[k] = int8(silkLimitInt(int(ind[k]), currentPrevInd+minDeltaGainQuant, nLevelsQGain-1))
			currentPrevInd = int(ind[k])
		} else {
			// Delta index
			delta := int(ind[k]) - currentPrevInd

			// Double the quantization step size for large gain increases
			doubleStepThreshold := 2*maxDeltaGainQuant - nLevelsQGain + currentPrevInd
			if delta > doubleStepThreshold {
				delta = doubleStepThreshold + ((delta - doubleStepThreshold + 1) >> 1)
			}

			delta = silkLimitInt(delta, minDeltaGainQuant, maxDeltaGainQuant)
			ind[k] = int8(delta)

			// Accumulate deltas
			// Match libopus: in the double-step branch, only apply upper-bound clamp.
			// In the normal branch, NO clamping — prev_ind can go negative.
			// The dequant function clamps to [0, N_LEVELS_QGAIN-1], but the encoder
			// quantizer intentionally allows negative prev_ind to produce lower gains.
			if int(ind[k]) > doubleStepThreshold {
				currentPrevInd += 2*int(ind[k]) - doubleStepThreshold
				if currentPrevInd > nLevelsQGain-1 {
					currentPrevInd = nLevelsQGain - 1
				}
			} else {
				currentPrevInd += int(ind[k])
			}

			// Shift to make non-negative (for encoding)
			ind[k] -= int8(minDeltaGainQuant)
		}

		// Scale and convert back to linear scale for NSQ
		// Per libopus gain_quant.c line 89:
		// gain_Q16[k] = silk_log2lin(silk_min_32(silk_SMULWB(INV_SCALE_Q16, *prev_ind) + OFFSET, 3967))
		// Note: NO minimum clamping after log2lin - gains can be < 1.0
		logQ7 := min(silkSMULWB(int32(invScaleQ16Val), int32(currentPrevInd))+int32(gainOffsetQ7), 3967)
		gainQ16[k] = silkLog2Lin(logQ7)
		// DO NOT clamp to minimum - libopus allows gains < 1.0
	}

	return int8(currentPrevInd)
}

// silkGainsID computes a unique identifier for a gains index vector.
// Matches libopus silk_gains_ID in gain_quant.c.
func silkGainsID(ind []int8, nbSubfr int) int32 {
	var gainsID int32
	for k := 0; k < nbSubfr && k < len(ind); k++ {
		gainsID = silkADD_LSHIFT32(int32(ind[k]), gainsID, 8)
	}
	return gainsID
}

// Note: silkLimitInt is defined in libopus_fixed.go
// Note: silkLimit32 is defined in libopus_fixed.go

// GainQ16FromPCM computes Q16 gain from PCM samples using libopus method.
// This computes the subframe energy and converts to gain matching libopus.
//
// libopus flow (silk/float/process_gains_FLP.c):
// 1. Gains[k] = sqrt(residual_energy) -> linear amplitude in int16 range (0-32767)
// 2. pGains_Q16[k] = Gains[k] * 65536.0f -> Q16 format
//
// For raw PCM (without LPC prediction), gain = RMS amplitude of signal.
func GainQ16FromPCM(pcm []int16, subframeSamples int) int32 {
	if len(pcm) == 0 || subframeSamples <= 0 {
		return 1 << 16 // Default gain of 1.0
	}

	// Compute sum of squares
	var sumSq int64
	n := min(subframeSamples, len(pcm))

	for i := 0; i < n; i++ {
		val := int64(pcm[i])
		sumSq += val * val
	}

	// Compute RMS and scale to Q16
	// rms = sqrt(sumSq / n) -> linear amplitude (int16 domain, 0-32767)
	// gain_Q16 = rms * 65536 -> Q16 format
	if sumSq == 0 {
		return 1 << 16
	}

	// Average energy (variance)
	energyQ0 := sumSq / int64(n)
	if energyQ0 <= 0 {
		return 1 << 16
	}

	// Compute sqrt in log domain for accuracy:
	// log(gain) = log(energy) / 2
	// gainLinear = exp(log(gain)) = sqrt(energy)
	logEnergy := silkLin2Log(int32(energyQ0))
	logGain := logEnergy >> 1 // Divide by 2 for sqrt

	// silkLog2Lin returns LINEAR value (the actual amplitude, e.g., 16384)
	gainLinear := silkLog2Lin(logGain)

	// Convert linear gain to Q16: gainQ16 = gainLinear * 65536
	// This matches libopus: pGains_Q16[k] = Gains[k] * 65536.0f
	gainQ16_64 := min(int64(gainLinear)<<16,
		// Clamp to max int32
		0x7FFFFFFF)
	gainQ16 := int32(gainQ16_64)

	// Clamp to minimum of 1.0 in Q16 (65536)
	if gainQ16 < (1 << 16) {
		gainQ16 = 1 << 16
	}

	return gainQ16
}
