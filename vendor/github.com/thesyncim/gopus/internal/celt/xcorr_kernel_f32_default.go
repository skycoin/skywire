//go:build !arm64 || purego

package celt

// pitchXcorrUsesNeonFMA is false off the fused arm64 build, so the byte-exact
// scalar/SSE/AVX2 pitch kernels are used and the amd64/purego oracle holds.
const pitchXcorrUsesNeonFMA = false

// xcorrKernel4Float32Neon is never called off arm64 (guarded by
// pitchXcorrUsesNeonFMA); the stub keeps the package building on all targets.
func xcorrKernel4Float32Neon(x, y []float32, sum *[4]float32, length int) {
	xcorrKernel4Float32(x, y, sum, length)
}
