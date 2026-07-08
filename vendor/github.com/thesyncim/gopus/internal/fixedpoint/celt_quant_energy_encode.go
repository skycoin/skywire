//go:build gopus_fixed_point

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

// This file ports the entropy-coder-driven FIXED_POINT CELT energy quantizers
// from celt/quant_bands.c: quant_coarse_energy (including the two-pass
// intra/inter decision in quant_coarse_energy_impl), quant_fine_energy and
// quant_energy_finalise, plus the laplace_encode counterpart from
// celt/laplace.c. All log-energy values are celt_glog == opus_val32 in
// Q(DB_SHIFT)=Q24. The range encode is delegated to the shared
// rangecoding.Encoder, whose Laplace/ICDF/bit primitives are byte-exact with
// libopus. The probability tables, prediction coefficients and shared helpers
// are reused from celt_unquant_energy.go.

// laplaceEncode ports celt/laplace.c ec_laplace_encode using the shared range
// encoder. fs is the Q15 probability-of-zero and decay is the Q14 decay. value
// is encoded in place: when the magnitude exceeds the representable range
// libopus rewrites *value to the clamped value it actually coded, so the caller
// observes that adjustment. The (possibly adjusted) value is returned.
func laplaceEncode(enc *rangecoding.Encoder, value, fs, decay int) int {
	fl := 0
	val := value
	if val != 0 {
		s := -boolToInt(val < 0)
		val = (val + s) ^ s
		fl = fs
		fs = laplaceGetFreq1(fs, decay)
		// Search the decaying part of the PDF.
		i := 1
		for fs > 0 && i < val {
			fs *= 2
			fl += fs + 2*laplaceMinP
			fs = (fs * decay) >> 15
			i++
		}
		if fs == 0 {
			// Everything beyond that has probability LAPLACE_MINP.
			ndiMax := (laplaceFS - fl + laplaceMinP - 1) >> laplaceLogMinP
			ndiMax = (ndiMax - s) >> 1
			di := imin(val-i, ndiMax-1)
			fl += (2*di + 1 + s) * laplaceMinP
			fs = imin(laplaceMinP, laplaceFS-fl)
			value = (i + di + s) ^ s
		} else {
			fs += laplaceMinP
			fl += fs &^ s
		}
	}
	fh := fl + fs
	if fh > laplaceFS {
		fh = laplaceFS
	}
	enc.EncodeBin(uint32(fl), uint32(fh), laplaceFTBits)
	return value
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// lossDistortion ports celt/quant_bands.c loss_distortion: the Q14 sum of
// squared band-energy prediction errors (channel-major, Q24), clamped to 200.
func lossDistortion(eBands, oldEBands []int32, start, end, length, C int) int32 {
	var dist int32
	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			d := pshr32(eBands[i+c*length]-oldEBands[i+c*length], dbShift-7)
			dist = mac16(dist, int16(d), int16(d))
		}
	}
	return min32(200, shr32(dist, 14))
}

