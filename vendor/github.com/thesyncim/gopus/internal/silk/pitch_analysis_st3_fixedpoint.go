//go:build gopus_fixed_point

package silk

// FIXED_POINT port of the SILK pitch-analysis stage-3 cross-correlation kernel
// silk_P_Ana_calc_corr_st3 from silk/fixed/pitch_analysis_core_FIX.c.
//
// For every subframe it runs celt_pitch_xcorr over the lag range needed by the
// stage-3 contour search, reverses the lag ordering into a scratch buffer, and
// maps those values into the 3-dimensional codebook array consumed by the
// stage-3 search.

// silkPAnaCalcCorrSt3Fixed is the FIXED_POINT silk_P_Ana_calc_corr_st3.
//
// crossCorrSt3 is the flattened 3-D output array indexed as
// crossCorrSt3[(k*nbCbkSearch + i)][j], matching the
// silk_pe_stage3_vals[ nb_subfr * nb_cbk_search ] layout of libopus.
//
// frame is the int16 analysis frame, startLag the lag offset to search around,
// sfLength the length of one 5 ms subframe, nbSubfr the number of subframes
// (2 or 4) and complexity in [SILK_PE_MIN_COMPLEX, SILK_PE_MAX_COMPLEX].
func silkPAnaCalcCorrSt3Fixed(
	crossCorrSt3 [][peNbStage3Lags]int32,
	frame []int16,
	startLag int,
	sfLength int,
	nbSubfr int,
	complexity int,
) {
	var (
		lagRangePtr [][2]int8
		lagCBPtr    [][]int8
		nbCbkSearch int
	)

	if nbSubfr == peMaxNbSubfr {
		lagRangePtr = pitchLagRangeStage3[complexity][:]
		lagCBPtr = pitchCBLagsStage3Slice
		nbCbkSearch = pitchNbCbkSearchsStage3[complexity]
	} else {
		lagRangePtr = pitchLagRangeStage310ms[:]
		lagCBPtr = pitchCBLagsStage310msSlice
		nbCbkSearch = peNbCbksStage310ms
	}

	var (
		scratchMem [pitchStage3ScratchSize]int32
		xcorr32    [pitchStage3ScratchSize]int32
	)

	// target_ptr = &frame[ silk_LSHIFT( sf_length, 2 ) ]: pointer to middle of frame.
	target := int(silkLSHIFT(int32(sfLength), 2))
	for k := 0; k < nbSubfr; k++ {
		lagCounter := 0

		lagLow := int(lagRangePtr[k][0])
		lagHigh := int(lagRangePtr[k][1])

		// celt_pitch_xcorr( target_ptr, target_ptr - start_lag - lag_high,
		//                   xcorr32, sf_length, lag_high - lag_low + 1 ).
		base := target - startLag - lagHigh
		nLags := lagHigh - lagLow + 1
		celtPitchXcorrFixed(frame[target:], frame[base:], xcorr32[:], sfLength, nLags)

		for j := lagLow; j <= lagHigh; j++ {
			scratchMem[lagCounter] = xcorr32[lagHigh-j]
			lagCounter++
		}

		delta := lagLow
		for i := 0; i < nbCbkSearch; i++ {
			idx := int(lagCBPtr[k][i]) - delta
			row := k*nbCbkSearch + i
			for j := 0; j < peNbStage3Lags; j++ {
				crossCorrSt3[row][j] = scratchMem[idx+j]
			}
		}
		target += sfLength
	}
}
