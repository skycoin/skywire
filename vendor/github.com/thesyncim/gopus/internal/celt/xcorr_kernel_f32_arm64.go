//go:build arm64 && !purego

package celt

// xcorrKernel4Float32Neon accumulates the four-lag float cross-correlation with
// a single NEON FMLA per sample. The fused multiply-add diverges from the
// scalar multiply-then-add by at most 1 ULP, which is the arm64 quality-gated
// regime (MODEL A); amd64 and purego keep the byte-exact scalar/SSE/AVX2 paths.
//
//go:noescape
func xcorrKernel4Float32Neon(x, y []float32, sum *[4]float32, length int)

// pitchXcorrUsesNeonFMA reports whether the fused NEON float pitch kernel runs.
// On arm64 the default (non-purego) build uses it; this is the same fused-vs-
// bit-exact split as mdctFMA32.
const pitchXcorrUsesNeonFMA = true
