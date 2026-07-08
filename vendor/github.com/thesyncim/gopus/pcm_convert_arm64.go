//go:build arm64 && !purego

package gopus

//go:noescape
func convertFloat32ToInt16UnitBlocks(dst []int16, src []float32, n int) bool

//go:noescape
func convertFloat32ToInt16SaturatingBlocks(dst []int16, src []float32, n int)

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	blocks := n &^ 15
	if blocks > 0 && !convertFloat32ToInt16UnitBlocks(dst, src, blocks) {
		return false
	}
	for i := blocks; i < n; i++ {
		v := src[i]
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = float32ToInt16(v)
	}
	return true
}

func convertFloat32ToInt16NoSoftClipUnit(dst []int16, src []float32, n int) {
	blocks := n &^ 15
	if blocks > 0 {
		convertFloat32ToInt16SaturatingBlocks(dst, src, blocks)
	}
	for i := blocks; i < n; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}
