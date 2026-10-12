//go:build (!amd64 && !arm64) || nosimd || purego || !goexperiment.simd

package celt

// imdctTDACWindow is the scalar IMDCT TDAC overlap-add windowing.
func imdctTDACWindow(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	imdctTDACWindowScalar(out, xsrc, window, yOut0, xOut0, xSrc0, wBwd0, count)
}
