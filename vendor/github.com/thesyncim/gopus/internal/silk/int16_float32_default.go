//go:build !arm64 || purego

package silk

func writeInt16AsFloat32Core(dst []float32, src []int16, n int) {
	_ = dst[n-1]
	_ = src[n-1]
	const inv32768 = 1.0 / 32768.0
	for i := 0; i < n; i++ {
		dst[i] = float32(src[i]) * inv32768
	}
}
