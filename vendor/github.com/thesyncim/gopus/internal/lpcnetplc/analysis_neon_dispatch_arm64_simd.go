//go:build arm64 && goexperiment.simd && !nosimd && !purego

package lpcnetplc

// The selected ARM SIMD libopus build presumes NEON for celt_pitch_xcorr and
// celt_inner_prod (celt/arm/pitch_arm.h). The LPCNet analysis uses both.
const useNEONAnalysisKernels = true
