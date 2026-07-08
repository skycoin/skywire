//go:build gopus_fixed_point

package fixedpoint

// This file ports the still-missing FIXED_POINT encode-side glue kernels from
// libopus celt/celt_encoder.c and celt/bands.c that celt_encode_with_ec needs
// between the energy quantisers and the bit allocator:
//
//   - tone_detect / tone_lpc / normalize_tone_input / acos_approx: the pure-tone
//     detector that produces tone_freq (Q13) and toneishness (Q29), feeding
//     transient_analysis, run_prefilter and dynalloc_analysis.
//   - median_of_5 / median_of_3 / dynalloc_analysis: the per-band dynamic
//     allocation boost offsets, importance and spread_weight.
//   - spreading_decision: the spread/tapset estimator.
//
// celt_glog / opus_val32 are int32 (DB_SHIFT == 24); opus_val16 is int16. The
// MAXG/MING macros over celt_glog are max32/min32.

// gconstF evaluates the FIXED_POINT GCONST(x) macro: (celt_glog)(.5+x*(1<<24)).
// The cast truncates toward zero, matching the C compile-time constant.
func gconstF(x float64) int32 {
	v := 0.5 + x*float64(int32(1)<<dbShift)
	return int32(v)
}

// median_of_5 ports celt/celt_encoder.c median_of_5 over celt_glog (int32).
func medianOf5(x []int32) int32 {
	t2 := x[2]
	var t0, t1, t3, t4 int32
	if x[0] > x[1] {
		t0, t1 = x[1], x[0]
	} else {
		t0, t1 = x[0], x[1]
	}
	if x[3] > x[4] {
		t3, t4 = x[4], x[3]
	} else {
		t3, t4 = x[3], x[4]
	}
	if t0 > t3 {
		t0, t3 = t3, t0
		t1, t4 = t4, t1
	}
	if t2 > t1 {
		if t1 < t3 {
			return min32(t2, t3)
		}
		return min32(t4, t1)
	}
	if t2 < t3 {
		return min32(t1, t3)
	}
	return min32(t2, t4)
}

// median_of_3 ports celt/celt_encoder.c median_of_3 over celt_glog (int32).
func medianOf3(x []int32) int32 {
	var t0, t1 int32
	if x[0] > x[1] {
		t0, t1 = x[1], x[0]
	} else {
		t0, t1 = x[0], x[1]
	}
	t2 := x[2]
	if t1 < t2 {
		return t1
	} else if t0 < t2 {
		return t2
	}
	return t0
}

