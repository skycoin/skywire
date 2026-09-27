//go:build arm64 && !purego

package silk

//go:noescape
func innerProductFLPArm64(a, b []float32, length int) silkCReal

func innerProductFLPImpl(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	_ = a[length-1]
	_ = b[length-1]
	return innerProductFLPArm64(a, b, length)
}