// quantCoarseEnergyImpl ports celt/quant_bands.c quant_coarse_energy_impl. It
// encodes the coarse band energies for bands [start,end) into oldEBands
// (channel-major, Q24) using the entropy encoder, writing the residual
// quantization error into error and returning the accumulated badness (0 when
// lfe). prob is e_prob_model[LM][intra]. tell is the encoder tell at entry.
func quantCoarseEnergyImpl(enc *rangecoding.Encoder, eBands, oldEBands, errOut []int32,
	prob []uint8, start, end, nbEBands, C, LM int, intra bool, budget, tell, maxDecay int32, lfe bool) int {
	badness := 0
	var prev [2]int64

	if tell+3 <= budget {
		enc.EncodeBit(boolToInt(intra), 3)
	}

	var coef, beta int16
	if intra {
		coef = 0
		beta = betaIntra
	} else {
		beta = betaCoef[LM]
		coef = predCoef[LM]
	}

	for i := start; i < end; i++ {
		for c := 0; c < C; c++ {
			idx := i + c*nbEBands
			x := eBands[idx]
			oldE := max32(-gconst(9), oldEBands[idx])
			f := int32(int64(x) - int64(mult16x32q15(coef, oldE)) - prev[c])
			// Rounding to nearest integer here is really important!
			qi := int((f + gconst1Half) >> dbShift)
			decayBound := max32(-gconst(28), oldEBands[idx]-maxDecay)
			// Prevent the energy from going down too quickly (e.g. for bands
			// that have just one bin).
			if qi < 0 && x < decayBound {
				qi += int(shr32(decayBound-x, dbShift))
				if qi > 0 {
					qi = 0
				}
			}
			qi0 := qi
			// If we don't have enough bits to encode all the energy, just
			// assume something safe.
			tell = int32(enc.Tell())
			bitsLeft := int(budget) - int(tell) - 3*C*(end-i)
			if i != start && bitsLeft < 30 {
				if bitsLeft < 24 {
					qi = imin(1, qi)
				}
				if bitsLeft < 16 {
					qi = imax(-1, qi)
				}
			}
			if lfe && i >= 2 {
				qi = imin(qi, 0)
			}
			switch {
			case budget-tell >= 15:
				pi := 2 * imin(i, 20)
				qi = laplaceEncode(enc, qi, int(prob[pi])<<7, int(prob[pi+1])<<6)
			case budget-tell >= 2:
				qi = imax(-1, imin(qi, 1))
				enc.EncodeICDF(2*qi^-boolToInt(qi < 0), smallEnergyICDF, 2)
			case budget-tell >= 1:
				qi = imin(0, qi)
				enc.EncodeBit(-qi, 1)
			default:
				qi = -1
			}
			errOut[idx] = f - shl32(int32(qi), dbShift)
			badness += iabs(qi0 - qi)
			q := int32(int32(qi) << dbShift)

			tmp := int32(int64(mult16x32q15(coef, oldE)) + prev[c] + int64(q))
			tmp = max32(-gconst(28), tmp)
			oldEBands[idx] = tmp
			prev[c] = prev[c] + int64(q) - int64(mult16x32q15(beta, q))
		}
	}
	if lfe {
		return 0
	}
	return badness
}

// QuantCoarseEnergy ports celt/quant_bands.c quant_coarse_energy. It encodes the
// coarse band energies for bands [start,end) given the target log energies
// eBands (channel-major, Q24) and the previous-frame predictor oldEBands
// (updated in place to the reconstructed energies). error receives the residual
// quantization error. delayedIntra is updated in place. The function performs
// the two-pass intra/inter trial and keeps whichever produces lower badness (or
// fewer bits under the intra_bias tie-break).
func QuantCoarseEnergy(enc *rangecoding.Encoder, eBands, oldEBands, errOut []int32,
	start, end, effEnd, nbEBands, C, LM, budget, nbAvailableBytes int,
	forceIntra, twoPass bool, lossRate int, lfe bool, delayedIntra *int32,
	scratch *celtEncodeScratch) {

	intra := forceIntra || (!twoPass && *delayedIntra > int32(2*C*(end-start)) && nbAvailableBytes > (end-start)*C)
	intraBias := int32((int64(budget) * int64(*delayedIntra) * int64(lossRate)) / int64(C*512))
	newDistortion := lossDistortion(eBands, oldEBands, start, effEnd, nbEBands, C)

	tell := int32(enc.Tell())
	if tell+3 > int32(budget) {
		twoPass = false
		intra = false
	}

	maxDecay := gconst(16)
	if end-start > 10 {
		maxDecay = shl32(min32(shr32(maxDecay, dbShift-3), int32(nbAvailableBytes)), dbShift-3)
	}
	if lfe {
		maxDecay = gconst(3)
	}

	var oldEBandsIntra, errorIntra []int32
	var encStartState, encIntraState *rangecoding.EncoderState
	if scratch != nil {
		oldEBandsIntra = ensureInt32(&scratch.qceOldIntra, C*nbEBands)
		errorIntra = ensureInt32(&scratch.qceErrIntra, C*nbEBands)
		encStartState = &scratch.qceEncStart
		encIntraState = &scratch.qceEncIntra
	} else {
		oldEBandsIntra = make([]int32, C*nbEBands)
		errorIntra = make([]int32, C*nbEBands)
		encStartState = &rangecoding.EncoderState{}
		encIntraState = &rangecoding.EncoderState{}
	}
	enc.SaveStateInto(encStartState)
	copy(oldEBandsIntra, oldEBands)

	var badness1 int
	if twoPass || intra {
		badness1 = quantCoarseEnergyImpl(enc, eBands, oldEBandsIntra, errorIntra,
			eProbModel[LM][1][:], start, end, nbEBands, C, LM, true, int32(budget), tell, maxDecay, lfe)
	}

	if !intra {
		tellIntra := int32(enc.TellFrac())
		enc.SaveStateInto(encIntraState)

		enc.RestoreState(encStartState)

		probIdx := 0 // intra == 0 here
		badness2 := quantCoarseEnergyImpl(enc, eBands, oldEBands, errOut,
			eProbModel[LM][probIdx][:], start, end, nbEBands, C, LM, false, int32(budget), tell, maxDecay, lfe)

		if twoPass && (badness1 < badness2 ||
			(badness1 == badness2 && int32(enc.TellFrac())+intraBias > tellIntra)) {
			enc.RestoreState(encIntraState)
			copy(oldEBands, oldEBandsIntra)
			copy(errOut, errorIntra)
			intra = true
		}
	} else {
		copy(oldEBands, oldEBandsIntra)
		copy(errOut, errorIntra)
	}

	if intra {
		*delayedIntra = newDistortion
	} else {
		predSq := mult16x16q15(predCoef[LM], predCoef[LM])
		*delayedIntra = add32(mult16x32q15(predSq, *delayedIntra), newDistortion)
	}
}

