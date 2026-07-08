// Package celt implements the CELT encoder/decoder per RFC 6716 Section 4.3.
// This file implements band energy quantization matching libopus quant_bands.c.

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// predCoef contains inter-frame energy prediction coefficients.
// Index by LM: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms.
// These are the same values as AlphaCoef but in a form matching libopus naming.
// Reference: libopus celt/quant_bands.c pred_coef[4]
var predCoef = [4]float32{
	29440.0 / 32768.0, // 0.8984375
	26112.0 / 32768.0, // 0.796875
	21248.0 / 32768.0, // 0.6484375
	16384.0 / 32768.0, // 0.5
}

// betaCoef contains inter-band prediction decay coefficients for INTER frames.
// Index by LM: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms.
// Reference: libopus celt/quant_bands.c beta_coef[4]
var betaCoef = [4]float32{
	30147.0 / 32768.0, // 0.9200744
	22282.0 / 32768.0, // 0.6800537
	12124.0 / 32768.0, // 0.3700561
	6554.0 / 32768.0,  // 0.2000122
}

// betaIntraF32 is the inter-band prediction decay for INTRA frames.
// Reference: libopus celt/quant_bands.c beta_intra
const betaIntraF32 = float32(4915.0 / 32768.0) // 0.15

// QuantCoarseEnergyResult holds the result of coarse energy quantization.
type QuantCoarseEnergyResult struct {
	// QuantizedEnergy is the quantized energy per band per channel.
	// Layout: [ch0_band0, ch0_band1, ..., ch1_band0, ch1_band1, ...]
	QuantizedEnergy []celtGLog

	// Error is the quantization error per band per channel (for fine energy).
	// error[i] = original - quantized (in DB6 units)
	Error []celtGLog

	// Intra indicates whether intra mode was used.
	Intra bool
}

// QuantCoarseEnergyParams holds parameters for coarse energy quantization.
type QuantCoarseEnergyParams struct {
	// Start is the first band to encode (typically 0, or 17 for hybrid).
	Start int
	// End is the last band to encode (exclusive).
	End int
	// EffEnd is the effective end for distortion computation.
	EffEnd int
	// LM is the log mode (0=2.5ms, 1=5ms, 2=10ms, 3=20ms).
	LM int
	// Channels is the number of audio channels (1 or 2).
	Channels int
	// Budget is the total bit budget for encoding.
	Budget int
	// NBAvailableBytes is the number of bytes available for encoding.
	NBAvailableBytes int
	// ForceIntra forces intra mode regardless of analysis.
	ForceIntra bool
	// TwoPass enables two-pass encoding comparing intra vs inter.
	TwoPass bool
	// LossRate is the packet loss rate (0-100).
	LossRate int
	// LFE indicates low-frequency effects mode.
	LFE bool
}

