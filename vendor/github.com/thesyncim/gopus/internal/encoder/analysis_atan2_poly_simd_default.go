//go:build amd64 && goexperiment.simd && !nosimd && !purego && (!amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes)

package encoder

import "simd/archsimd"

func analysisAtan2PolyTermSIMD(p, q, c archsimd.Float32x4) archsimd.Float32x4 {
	return p.Add(c.Mul(q))
}
