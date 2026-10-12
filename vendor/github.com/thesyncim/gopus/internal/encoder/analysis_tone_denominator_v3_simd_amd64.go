//go:build amd64 && amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import "simd/archsimd"

// libopus 1.6.1's GCC 13.3 amd64.v3 tonality_analysis caller contracts each
// `1.f + K*modulation` denominator into an FMA in the selected SIMD path.
func analysisToneDenominatorSIMD(k, modulation, one archsimd.Float32x4) archsimd.Float32x4 {
	return k.MulAdd(modulation, one)
}