// quantCoarseEnergyImpl implements the core coarse energy quantization loop.
// This matches libopus quant_coarse_energy_impl() in quant_bands.c.
//
// The function quantizes energies relative to a prediction based on:
// 1. Previous frame energy (inter-frame prediction, weighted by coef)
// 2. Previous band energy within the frame (inter-band prediction, decayed by beta)
//
// Each quantization step is 1 unit = DB6 (6 dB).
func quantCoarseEnergyImpl(
	re *rangecoding.Encoder,
	start, end int,
	eBands []celtGLog,
	oldEBands []celtGLog,
	budget, tell int,
	probModel []uint8,
	error []celtGLog,
	channels, lm int,
	intra bool,
	maxDecay float32,
	lfe bool,
	nbEBands int,
) int {
	badness := 0

	// Get prediction coefficients
	var coef, beta float32
	if intra {
		coef = 0
		beta = betaIntraF32
	} else {
		beta = betaCoef[lm]
		coef = predCoef[lm]
	}

	// Per-channel inter-band prediction state
	prev := [2]float32{0, 0}

	if nbEBands <= 0 {
		nbEBands = MaxBands
	}

	for i := start; i < end; i++ {
		for c := range channels {
			idx := c*nbEBands + i

			// Current energy to encode
			x := eBands[idx]

			// Previous frame energy for prediction (clamped to minimum)
			oldE := oldEBands[idx]
			minEnergy := float32(-9.0)
			if oldE < minEnergy {
				oldE = minEnergy
			}

			// Compute prediction residual: f = x - coef*oldE - prev[c]
			f := x - coef*oldE - prev[c]

			// Quantize residual: round to nearest integer
			// qi = floor(f + 0.5)
			qi := floor32ToInt(f + 0.5)

			// Compute decay bound to prevent energy from dropping too quickly
			// decay_bound = max(-28, oldEBands[i]) - max_decay
			oldEBandVal := oldEBands[idx]
			decayBound := oldEBandVal
			minDecay := float32(-28.0)
			if decayBound < minDecay {
				decayBound = minDecay
			}
			decayBound -= maxDecay

			// Prevent energy from going down too quickly
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x))
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			// Save original qi for badness computation
			qi0 := qi

			// Check bit budget constraints
			tellNow := re.Tell()
			bitsLeft := budget - tellNow - 3*channels*(end-i)
			remaining := budget - tellNow
			if i != start && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}

			// LFE mode constraint: bands >= 2 can only decrease
			if lfe && i >= 2 && qi > 0 {
				qi = 0
			}

			// Encode the quantized value
			if remaining >= 15 {
				// Use Laplace encoding
				pi := 2 * i
				if pi > 40 {
					pi = 40
				}
				fs := int(probModel[pi]) << 7
				decay := int(probModel[pi+1]) << 6
				qi = encodeLaplaceEnergy(re, qi, fs, decay)
			} else if remaining >= 2 {
				// Clamp to small range
				if qi > 1 {
					qi = 1
				}
				if qi < -1 {
					qi = -1
				}
				// Zigzag encoding: qi -> symbol
				// qi=0 -> s=0, qi=-1 -> s=1, qi=1 -> s=2
				var s int
				if qi < 0 {
					s = -2*qi - 1
				} else {
					s = 2 * qi
				}
				re.EncodeICDF(s, smallEnergyICDF, 2)
			} else if remaining >= 1 {
				if qi > 0 {
					qi = 0
				}
				re.EncodeBit(-qi, 1)
			} else {
				qi = -1
			}

			// Store quantization error for fine energy
			error[idx] = celtGLog(f - float32(qi))

			// Accumulate badness
			if qi0 > qi {
				badness += qi0 - qi
			} else {
				badness += qi - qi0
			}

			// Compute quantized energy
			q := float32(qi)
			tmp := coef*oldE + prev[c] + q

			// Store quantized energy
			oldEBands[idx] = celtGLog(tmp)

			// Update inter-band predictor
			prev[c] = prev[c] + q - beta*q
		}
	}

	if lfe {
		return 0
	}
	return badness
}

// encodeLaplaceEnergy encodes a Laplace-distributed value for coarse energy.
// This matches libopus ec_laplace_encode().
func encodeLaplaceEnergy(re *rangecoding.Encoder, val int, fs int, decay int) int {
	fl := 0
	if val != 0 {
		s := 0
		if val < 0 {
			s = -1
		}
		absVal := (val + s) ^ s
		fl = fs
		fs = ec_laplace_get_freq1(fs, decay)
		i := 1
		for fs > 0 && i < absVal {
			fs *= 2
			fl += fs + 2*laplaceMinP
			fs = (fs * decay) >> 15
			i++
		}
		if fs == 0 {
			ndiMax := (laplaceFS - fl + laplaceMinP - 1) >> laplaceLogMinP
			ndiMax = (ndiMax - s) >> 1
			di := absVal - i
			if di > ndiMax-1 {
				di = ndiMax - 1
			}
			fl += (2*di + 1 + s) * laplaceMinP
			if laplaceFS-fl < laplaceMinP {
				fs = laplaceFS - fl
			} else {
				fs = laplaceMinP
			}
			absVal = i + di
			val = (absVal + s) ^ s
		} else {
			fs += laplaceMinP
			if s == 0 {
				fl += fs
			}
		}
	}
	if fl+fs > laplaceFS {
		fs = laplaceFS - fl
	}
	re.EncodeBin(uint32(fl), uint32(fl+fs), laplaceFTBits)
	return val
}

