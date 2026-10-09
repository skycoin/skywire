//go:build !amd64 || nosimd || purego || !goexperiment.simd

package celt

// celtFIR5F32 is libopus celt_fir5() applied in place to x.
func celtFIR5F32(x []float32, num [5]float32) {
	celtFIR5Scalar(x, num)
}

// pitchDownsample2 is the factor-2 pitch_downsample() decimation of outputs
// [1, len(dst)); see pitchDownsample2Scalar.
func pitchDownsample2(dst, x0, x1 []float32) {
	pitchDownsample2Scalar(dst, x0, x1, 1)
}
