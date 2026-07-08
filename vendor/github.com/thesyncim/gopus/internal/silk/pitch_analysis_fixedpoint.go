//go:build gopus_fixed_point

package silk

// FIXED_POINT port of the SILK pitch-analysis stage-3 energy refinement
// kernel silk_P_Ana_calc_energy_st3 from
// silk/fixed/pitch_analysis_core_FIX.c.
//
// It computes, for the first lags of every subframe, the running energy of the
// basis vector and then maps those recursively-updated energies into the
// 3-dimensional codebook array consumed by the stage-3 contour search.

// pitchStage3ScratchSize mirrors SCRATCH_SIZE in pitch_analysis_core_FIX.c.
const pitchStage3ScratchSize = 22

// Complexity bounds for the SILK pitch estimator (SILK_PE_*_COMPLEX in
// pitch_est_defines.h).
const (
	SILK_PE_MIN_COMPLEX = 0
	SILK_PE_MAX_COMPLEX = 2
)

// silkPAnaCalcEnergySt3Fixed is the FIXED_POINT silk_P_Ana_calc_energy_st3.
//
// energiesSt3 is the flattened 3-D output array indexed as
// energiesSt3[(k*nbCbkSearch + i)*peNbStage3Lags + j], matching the
// silk_pe_stage3_vals[ nb_subfr * nb_cbk_search ] layout of libopus.
//
// frame is the int16 analysis frame, startLag the lag offset to search around,
// sfLength the length of one 5 ms subframe, nbSubfr the number of subframes
// (2 or 4) and complexity in [SILK_PE_MIN_COMPLEX, SILK_PE_MAX_COMPLEX].
func silkPAnaCalcEnergySt3Fixed(
	energiesSt3 [][peNbStage3Lags]int32,
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

	var scratchMem [pitchStage3ScratchSize]int32

	// target_ptr = &frame[ silk_LSHIFT( sf_length, 2 ) ]: pointer to middle of frame.
	target := int(silkLSHIFT(int32(sfLength), 2))
	for k := 0; k < nbSubfr; k++ {
		lagCounter := 0

		lagLow := int(lagRangePtr[k][0])
		lagHigh := int(lagRangePtr[k][1])

		// basis_ptr = target_ptr - ( start_lag + lag_low )
		basis := target - (startLag + lagLow)

		// Energy for the first lag.
		energy := silkInnerProdAlignedFixed(frame[basis:], frame[basis:], sfLength)
		scratchMem[lagCounter] = energy
		lagCounter++

		lagDiff := lagHigh - lagLow + 1
		for i := 1; i < lagDiff; i++ {
			// Remove the part that left the window.
			energy -= silk_SMULBB(int32(frame[basis+sfLength-i]), int32(frame[basis+sfLength-i]))
			// Add the part that entered the window.
			energy = silk_ADD_SAT32(energy, silk_SMULBB(int32(frame[basis-i]), int32(frame[basis-i])))
			scratchMem[lagCounter] = energy
			lagCounter++
		}

		delta := lagLow
		for i := 0; i < nbCbkSearch; i++ {
			idx := int(lagCBPtr[k][i]) - delta
			row := (k*nbCbkSearch + i)
			for j := 0; j < peNbStage3Lags; j++ {
				energiesSt3[row][j] = scratchMem[idx+j]
			}
		}
		target += sfLength
	}
}