// QuantCoarseEnergy quantizes coarse band energies with two-pass comparison.
// This is the main entry point matching libopus quant_coarse_energy().
//
// The algorithm:
// 1. Optionally encode with intra mode (no inter-frame prediction)
// 2. Encode with inter mode (using inter-frame prediction)
// 3. Compare results and pick the better one (when two_pass is enabled)
//
// Reference: libopus celt/quant_bands.c quant_coarse_energy()
func QuantCoarseEnergy(
	re *rangecoding.Encoder,
	eBands []celtGLog,
	oldEBands []celtGLog,
	params QuantCoarseEnergyParams,
	delayedIntra *float32,
) QuantCoarseEnergyResult {
	result := QuantCoarseEnergyResult{
		QuantizedEnergy: make([]celtGLog, params.Channels*MaxBands),
		Error:           make([]celtGLog, params.Channels*MaxBands),
	}

	// Copy input oldEBands to output (we'll modify it)
	copy(result.QuantizedEnergy, oldEBands)

	// Compute parameters
	budget := params.Budget
	tell := re.Tell()
	lm := max(params.LM, 0)
	if lm > 3 {
		lm = 3
	}

	start := params.Start
	end := params.End
	effEnd := params.EffEnd
	channels := params.Channels
	twoPass := params.TwoPass
	forceIntra := params.ForceIntra

	// Determine initial intra decision
	intra := forceIntra
	if !twoPass && !intra {
		// Simple decision based on delayedIntra threshold
		if *delayedIntra > 2.0*float32(channels*(end-start)) && params.NBAvailableBytes > (end-start)*channels {
			intra = true
		}
	}

	// Check if we have enough bits for intra flag
	if tell+3 > budget {
		twoPass = false
		intra = false
	}

	// Compute max_decay
	maxDecay := float32(16.0)
	if end-start > 10 {
		limit := 0.125 * float32(params.NBAvailableBytes)
		if limit < maxDecay {
			maxDecay = limit
		}
	}
	if params.LFE {
		maxDecay = 3.0
	}

	// Compute new distortion for delayed intra update
	newDistortion := lossDistortion(eBands, oldEBands, start, effEnd, MaxBands, channels)

	// Get probability model
	probIntra := eProbModel[lm][1]
	probInter := eProbModel[lm][0]

	if twoPass || intra {
		// Save encoder state
		encStartState := re.SaveState()

		// Allocate temporary arrays for intra encoding
		oldEBandsIntra := make([]celtGLog, channels*MaxBands)
		errorIntra := make([]celtGLog, channels*MaxBands)
		copy(oldEBandsIntra, oldEBands)

		// Try intra encoding first
		badness1 := quantCoarseEnergyImpl(
			re, start, end,
			eBands, oldEBandsIntra,
			budget, tell,
			probIntra[:],
			errorIntra,
			channels, lm,
			true, // intra
			maxDecay,
			params.LFE,
			MaxBands,
		)

		if !intra {
			// Try inter encoding as well and compare
			tellIntra := re.TellFrac()
			encIntraState := re.SaveState()

			// Restore to start state for inter encoding
			re.RestoreStateShallow(encStartState)

			// Try inter encoding
			errorInter := make([]celtGLog, channels*MaxBands)
			badness2 := quantCoarseEnergyImpl(
				re, start, end,
				eBands, result.QuantizedEnergy,
				budget, tell,
				probInter[:],
				errorInter,
				channels, lm,
				false, // inter
				maxDecay,
				params.LFE,
				MaxBands,
			)
			copy(result.Error, errorInter)

			// Compare and choose better encoding
			intraBias := int((float32(budget) * *delayedIntra * float32(params.LossRate)) / float32(channels*512))
			tellInter := re.TellFrac()

			if badness1 < badness2 || (badness1 == badness2 && tellInter+intraBias > tellIntra) {
				// Intra was better, restore intra state
				re.RestoreState(encIntraState)
				copy(result.QuantizedEnergy, oldEBandsIntra)
				copy(result.Error, errorIntra)
				intra = true
			}
		} else {
			// Only intra was tried
			copy(result.QuantizedEnergy, oldEBandsIntra)
			copy(result.Error, errorIntra)
		}
	} else {
		// Only inter encoding
		errorInter := make([]celtGLog, channels*MaxBands)
		_ = quantCoarseEnergyImpl(
			re, start, end,
			eBands, result.QuantizedEnergy,
			budget, tell,
			probInter[:],
			errorInter,
			channels, lm,
			false, // inter
			maxDecay,
			params.LFE,
			MaxBands,
		)
		copy(result.Error, errorInter)
	}

	// Update delayedIntra
	if intra {
		*delayedIntra = newDistortion
	} else {
		alpha := predCoef[lm] * predCoef[lm]
		*delayedIntra = alpha**delayedIntra + newDistortion
	}

	result.Intra = intra
	return result
}

