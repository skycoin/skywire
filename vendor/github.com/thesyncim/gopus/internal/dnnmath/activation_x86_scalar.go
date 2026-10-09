//go:build !amd64 || !goexperiment.simd || nosimd || purego

package dnnmath

// X86VectorKernels is false outside the amd64 Go SIMD lane.
const X86VectorKernels = false

func sigmoidVectorX86(out, in []float32, n int) { SigmoidVectorScalarApprox(out, in, n) }
func tanhVectorX86(out, in []float32, n int)    { TanhVectorScalarApprox(out, in, n) }
func tanhApproxX86(x float32) float32           { return TanhScalarApprox(x) }
func expVectorX86(out, in []float32, n int)     { ExpVectorScalarApprox(out, in, n) }
