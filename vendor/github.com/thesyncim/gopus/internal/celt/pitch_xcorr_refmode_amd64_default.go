//go:build amd64 && !goexperiment.simd && !nosimd && !purego

package celt

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return false
}