// lossDistortion computes the distortion for the loss_distortion function.
// This is used to decide whether to use intra or inter mode.
// Reference: libopus celt/quant_bands.c loss_distortion()
func lossDistortion(eBands, oldEBands []celtGLog, start, end, nbEBands, channels int) float32 {
	var dist float32
	for c := range channels {
		for i := start; i < end; i++ {
			d := eBands[c*nbEBands+i] - oldEBands[c*nbEBands+i]
			dist += d * d
		}
	}
	// Scale and clamp
	dist /= 128.0
	if dist > 200 {
		dist = 200
	}
	return dist
}

// QuantFineEnergy encodes fine energy refinement bits.
// This matches libopus quant_fine_energy() in quant_bands.c.
//
// Parameters:
//   - re: range encoder
//   - start, end: band range to encode
//   - oldEBands: quantized energies (will be updated with fine refinement)
//   - error: quantization error from coarse encoding (will be updated)
//   - prevQuant: previous quantization bits per band (can be nil)
//   - extraQuant: fine bits per band (0 = no refinement)
//   - channels: number of audio channels
//
// Reference: libopus celt/quant_bands.c quant_fine_energy()
func QuantFineEnergy(
	re *rangecoding.Encoder,
	start, end int,
	oldEBands, errorVal []celtGLog,
	prevQuant, extraQuant []int,
	channels int,
) {
	nbEBands := MaxBands

	for i := start; i < end; i++ {
		extra := extraQuant[i]
		if extra <= 0 {
			continue
		}

		// Check budget
		if re.Tell()+channels*extra > re.StorageBits() {
			continue
		}

		prev := 0
		if prevQuant != nil && i < len(prevQuant) {
			prev = prevQuant[i]
		}

		// extra is the number of bits, so 2^extra is the number of levels
		extraLevels := 1 << extra

		for c := range channels {
			idx := c*nbEBands + i

			// Quantize error to extra_quant[i] bits
			// libopus float: q2 = (int)floor((error[i+c*m->nbEBands]*(1<<prev)+.5f)*extra)
			// where extra = 1 << extra_quant[i]
			scaledError := errorVal[idx]*float32(uint(1)<<prev) + 0.5
			q2 := floor32ToInt(scaledError * float32(extraLevels))

			// Clamp to valid range
			if q2 > extraLevels-1 {
				q2 = extraLevels - 1
			}
			if q2 < 0 {
				q2 = 0
			}

			// Encode the bits
			re.EncodeRawBits(uint32(q2), uint(extra))

			// Compute offset and update energies
			// libopus float: offset = (q2+.5f)*(1<<(14-extra_quant[i]))*(1.f/16384) - .5f
			//                offset *= (1<<(14-prev))*(1.f/16384)
			offset := (float32(q2)+0.5)*float32(uint(1)<<(14-extra))*(1.0/16384.0) - 0.5
			offset *= float32(uint(1)<<(14-prev)) * (1.0 / 16384.0)

			oldEBands[idx] += offset
			errorVal[idx] -= offset
		}
	}
}