// QuantFineEnergy ports celt/quant_bands.c quant_fine_energy. It refines bands
// [start,end) of oldEBands (channel-major, Q24) using extraQuant[i] fine bits
// per band, encoding the quantized residual and updating error in place.
// prevQuant may be nil.
func QuantFineEnergy(enc *rangecoding.Encoder, oldEBands, errOut []int32,
	start, end, nbEBands, C int, prevQuant, extraQuant []int32) {
	for i := start; i < end; i++ {
		if extraQuant[i] <= 0 {
			continue
		}
		extra := int32(1) << uint(extraQuant[i])
		if enc.Tell()+C*int(extraQuant[i]) > enc.Storage()*8 {
			continue
		}
		var prev int32
		if prevQuant != nil {
			prev = prevQuant[i]
		}
		for c := 0; c < C; c++ {
			idx := i + c*nbEBands
			// Has to be without rounding.
			q2 := vshr32(errOut[idx]+shr32(gconst1Half, int(prev)), dbShift-int(extraQuant[i])-int(prev))
			if q2 > extra-1 {
				q2 = extra - 1
			}
			if q2 < 0 {
				q2 = 0
			}
			enc.EncodeRawBits(uint32(q2), uint(extraQuant[i]))
			offset := vshr32(2*q2+1, int(extraQuant[i])-dbShift+1) - gconst1Half
			offset = shr32(offset, int(prev))
			oldEBands[idx] += offset
			errOut[idx] -= offset
		}
	}
}

// QuantEnergyFinalise ports celt/quant_bands.c quant_energy_finalise. It spends
// the remaining bitsLeft refining bands across two priority passes, encoding one
// bit per band/channel and updating oldEBands and error in place.
func QuantEnergyFinalise(enc *rangecoding.Encoder, oldEBands, errOut []int32,
	start, end, nbEBands, C int, fineQuant, finePriority []int32, bitsLeft int) {
	for prio := int32(0); prio < 2; prio++ {
		for i := start; i < end && bitsLeft >= C; i++ {
			if fineQuant[i] >= maxFineBits || finePriority[i] != prio {
				continue
			}
			for c := 0; c < C; c++ {
				idx := i + c*nbEBands
				var q2 int32
				if errOut[idx] >= 0 {
					q2 = 1
				}
				enc.EncodeRawBits(uint32(q2), 1)
				offset := shr32(shl32(q2, dbShift)-gconst1Half, int(fineQuant[i])+1)
				if oldEBands != nil {
					oldEBands[idx] += offset
				}
				errOut[idx] -= offset
				bitsLeft--
			}
		}
	}
}