// DynallocAnalysis ports celt/celt_encoder.c dynalloc_analysis (FIXED_POINT,
// surround masking and float analysis disabled: surroundDynalloc is all zero and
// analysis is invalid, matching a plain CELT encoder). It fills offsets[start,end)
// with the per-band dynalloc boosts, importance[start,end) and spread_weight[0,end),
// returns maxDepth (celt_glog) and writes the total boost into totBoost.
//
//	bandLogE/bandLogE2/oldBandE  channel-major Q24 log energies (length C*nbEBands).
//	logN     mode->logN (Q5), eBands mode->eBands.
//	toneFreq Q13, toneishness Q29.
func DynallocAnalysis(bandLogE, bandLogE2, oldBandE []int32, nbEBands, start, end, C int,
	offsets []int, lsbDepth int, logN []int16, isTransient, vbr, constrainedVBR bool,
	eBands []int16, lm, effectiveBytes int, lfe bool, surroundDynalloc []int32,
	importance, spreadWeight []int, toneFreq int16, toneishness int32, totBoost *int,
	scratch *celtEncodeScratch) int32 {

	var follower, noiseFloor, bandLogE3 []int32
	if scratch != nil {
		follower = ensureInt32(&scratch.daFollower, C*nbEBands)
		noiseFloor = ensureInt32(&scratch.daNoise, C*nbEBands)
		bandLogE3 = ensureInt32(&scratch.daBandLogE3, nbEBands)
	} else {
		follower = make([]int32, C*nbEBands)
		noiseFloor = make([]int32, C*nbEBands)
		bandLogE3 = make([]int32, nbEBands)
	}

	for i := 0; i < nbEBands; i++ {
		offsets[i] = 0
	}

	var totBoostV int
	maxDepth := -gconstF(31.9)

	for i := 0; i < end; i++ {
		// noise_floor[i] = GCONST(.0625)*logN[i] + GCONST(.5)
		//   + SHL32(9-lsb_depth, DB_SHIFT) - SHL32(eMeans[i], DB_SHIFT-4)
		//   + GCONST(.0062)*(i+5)*(i+5)
		noiseFloor[i] = gconstF(0.0625)*int32(logN[i]) + gconstF(0.5) +
			shl32(int32(9-lsbDepth), dbShift) - shl32(int32(eMeans[i]), dbShift-4) +
			gconstF(0.0062)*int32(i+5)*int32(i+5)
	}
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			maxDepth = max32(maxDepth, bandLogE[c*nbEBands+i]-noiseFloor[i])
		}
	}
	{
		// Simple masking model for the spreading decision.
		var mask, sig []int32
		if scratch != nil {
			mask = ensureInt32(&scratch.daMask, nbEBands)
			sig = ensureInt32(&scratch.daSig, nbEBands)
		} else {
			mask = make([]int32, nbEBands)
			sig = make([]int32, nbEBands)
		}
		for i := 0; i < end; i++ {
			mask[i] = bandLogE[i] - noiseFloor[i]
		}
		if C == 2 {
			for i := 0; i < end; i++ {
				mask[i] = max32(mask[i], bandLogE[nbEBands+i]-noiseFloor[i])
			}
		}
		copy(sig[:end], mask[:end])
		for i := 1; i < end; i++ {
			mask[i] = max32(mask[i], mask[i-1]-gconstF(2))
		}
		for i := end - 2; i >= 0; i-- {
			mask[i] = max32(mask[i], mask[i+1]-gconstF(3))
		}
		for i := 0; i < end; i++ {
			smr := sig[i] - max32(max32(0, maxDepth-gconstF(12)), mask[i])
			// shift = -PSHR32(MAXG(-GCONST(5.f), MING(0, smr)), DB_SHIFT)
			shift := -pshr32(max32(-gconstF(5), min32(0, smr)), dbShift)
			spreadWeight[i] = 32 >> uint(shift)
		}
	}

	if effectiveBytes >= (30+5*lm) && !lfe {
		last := 0
		for c := 0; c < C; c++ {
			copy(bandLogE3[:end], bandLogE2[c*nbEBands:c*nbEBands+end])
			if lm == 0 {
				for i := 0; i < imin(8, end); i++ {
					bandLogE3[i] = max32(bandLogE2[c*nbEBands+i], oldBandE[c*nbEBands+i])
				}
			}
			f := follower[c*nbEBands : c*nbEBands+nbEBands]
			f[0] = bandLogE3[0]
			for i := 1; i < end; i++ {
				if bandLogE3[i] > bandLogE3[i-1]+gconstF(0.5) {
					last = i
				}
				f[i] = min32(f[i-1]+gconstF(1.5), bandLogE3[i])
			}
			for i := last - 1; i >= 0; i-- {
				f[i] = min32(f[i], min32(f[i+1]+gconstF(2), bandLogE3[i]))
			}

			offset := gconstF(1)
			for i := 2; i < end-2; i++ {
				f[i] = max32(f[i], medianOf5(bandLogE3[i-2:])-offset)
			}
			tmp := medianOf3(bandLogE3[0:]) - offset
			f[0] = max32(f[0], tmp)
			f[1] = max32(f[1], tmp)
			tmp = medianOf3(bandLogE3[end-3:]) - offset
			f[end-2] = max32(f[end-2], tmp)
			f[end-1] = max32(f[end-1], tmp)

			for i := 0; i < end; i++ {
				f[i] = max32(f[i], noiseFloor[i])
			}
		}
		if C == 2 {
			for i := start; i < end; i++ {
				follower[nbEBands+i] = max32(follower[nbEBands+i], follower[i]-gconstF(4))
				follower[i] = max32(follower[i], follower[nbEBands+i]-gconstF(4))
				follower[i] = half32(max32(0, bandLogE[i]-follower[i]) + max32(0, bandLogE[nbEBands+i]-follower[nbEBands+i]))
			}
		} else {
			for i := start; i < end; i++ {
				follower[i] = max32(0, bandLogE[i]-follower[i])
			}
		}
		for i := start; i < end; i++ {
			follower[i] = max32(follower[i], surroundDynalloc[i])
		}
		for i := start; i < end; i++ {
			importance[i] = int(pshr32(13*celtExp2Db(min32(follower[i], gconstF(4))), 16))
		}
		if (!vbr || constrainedVBR) && !isTransient {
			for i := start; i < end; i++ {
				follower[i] = half32(follower[i])
			}
		}
		for i := start; i < end; i++ {
			if i < 8 {
				follower[i] *= 2
			}
			if i >= 12 {
				follower[i] = half32(follower[i])
			}
		}
		// Compensate for under-allocation on tones.
		if toneishness > gconstQ(0.98, 29) {
			// freq_bin = PSHR32(tone_freq*QCONST16(120/M_PI,9), 13+9)
			freqBin := int(pshr32(int32(toneFreq)*int32(19557), 13+9))
			for i := start; i < end; i++ {
				if freqBin >= int(eBands[i]) && freqBin <= int(eBands[i+1]) {
					follower[i] += gconstF(2)
				}
				if freqBin >= int(eBands[i])-1 && freqBin <= int(eBands[i+1])+1 {
					follower[i] += gconstF(1)
				}
				if freqBin >= int(eBands[i])-2 && freqBin <= int(eBands[i+1])+2 {
					follower[i] += gconstF(1)
				}
				if freqBin >= int(eBands[i])-3 && freqBin <= int(eBands[i+1])+3 {
					follower[i] += gconstF(0.5)
				}
			}
			if freqBin >= int(eBands[end]) {
				follower[end-1] += gconstF(2)
				follower[end-2] += gconstF(1)
			}
		}
		// analysis is invalid: leak_boost branch skipped.
		for i := start; i < end; i++ {
			follower[i] = min32(follower[i], gconstF(4))
			follower[i] = shr32(follower[i], 8)
			width := C * (int(eBands[i+1]) - int(eBands[i])) << lm
			var boost, boostBits int
			if width < 6 {
				boost = int(shr32(follower[i], dbShift-8))
				boostBits = boost * width << bitRes
			} else if width > 48 {
				boost = int(shr32(follower[i]*8, dbShift-8))
				boostBits = (boost * width << bitRes) / 8
			} else {
				boost = int(shr32(follower[i]*int32(width)/6, dbShift-8))
				boostBits = boost * 6 << bitRes
			}
			if (!vbr || (constrainedVBR && !isTransient)) &&
				(totBoostV+boostBits)>>bitRes>>3 > 2*effectiveBytes/3 {
				cap := (2 * effectiveBytes / 3) << bitRes << 3
				offsets[i] = cap - totBoostV
				totBoostV = cap
				break
			} else {
				offsets[i] = boost
				totBoostV += boostBits
			}
		}
	} else {
		for i := start; i < end; i++ {
			importance[i] = 13
		}
	}
	*totBoost = totBoostV
	return maxDepth
}

// gconstQ evaluates QCONST32(x, q): (opus_val32)(.5+x*(1<<q)).
func gconstQ(x float64, q uint) int32 {
	return int32(0.5 + x*float64(int32(1)<<q))
}
