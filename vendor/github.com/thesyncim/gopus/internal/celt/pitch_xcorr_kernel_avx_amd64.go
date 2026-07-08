//go:build amd64 && !purego

package celt

// xcorrKernelAVX8 computes eight float cross-correlations of x against
// successive offsets of y using AVX2 FMA, mirroring libopus
// celt/x86/pitch_avx.c:xcorr_kernel_avx instruction-for-instruction so the
// eight results are bit-identical to that reference (and to the scalar
// lane-ordered fallback in pitchXCorrFloat32AVX2FMAOrder).
//
//go:noescape
func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int)

// pitchXcorrKernelAVX8 fills sum with the eight cross-correlations of x against
// y[0..7+length-1]. x must have at least length elements and y at least
// length+7; both invariants hold for callers in pitchXCorrFloat32AVX2FMAOrder.
func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX8(&x[0], &y[0], sum, length)
}
