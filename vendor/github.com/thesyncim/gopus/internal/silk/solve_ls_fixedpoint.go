//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK normal-equation regularization
// kernel from silk/fixed/regularize_correlations_FIX.c:
// silk_regularize_correlations_FIX. It adds a noise floor to the diagonal of a
// row-major D x D correlation matrix XX and to xx[0] so the subsequent solve of
// the LTP/LPC normal equations stays well-conditioned.
//
// In libopus 1.6.1 the LDL solver itself (silk_solve_LDL_FIX and its
// silk_LDL_factorize_FIX / silk_LS_SolveFirst_FIX / silk_LS_SolveLast_FIX /
// silk_LS_divide_Q16_FIX helpers from earlier releases) has been removed from
// the tree; this regularization helper is the remaining self-contained piece of
// that family.
//
// matrix_ptr(M, row, col, N) addresses a row-major N x N matrix as
// M[row*N+col], so the diagonal entry (i, i) of a D x D matrix is XX[i*D+i].

// silkRegularizeCorrelationsFixed is the bit-exact Go port of
// silk_regularize_correlations_FIX. It adds noise to the diagonal of the D x D
// row-major correlation matrix XX and to xx[0].
func silkRegularizeCorrelationsFixed(XX []int32, xx []int32, noise int32, D int) {
	for i := 0; i < D; i++ {
		XX[i*D+i] = silkADD32(XX[i*D+i], noise)
	}
	xx[0] += noise
}
