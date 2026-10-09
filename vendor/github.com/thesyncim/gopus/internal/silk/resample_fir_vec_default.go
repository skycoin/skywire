//go:build !amd64 || !goexperiment.simd || nosimd || purego

package silk

// firInterpolVec computes no outputs in this build; the scalar kernels do.
func firInterpolVec(dst, buf []int16, indexIncrQ16 int32) int {
	return 0
}
