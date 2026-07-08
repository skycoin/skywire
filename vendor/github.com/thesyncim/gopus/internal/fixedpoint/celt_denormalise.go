//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer FIXED_POINT celt/bands.c denormalise_bands:
// it turns the unit-energy normalized coefficients X back into a synthesis
// spectrum by applying the per-band gain derived from the quantized band log
// energy. The kernel is bit-exact to libopus on the OPUS_FAST_INT64 path
// (amd64, arm64/LP64) where MULT32_32_Q31 is the 64-bit form.
//
// With the scalable quality extension disabled (the default reference build),
// celt_norm/celt_sig/celt_glog are all opus_val32 (int32) and:
//
//	NORM_SHIFT = 24
//	DB_SHIFT   = 24
//	BITRES     = 3
//	celt_exp2_db_frac(x) = SHL32(celt_exp2_frac(PSHR32(x, DB_SHIFT-10)), 14)

// imin implements libopus IMIN(a, b) on plain ints.
func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// celtExp2DbFrac implements the FIXED_POINT (non-QEXT) celt_exp2_db_frac macro:
// SHL32(celt_exp2_frac(PSHR32(x, DB_SHIFT-10)), 14). Input x is a Q24 fractional
// log-energy in [0, 1<<DB_SHIFT); the result is the Q14 mantissa lifted into the
// upper bits by the left shift.
func celtExp2DbFrac(x int32) int32 {
	// PSHR32(x, DB_SHIFT-10) reduces Q24 to Q10; celt_exp2_frac takes Q10 in
	// (opus_val16) and returns the Q14 mantissa.
	q10 := int16(pshr32(x, 24-10))
	return int32(CeltExp2Frac(q10)) << 14
}

// DenormaliseBands ports the FIXED_POINT celt/bands.c denormalise_bands: for the
// active bands [start,end) it derives each band's gain from bandLogE[i] (biased
// by eMeans[i]) and writes the de-normalised synthesis spectrum into freq. Bands
// outside [start,end) and everything from bound onward are cleared to zero.
//
// Inputs mirror the libopus CELTMode plumbing:
//
//	x             normalized coefficients (celt_norm), length M*eBands[end]
//	freq          output synthesis spectrum (celt_sig), length N = M*shortMdctSize
//	bandLogE      per-band quantized log energy (celt_glog), length >= end
//	eBands        mode band boundaries (m->eBands), length >= end+1
//	shortMdctSize m->shortMdctSize
//	start, end    active band range
//	M             time-resolution factor (1<<LM)
//	downsample    decode downsampling factor
//	silence       when non-zero, the whole frame is silenced
func DenormaliseBands(x, freq, bandLogE []int32, eBands []int16, shortMdctSize, start, end, M, downsample int, silence bool) {
	n := M * int(shortMdctSize)
	bound := M * int(eBands[end])
	if downsample != 1 {
		bound = imin(bound, n/downsample)
	}
	if silence {
		bound = 0
		start = 0
		end = 0
	}

	// f tracks the libopus write cursor into freq; xi tracks the read cursor
	// into x, which starts at X + M*eBands[start].
	f := 0
	xi := M * int(eBands[start])
	if start != 0 {
		for i := 0; i < M*int(eBands[start]); i++ {
			freq[f] = 0
			f++
		}
	} else {
		f += M * int(eBands[start])
	}

	for i := start; i < end; i++ {
		j := M * int(eBands[i])
		bandEnd := M * int(eBands[i+1])

		// lg = ADD32(bandLogE[i], SHL32(eMeans[i], DB_SHIFT-4))
		lg := add32(bandLogE[i], shl32(int32(eMeans[i]), 24-4))

		// Handle the integer part of the log energy.
		shift := 17 - int(lg>>24)
		var g int32
		if shift >= 31 {
			shift = 0
			g = 0
		} else {
			// Handle the fractional part: g = SHL32(celt_exp2_db_frac(lg&((1<<DB_SHIFT)-1)), 2)
			g = shl32(celtExp2DbFrac(lg&((1<<24)-1)), 2)
		}
		// Handle extreme gains with negative shift by capping g.
		if shift < 0 {
			g = 2147483647
			shift = 0
		}

		for {
			// *f++ = PSHR32(MULT32_32_Q31(SHL32(*x, 30-NORM_SHIFT), g), shift)
			freq[f] = pshr32(mult32x32q31(shl32(x[xi], 30-24), g), shift)
			f++
			xi++
			j++
			if j >= bandEnd {
				break
			}
		}
	}

	// OPUS_CLEAR(&freq[bound], N-bound)
	for i := bound; i < n; i++ {
		freq[i] = 0
	}
}
