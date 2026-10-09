//go:build amd64 && goexperiment.simd && !nosimd && !purego && (!amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes)

package encoder

import "simd/archsimd"

func analysisToneDenominatorSIMD(k, modulation, one archsimd.Float32x4) archsimd.Float32x4 {
	return one.Add(k.Mul(modulation))
}
