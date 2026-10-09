//go:build amd64 && amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import "simd/archsimd"

// The pinned libopus 1.6.1 analysis.c translation unit uses mathops.h's
// fast_atan2f. Its GCC 13.3 amd64.v3 ordinary SIMD build contracts each
// p+c*q rational-polynomial term into one FMA; the selected SIMD path uses
// MulAdd only for those three terms.
func analysisAtan2PolyTermSIMD(p, q, c archsimd.Float32x4) archsimd.Float32x4 {
	return q.MulAdd(c, p)
}
