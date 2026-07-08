//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK LPC-analysis correlation
// kernels from silk/fixed/corrMatrix_FIX.c: silk_corrVector_FIX (the X'*t
// correlation vector) and silk_corrMatrix_FIX (the X'*X autocorrelation
// matrix together with the energy and the right-shift count chosen to keep
// the accumulators in an int32).
//
// The data matrix X is formed from x[0 : L+order-1]; column lag of X is the
// length-L slice x[order-1-lag : order-1-lag+L]. matrix_ptr(M, row, col, N)
// addresses a row-major order x order matrix as M[row*N+col].

// silkCorrVectorFixed is the bit-exact Go port of silk_corrVector_FIX. It
// computes Xt[lag] = X[:,lag]' * t for lag in [0, order), where X[:,lag] is the
// length-L window of x ending at x[order-1] and sliding left by lag, and t is
// the length-L target vector.
//
// When rshifts > 0 every product is right-shifted by rshifts before being
// accumulated. When rshifts == 0 the products are summed without shifting,
// matching silk_inner_prod_aligned (celt_inner_prod) in the FIXED_POINT build.
func silkCorrVectorFixed(x, t []int16, L, order int, Xt []int32, rshifts int) {
	// ptr1 starts at the first sample of column 0 of X: x[order-1].
	ptr1 := order - 1
	if rshifts > 0 {
		for lag := 0; lag < order; lag++ {
			var innerProd int32
			for i := 0; i < L; i++ {
				innerProd = silkADD_RSHIFT32(innerProd, silkSMULBB(int32(x[ptr1+i]), int32(t[i])), rshifts)
			}
			Xt[lag] = innerProd
			ptr1-- // Go to next column of X.
		}
	} else {
		for lag := 0; lag < order; lag++ {
			Xt[lag] = silkInnerProdAlignedFixed(x[ptr1:], t, L)
			ptr1--
		}
	}
}

// silkCorrMatrixFixed is the bit-exact Go port of silk_corrMatrix_FIX. It fills
// the row-major order x order matrix XX with X'*X, returns the energy of the x
// vector and the number of right shifts applied to the correlations so they fit
// in an int32. XX must have length at least order*order.
func silkCorrMatrixFixed(x []int16, L, order int, XX []int32) (nrg int32, rshifts int) {
	// Energy and the right-shift used to keep it inside an int32.
	nrg, rshifts = silkSumSqrShiftFixed(x, L+order-1)
	energy := nrg

	// Energy of the first column (0) of X: remove the contribution of the
	// first order-1 samples.
	for i := 0; i < order-1; i++ {
		energy -= silkRSHIFT(silkSMULBB(int32(x[i]), int32(x[i])), rshifts)
	}

	// Diagonal of the correlation matrix.
	XX[0] = energy // matrix_ptr(XX, 0, 0, order)
	ptr1 := order - 1
	for j := 1; j < order; j++ {
		energy -= silkRSHIFT(silkSMULBB(int32(x[ptr1+L-j]), int32(x[ptr1+L-j])), rshifts)
		energy += silkRSHIFT(silkSMULBB(int32(x[ptr1-j]), int32(x[ptr1-j])), rshifts)
		XX[j*order+j] = energy // matrix_ptr(XX, j, j, order)
	}

	ptr2 := order - 2 // First sample of column 1 of X.
	if rshifts > 0 {
		for lag := 1; lag < order; lag++ {
			// Inner product of column 0 and column lag: X[:,0]'*X[:,lag].
			var e int32
			for i := 0; i < L; i++ {
				e += silkRSHIFT(silkSMULBB(int32(x[ptr1+i]), int32(x[ptr2+i])), rshifts)
			}
			energy = e
			XX[lag*order+0] = energy // matrix_ptr(XX, lag, 0, order)
			XX[0*order+lag] = energy // matrix_ptr(XX, 0, lag, order)
			for j := 1; j < order-lag; j++ {
				energy -= silkRSHIFT(silkSMULBB(int32(x[ptr1+L-j]), int32(x[ptr2+L-j])), rshifts)
				energy += silkRSHIFT(silkSMULBB(int32(x[ptr1-j]), int32(x[ptr2-j])), rshifts)
				XX[(lag+j)*order+j] = energy // matrix_ptr(XX, lag+j, j, order)
				XX[j*order+(lag+j)] = energy // matrix_ptr(XX, j, lag+j, order)
			}
			ptr2-- // Next column (lag) in X.
		}
	} else {
		for lag := 1; lag < order; lag++ {
			energy = silkInnerProdAlignedFixed(x[ptr1:], x[ptr2:], L)
			XX[lag*order+0] = energy
			XX[0*order+lag] = energy
			for j := 1; j < order-lag; j++ {
				energy = energy - silkSMULBB(int32(x[ptr1+L-j]), int32(x[ptr2+L-j]))
				energy = silkSMLABB(energy, int32(x[ptr1-j]), int32(x[ptr2-j]))
				XX[(lag+j)*order+j] = energy
				XX[j*order+(lag+j)] = energy
			}
			ptr2--
		}
	}
	return nrg, rshifts
}

// silkInnerProdAlignedFixed is the FIXED_POINT silk_inner_prod_aligned
// (celt_inner_prod_c): sum over i of inVec1[i]*inVec2[i] accumulated in an
// int32 with wrap-around-free signed multiplication of int16 lanes.
//
// NOTE(dedup): self-contained copy used by the correlation kernels. If a shared
// fixed-point inner-product lands in the default silk build, fold this into it.
func silkInnerProdAlignedFixed(inVec1, inVec2 []int16, length int) int32 {
	var sum int32
	for i := 0; i < length; i++ {
		sum = silkSMLABB(sum, int32(inVec1[i]), int32(inVec2[i]))
	}
	return sum
}
