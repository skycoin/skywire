// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file implements the spread decision algorithm for the encoder.

package celt

// SpreadingDecision analyzes the normalized MDCT coefficients to decide the
// optimal spread parameter for PVQ quantization.
//
// The spread parameter controls how pulses are distributed across the band:
// - SPREAD_AGGRESSIVE (3): More spreading, better for tonal signals
// - SPREAD_NORMAL (2): Default spreading
// - SPREAD_LIGHT (1): Less spreading
// - SPREAD_NONE (0): No spreading, for very noisy signals
//
// The algorithm counts how many coefficients fall below certain thresholds
// relative to the band energy. Tonal signals have energy concentrated in
// few bins (low counts), while noisy signals have energy spread across
// many bins (high counts).
//
// Parameters:
//   - normX: normalized MDCT coefficients (unit-norm per band)
//   - nbBands: number of bands to analyze
//   - channels: number of audio channels (1 or 2)
//   - frameSize: frame size in samples (determines M scaling)
//   - updateHF: whether to update high-frequency average for tapset decision
//
// Returns: spread decision (0=SPREAD_NONE, 1=SPREAD_LIGHT, 2=SPREAD_NORMAL, 3=SPREAD_AGGRESSIVE)
//
// Reference: libopus celt/bands.c spreading_decision()
func (e *Encoder) SpreadingDecision(normX []celtNorm, nbBands, channels, frameSize int, updateHF bool) int {
	return e.SpreadingDecisionWithWeights(normX, nbBands, channels, frameSize, updateHF, nil)
}

