//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

// pitchXcorrUsesNeonFMA is false off the fused arm64 build, so the byte-exact
// scalar/SSE/AVX2 pitch kernels are used and the amd64/nosimd oracle holds.
const pitchXcorrUsesNeonFMA = false

// xcorrKernel4Float32NeonOrdered is never called off arm64 (guarded by
// pitchXcorrUsesNeonFMA); the stub keeps the package building on all targets.
func xcorrKernel4Float32NeonOrdered(x, y []float32, sum *[4]float32, length int) {
	xcorrKernel4Float32(x, y, sum, length)
}
