//go:build !amd64 || !goexperiment.simd || nosimd || purego

package silk

// silkLPCAnalysisFilterVec computes no outputs in the scalar build; it returns
// the first output index left for the scalar loop.
func silkLPCAnalysisFilterVec(out, in, B []int16, length, order int) int {
	return order
}
