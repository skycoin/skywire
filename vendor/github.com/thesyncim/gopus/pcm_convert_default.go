//go:build !arm64

package gopus

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	return false
}

func convertFloat32ToInt16NoSoftClipUnit(dst []int16, src []float32, n int) {
	for i := 0; i < n; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}
