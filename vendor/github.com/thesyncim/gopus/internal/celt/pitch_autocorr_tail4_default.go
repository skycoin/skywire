//go:build !darwin || !arm64 || !goexperiment.simd || nosimd || purego || gopus_fixed_point || gopus_dred || gopus_osce || gopus_custom_modes

package celt

const pitchAutocorrRoundFourTermTail = false

func pitchAutocorrTail4(x, y []float32) float32 {
	var sum float32
	for i := range 4 {
		sum += x[i] * y[i]
	}
	return sum
}
