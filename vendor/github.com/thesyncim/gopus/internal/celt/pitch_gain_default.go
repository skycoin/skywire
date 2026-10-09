//go:build !amd64.v3 || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package celt

func pitchGainDenominator32(xx, yy float32) float32 {
	return noFMA32Add(1, noFMA32Mul(xx, yy))
}
