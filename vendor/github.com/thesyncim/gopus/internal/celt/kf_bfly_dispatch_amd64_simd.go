//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

func kfBfly4M1Core(fout []kissCpx, n int) {
	kfBfly4M1CoreSIMD(fout, n)
}

func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly5InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly3InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly4InnerSIMD(fout, w, m, N, mm, fstride)
}

// kfBflyScalarFastInput is false: the SIMD butterflies handle every stage.
func kfBflyScalarFastInput([]kissCpx) bool { return false }

func kfBfly2M4(fout []kissCpx, n int) {
	kfBfly2M4SIMD(fout, n)
}