// QuantEnergyFinalise uses remaining bits for additional energy refinement.
// This matches libopus quant_energy_finalise() in quant_bands.c.
//
// Parameters:
//   - re: range encoder
//   - start, end: band range to encode
//   - oldEBands: quantized energies (will be updated)
//   - error: quantization error (will be updated)
//   - fineQuant: fine bits already used per band
//   - finePriority: priority for finalization (0 or 1)
//   - bitsLeft: remaining bits to use
//   - channels: number of audio channels
//
// Reference: libopus celt/quant_bands.c quant_energy_finalise()
func QuantEnergyFinalise(
	re *rangecoding.Encoder,
	start, end int,
	oldEBands, errorVal []celtGLog,
	fineQuant, finePriority []int,
	bitsLeft, channels int,
) {
	nbEBands := MaxBands

	// Use up the remaining bits in two priority passes
	for prio := range 2 {
		for i := start; i < end && bitsLeft >= channels; i++ {
			// Skip if already at max fine bits or different priority
			if fineQuant[i] >= maxFineBits || finePriority[i] != prio {
				continue
			}

			// Check if we have enough bits for all channels of this band
			if bitsLeft < channels {
				break
			}

			for c := range channels {
				idx := c*nbEBands + i

				// Check if we have at least 1 bit left
				if bitsLeft <= 0 {
					break
				}

				// Encode 1 bit based on error sign
				q2 := 0
				err := errorVal[idx]
				if err >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				// Compute offset
				// libopus float: offset = (q2-.5f)*(1<<(14-fine_quant[i]-1))*(1.f/16384)
				offset := (float32(q2) - 0.5) * float32(uint(1)<<(14-fineQuant[i]-1)) * (1.0 / 16384.0)

				oldEBands[idx] += offset
				errorVal[idx] = err - offset
				bitsLeft--
			}
		}
	}
}

// Amp2Log2 converts amplitude (sqrt energy) to log2 format for quantization.
// This matches libopus amp2Log2() in quant_bands.c.
//
// The conversion:
//
//	bandLogE[i] = celt_log2_db(bandE[i]) - eMeans[i]
//
// Parameters:
//   - bandE: band amplitudes (sqrt energy)
//   - effEnd: effective end band
//   - channels: number of channels
//
// Returns: bandLogE values suitable for quantization
//
// Reference: libopus celt/quant_bands.c amp2Log2()
func Amp2Log2(bandE []celtEner, effEnd, end, channels int) []celtGLog {
	nbEBands := MaxBands
	bandLogE := make([]celtGLog, channels*nbEBands)

	for c := range channels {
		for i := range effEnd {
			// Convert amplitude to log2
			// log2(amplitude) = 0.5 * log2(energy)
			amp := float32(bandE[c*nbEBands+i])
			if amp < 1e-27 {
				amp = 1e-27
			}
			logE := celtLog2(amp)

			// Subtract eMeans (in DB_SHIFT=14 format, but we work in float)
			// eMeans is stored in log2 units (not Q format)
			if i < len(eMeans) {
				logE -= float32(eMeans[i])
			}

			bandLogE[c*nbEBands+i] = celtGLog(logE)
		}
		// Fill remaining bands with silence level
		for i := effEnd; i < end; i++ {
			bandLogE[c*nbEBands+i] = celtGLog(-14.0)
		}
	}

	return bandLogE
}
