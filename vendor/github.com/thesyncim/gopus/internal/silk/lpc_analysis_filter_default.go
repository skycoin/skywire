//go:build !amd64 || !goexperiment.simd || nosimd || purego

package silk

func lpcAnalysisFilterF32(rLPC, predCoef, s []float32, length, order int) {
	lpcAnalysisFilterF32Scalar(rLPC, predCoef, s, length, order)
}