// SpreadingDecisionWithWeights analyzes the normalized MDCT coefficients to decide the
// optimal spread parameter for PVQ quantization, using precomputed spread weights.
//
// The spread parameter controls how pulses are distributed across the band:
// - SPREAD_AGGRESSIVE (3): More spreading, better for tonal signals
// - SPREAD_NORMAL (2): Default spreading
// - SPREAD_LIGHT (1): Less spreading
// - SPREAD_NONE (0): No spreading, for very noisy signals
//
// Parameters:
//   - normX: normalized MDCT coefficients (unit-norm per band)
//   - nbBands: number of bands to analyze
//   - channels: number of audio channels (1 or 2)
//   - frameSize: frame size in samples (determines M scaling)
//   - updateHF: whether to update high-frequency average for tapset decision
//   - spreadWeight: per-band weights from computeSpreadWeights
//
// Returns: spread decision (0=SPREAD_NONE, 1=SPREAD_LIGHT, 2=SPREAD_NORMAL, 3=SPREAD_AGGRESSIVE)
//
// Reference: libopus celt/bands.c spreading_decision()
func (e *Encoder) SpreadingDecisionWithWeights(normX []celtNorm, nbBands, channels, frameSize int, updateHF bool, spreadWeight []int32) int {
	if nbBands <= 0 || len(normX) == 0 {
		return spreadNormal
	}
	// M is the time resolution multiplier (frameSize / shortMdctSize). For the
	// 48 kHz modes shortMdctSize is 120; for the Fs==400*shortMdctSize custom
	// family it is the mode's short-MDCT size (e.scaleBase()).
	base := e.scaleBase()
	M := max(frameSize/base, 1)

	// N0 = total MDCT coefficients per channel (== frameSize).
	N0 := frameSize

	// binFS drives the package ScaledBand* helpers to the libopus eBands[i]<<LM
	// bin edges for the active mode. For the 48 kHz modes binFS == frameSize.
	binFS := Overlap * M

	// Check if the last band is too narrow for spread decision
	// libopus: if (M*(eBands[end]-eBands[end-1]) <= 8) return SPREAD_NONE
	lastBandWidth := ScaledBandWidth(nbBands-1, binFS)
	if lastBandWidth <= 8 {
		return spreadNone
	}

	sum := int32(0)
	nbBandsTotal := int32(0)
	hfSum := int32(0)
	// Process each channel
	for c := range channels {
		// Process each band
		for band := range nbBands {
			// Get band boundaries
			bandStart := ScaledBandStart(band, binFS)
			bandEnd := ScaledBandEnd(band, binFS)
			N := bandEnd - bandStart

			if N <= 8 {
				continue
			}

			// Extract coefficients for this band and channel
			// Coefficients are organized as [ch0_coeffs][ch1_coeffs]
			xOffset := c*N0 + bandStart
			if xOffset+N > len(normX) {
				continue
			}

			// Count coefficients below thresholds
			// libopus uses x2N = x[j]^2 * N, then checks thresholds:
			// - tcount[0]: x2N < 0.25 (|x[j]| < 0.5/sqrt(N))
			// - tcount[1]: x2N < 0.0625 (|x[j]| < 0.25/sqrt(N))
			// - tcount[2]: x2N < 0.015625 (|x[j]| < 0.125/sqrt(N))
			Nf := float32(N)
			bandX := normX[xOffset : xOffset+N : xOffset+N]
			tc0, tc1, tc2 := spreadCountThresholds(bandX, N, Nf)
			tcount := [3]int{tc0, tc1, tc2}

			// High frequency bands contribution (bands above 8kHz).
			// Match libopus: if (i > m->nbEBands-4), where m->nbEBands is 21.
			if band > MaxBands-4 {
				hfSum += int32((32 * (tcount[1] + tcount[0])) / N)
			}

			// Compute tmp: count of thresholds where majority of coeffs are below
			// tmp = (2*tcount[2] >= N) + (2*tcount[1] >= N) + (2*tcount[0] >= N)
			tmp := int32(0)
			if 2*tcount[2] >= N {
				tmp++
			}
			if 2*tcount[1] >= N {
				tmp++
			}
			if 2*tcount[0] >= N {
				tmp++
			}

			weight := int32(1)
			if band < len(spreadWeight) {
				weight = spreadWeight[band]
			}
			sum += tmp * weight
			nbBandsTotal += weight
		}
	}

	// Update high-frequency average for tapset decision
	if updateHF {
		// Match libopus normalization exactly:
		// hf_sum = celt_udiv(hf_sum, C*(4-m->nbEBands+end))
		// with m->nbEBands fixed at 21 and end == nbBands.
		if hfSum > 0 {
			den := channels * (4 - MaxBands + nbBands)
			if den > 0 {
				hfSum = hfSum / int32(den)
			}
		}
		e.hfAverage = (e.hfAverage + hfSum) >> 1

		// Adjust for current tapset decision
		adjustedHF := e.hfAverage
		if e.tapsetDecision == 2 {
			adjustedHF += 4
		} else if e.tapsetDecision == 0 {
			adjustedHF -= 4
		}

		// Update tapset decision with hysteresis
		if adjustedHF > 22 {
			e.tapsetDecision = 2
		} else if adjustedHF > 18 {
			e.tapsetDecision = 1
		} else {
			e.tapsetDecision = 0
		}
	}

	if nbBandsTotal <= 0 {
		return spreadNormal
	}

	// Normalize sum to Q8 (multiply by 256, divide by band count)
	sum = (sum << 8) / nbBandsTotal
	// Recursive averaging with previous
	sum = (sum + e.tonalAverage) >> 1
	e.tonalAverage = sum

	// Apply hysteresis based on last decision
	// libopus: sum = (3*sum + (((3-last_decision)<<7) + 64) + 2)>>2
	sum = (3*sum + ((3 - int32(e.spreadDecision)) << 7) + 64 + 2) >> 2

	// Make decision based on thresholds
	var decision int
	if sum < 80 {
		decision = spreadAggressive
	} else if sum < 256 {
		decision = spreadNormal
	} else if sum < 384 {
		decision = spreadLight
	} else {
		decision = spreadNone
	}

	e.spreadDecision = int32(decision)
	return decision
}

