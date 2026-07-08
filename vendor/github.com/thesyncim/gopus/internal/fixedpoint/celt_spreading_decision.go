//go:build gopus_fixed_point

package fixedpoint

// FIXED_POINT port of celt/bands.c spreading_decision: the spread/tapset
// estimator used by celt_encode_with_ec. It updates the running tonal average,
// the HF average and the tapset decision, returning the spread decision symbol.

// spreadLight / spreadNormal complete the SPREAD_* set (spreadNone and
// spreadAggressive are declared with the alg_quant / bands_quant kernels).
const (
	spreadLight  = 1 // SPREAD_LIGHT
	spreadNormal = 2 // SPREAD_NORMAL
)

// SpreadingState holds the cross-frame averages spreading_decision maintains
// (st->tonal_average, st->hf_average, st->tapset_decision).
type SpreadingState struct {
	TonalAverage   int
	HFAverage      int
	TapsetDecision int
}

// SpreadingDecision ports celt/bands.c spreading_decision (FIXED_POINT). X is the
// interleaved normalised spectrum (celt_norm), eBands is mode->eBands, nbEBands is
// mode->nbEBands, end is effEnd, C is the channel count, M == 1<<LM. updateHF
// gates the HF/tapset update (pf_on && !shortBlocks in the caller). spreadWeight
// comes from dynalloc_analysis. It updates st in place and returns the decision.
func SpreadingDecision(x []int32, eBands []int16, nbEBands, lastDecision int,
	st *SpreadingState, updateHF, end, C, M int, spreadWeight []int) int {

	sum := 0
	nbBands := 0
	hfSum := 0
	N0 := M * celtShortMdctSize // M*m->shortMdctSize

	if M*(int(eBands[end])-int(eBands[end-1])) <= 8 {
		return spreadNone
	}
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			xBand := x[M*int(eBands[i])+c*N0:]
			N := M * (int(eBands[i+1]) - int(eBands[i]))
			if N <= 8 {
				continue
			}
			var tcount [3]int
			for j := 0; j < N; j++ {
				// x2N = MULT16_16(MULT16_16_Q15(SHR32(x[j],NORM_SHIFT-14), SHR32(x[j],NORM_SHIFT-14)), N)  (Q13)
				xs := shr32(xBand[j], normShift-14)
				x2N := mult16x16(mult16x16Q15(xs, xs), int32(N))
				if x2N < 2048 { // QCONST16(0.25f,13)
					tcount[0]++
				}
				if x2N < 512 { // QCONST16(0.0625f,13)
					tcount[1]++
				}
				if x2N < 128 { // QCONST16(0.015625f,13)
					tcount[2]++
				}
			}
			if i > nbEBands-4 {
				hfSum += int(celtUdiv(uint32(32*(tcount[1]+tcount[0])), uint32(N)))
			}
			tmp := boolToInt(2*tcount[2] >= N) + boolToInt(2*tcount[1] >= N) + boolToInt(2*tcount[0] >= N)
			sum += tmp * spreadWeight[i]
			nbBands += spreadWeight[i]
		}
	}

	if updateHF != 0 {
		if hfSum != 0 {
			hfSum = int(celtUdiv(uint32(hfSum), uint32(C*(4-nbEBands+end))))
		}
		st.HFAverage = (st.HFAverage + hfSum) >> 1
		hfSum = st.HFAverage
		if st.TapsetDecision == 2 {
			hfSum += 4
		} else if st.TapsetDecision == 0 {
			hfSum -= 4
		}
		if hfSum > 22 {
			st.TapsetDecision = 2
		} else if hfSum > 18 {
			st.TapsetDecision = 1
		} else {
			st.TapsetDecision = 0
		}
	}

	sum = int(celtUdiv(uint32(sum<<8), uint32(nbBands)))
	sum = (sum + st.TonalAverage) >> 1
	st.TonalAverage = sum
	sum = (3*sum + (((3 - lastDecision) << 7) + 64) + 2) >> 2
	switch {
	case sum < 80:
		return spreadAggressive
	case sum < 256:
		return spreadNormal
	case sum < 384:
		return spreadLight
	default:
		return spreadNone
	}
}
