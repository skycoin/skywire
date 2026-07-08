//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK long-term-prediction analysis
// from silk/fixed/find_LTP_FIX.c: silk_find_LTP_FIX. For each subframe it forms
// the LTP correlation matrix X'*X and the correlation vector X'*r (via the
// existing silkCorrMatrixFixed / silkCorrVectorFixed kernels), reconciles the
// right-shift counts of the matrix energy and the signal energy, then divides
// every correlation by max(nrg*LTP_CORR_INV_MAX + 1, xx) to produce the Q17
// outputs.
//
// The data window for subframe k starts at lag_ptr = r_ptr - (lag[k] +
// LTP_ORDER/2), where r_ptr advances by subfr_length each subframe. residual is
// the full LPC residual buffer and resStart is the index of r_ptr for the first
// subframe.

// silkFindLTPFixed is the bit-exact Go port of silk_find_LTP_FIX.
//
// XXLTPQ17 (length nbSubfr*ltpOrder*ltpOrder) receives the per-subframe Q17
// correlation matrices and xXLTPQ17 (length nbSubfr*ltpOrder) the per-subframe
// Q17 correlation vectors.
func silkFindLTPFixed(
	XXLTPQ17 []int32,
	xXLTPQ17 []int32,
	residual []int16,
	resStart int,
	lag []int32,
	subfrLength int,
	nbSubfr int,
) {
	const order = ltpOrder // LTP_ORDER == 5

	// SILK_FIX_CONST(LTP_CORR_INV_MAX, 16).
	corrInvMaxQ16 := int32(silkFixConst(ltpCorrInvMax, 16))

	xXIdx := 0
	XXIdx := 0
	rPtr := resStart
	for k := 0; k < nbSubfr; k++ {
		lagPtr := rPtr - (int(lag[k]) + order/2)

		// xx in Q(-xxShifts).
		xx, xxShifts := silkSumSqrShiftFixed(residual[rPtr:], subfrLength+order)

		// XXLTP_Q17_ptr and nrg in Q(-XXShifts).
		XX := XXLTPQ17[XXIdx : XXIdx+order*order]
		nrg, XXShifts := silkCorrMatrixFixed(residual[lagPtr:], subfrLength, order, XX)

		var xXShifts int
		extraShifts := xxShifts - XXShifts
		if extraShifts > 0 {
			// Shift XX.
			xXShifts = xxShifts
			for i := 0; i < order*order; i++ {
				XX[i] = silkRSHIFT(XX[i], extraShifts) // Q(-xXShifts)
			}
			nrg = silkRSHIFT(nrg, extraShifts) // Q(-xXShifts)
		} else if extraShifts < 0 {
			// Shift xx.
			xXShifts = XXShifts
			xx = silkRSHIFT(xx, -extraShifts) // Q(-xXShifts)
		} else {
			xXShifts = xxShifts
		}

		// xXLTP_Q17_ptr in Q(-xXShifts).
		xX := xXLTPQ17[xXIdx : xXIdx+order]
		silkCorrVectorFixed(residual[lagPtr:], residual[rPtr:], subfrLength, order, xX, xXShifts)

		// All correlations are now in Q(-xXShifts).
		temp := silkSMLAWB(1, nrg, corrInvMaxQ16)
		temp = silkMax32(temp, xx)
		for i := 0; i < order*order; i++ {
			XX[i] = int32((int64(XX[i]) << 17) / int64(temp))
		}
		for i := 0; i < order; i++ {
			xX[i] = int32((int64(xX[i]) << 17) / int64(temp))
		}

		rPtr += subfrLength
		XXIdx += order * order
		xXIdx += order
	}
}
