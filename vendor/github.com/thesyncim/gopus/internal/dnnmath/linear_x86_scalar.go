//go:build !amd64 || !goexperiment.simd || nosimd || purego

package dnnmath

import "github.com/thesyncim/gopus/internal/dnnblob"

// The AVX2/FMA DNN kernels exist only in the amd64 Go SIMD lane. Callers
// gate them on X86VectorKernels, which is false here.

func SGEMVX86([]float32, dnnblob.Float32View, int, int, int, []float32) {
	panic("dnnmath: AVX2 DNN kernels are not available in this build")
}

func SparseSGEMV8x4X86([]float32, dnnblob.Float32View, dnnblob.Int32View, int, []float32) {
	panic("dnnmath: AVX2 DNN kernels are not available in this build")
}

func CGEMV8x4X86([]float32, dnnblob.Int8View, dnnblob.Float32View, int, int, []float32, []uint8) {
	panic("dnnmath: AVX2 DNN kernels are not available in this build")
}

func SparseCGEMV8x4X86([]float32, dnnblob.Int8View, dnnblob.Int32View, dnnblob.Float32View, int, int, []float32, []uint8) {
	panic("dnnmath: AVX2 DNN kernels are not available in this build")
}

func Conv2D3x3X86([]float32, dnnblob.Float32View, int, int, []float32, int, int) {
	panic("dnnmath: AVX2 DNN kernels are not available in this build")
}