// computeSpreadWeights computes per-band weights for the spread decision.
// Higher weights for perceptually important bands based on masking analysis.
//
// This implements the libopus masking model from dynalloc_analysis():
// 1. Compute noise floor per band (based on logN, lsb_depth, eMeans, preemphasis)
// 2. Compute signal as max(bandLogE) - noise floor
// 3. Apply forward/backward masking propagation
// 4. Compute SMR (signal-to-mask ratio)
// 5. Convert SMR to spread weight: 32 >> clamp(-round(smr), 0, 5)
//
// Parameters:
//   - bandLogE: log-domain band energies (may contain multiple channels: C*nbBands)
//   - nbBands: number of bands per channel
//   - channels: number of audio channels (1 or 2)
//   - lsbDepth: bit depth of input (typically 16 or 24)
//
// Returns: weights per band (higher = more perceptually important)
//
// Reference: libopus celt/celt_encoder.c dynalloc_analysis()
func computeSpreadWeights(bandLogE []celtGLog, nbBands, channels, lsbDepth int) []int32 {
	weights := make([]int32, nbBands)

	// Ensure we have enough band energies
	if len(bandLogE) < nbBands {
		// Default to uniform weights
		for i := range nbBands {
			weights[i] = 1
		}
		return weights
	}

	// Compute noise floor per band
	// libopus: noise_floor[i] = 0.0625*logN[i] + 0.5 + (9-lsb_depth) - eMeans[i] + 0.0062*(i+5)^2
	noiseFloor := make([]float32, nbBands)
	for i := range nbBands {
		logNVal := float32(0.0)
		if i < len(LogN) {
			logNVal = float32(LogN[i])
		}
		eMean := float32(0.0)
		if i < len(eMeans) {
			eMean = float32(eMeans[i])
		}
		// noise_floor = 0.0625*logN + 0.5 + (9-lsb_depth) - eMeans + 0.0062*(i+5)^2
		noiseFloor[i] = 0.0625*logNVal + 0.5 + float32(9-lsbDepth) - eMean + 0.0062*float32((i+5)*(i+5))
	}

	// Compute maxDepth (maximum signal relative to noise floor across all bands/channels)
	maxDepth := float32(-31.9)
	for c := range channels {
		for i := range nbBands {
			idx := c*nbBands + i
			if idx < len(bandLogE) {
				depth := float32(bandLogE[idx]) - noiseFloor[i]
				if depth > maxDepth {
					maxDepth = depth
				}
			}
		}
	}

	// Compute signal mask (max across channels) relative to noise floor
	mask := make([]float32, nbBands)
	sig := make([]float32, nbBands)
	for i := range nbBands {
		mask[i] = float32(bandLogE[i]) - noiseFloor[i]
	}
	if channels == 2 && len(bandLogE) >= 2*nbBands {
		for i := range nbBands {
			ch2Val := float32(bandLogE[nbBands+i]) - noiseFloor[i]
			if ch2Val > mask[i] {
				mask[i] = ch2Val
			}
		}
	}
	copy(sig, mask)

	// Forward masking propagation (lower bands mask higher bands)
	// libopus: mask[i] = max(mask[i], mask[i-1] - 2.0)
	for i := 1; i < nbBands; i++ {
		if mask[i-1]-2.0 > mask[i] {
			mask[i] = mask[i-1] - 2.0
		}
	}

	// Backward masking propagation (higher bands mask lower bands)
	// libopus: mask[i] = max(mask[i], mask[i+1] - 3.0)
	for i := nbBands - 2; i >= 0; i-- {
		if mask[i+1]-3.0 > mask[i] {
			mask[i] = mask[i+1] - 3.0
		}
	}

	// Compute SMR and spread weight for each band
	// libopus: smr = sig[i] - max(0, max(maxDepth-12, mask[i]))
	// spread_weight = 32 >> clamp(-round(smr), 0, 5)
	for i := range nbBands {
		// Compute masking threshold: never more than 72dB below peak, never below noise floor
		maskThresh := mask[i]
		if maxDepth-12.0 > maskThresh {
			maskThresh = maxDepth - 12.0
		}
		if maskThresh < 0 {
			maskThresh = 0
		}

		// SMR = signal - mask threshold
		smr := sig[i] - maskThresh

		// Convert SMR to shift: shift = clamp(-round(smr), 0, 5)
		shift := 0
		if rawShift := float32(0.5) - smr; rawShift > 0 {
			shift = int(rawShift)
		}
		if shift > 5 {
			shift = 5
		}

		// Weight = 32 >> shift
		weights[i] = 32 >> shift
	}

	return weights
}

// computeSpreadWeightsSimple computes spread weights with default parameters.
// This is a convenience wrapper for the common case of mono audio with 16-bit depth.
//
// Parameters:
//   - bandLogE: log-domain band energies
//   - nbBands: number of bands
//
// Returns: weights per band (higher = more important)
func computeSpreadWeightsSimple(bandLogE []celtGLog, nbBands int) []int32 {
	return computeSpreadWeights(bandLogE, nbBands, 1, 16)
}
