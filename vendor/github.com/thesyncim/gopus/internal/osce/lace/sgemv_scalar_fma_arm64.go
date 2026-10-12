//go:build arm64 && (!goexperiment.simd || nosimd || purego)

package lace

// The generic scalar OSCE reference uses dnn/vec.h's non-8-row sgemv loop.
// With the paired no-vectorization CFLAGS, the selected arm64 object contracts
// `out[i] += weights[j*col_stride+i] * x[j]` into FMADD.
const scalarOSCEGenericFMA = true
