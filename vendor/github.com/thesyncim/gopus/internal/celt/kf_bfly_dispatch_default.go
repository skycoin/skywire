//go:build (!arm64 && !amd64) || nosimd || purego || !goexperiment.simd

package celt

func kfBfly4M1Core(fout []kissCpx, n int) {
	kfBfly4M1CoreScalar(fout, n)
}

func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
}

func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly3InnerScalar(fout, w, m, N, mm, fstride)
}

func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
}

// kfBflyScalarFastInput reports whether an FFT over fout may use the
// kfBfly*InnerFast butterflies: always where the twiddle products have no
// separate non-finite path, otherwise when the input is bounded.
func kfBflyScalarFastInput(fout []kissCpx) bool {
	return !kissMulSourceNaNFixup || kissFFTInputBounded(fout)
}

func kfBfly2M4(fout []kissCpx, n int) {
	kfBfly2M4Scalar(fout, n)
}
