//go:build !(arm64 || amd64) || nosimd || purego || !goexperiment.simd

package celt

func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	imdctPostRotateF32FromKissScalar(buf, fft, trig, n2, n4)
}

func imdctPreRotateNoFMA(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateNoFMAScalar(fftIn, spectrum, trig, n2, n4)
}
