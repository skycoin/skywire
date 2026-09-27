//go:build arm64 && !purego

package celt

// xcorrKernel4Float32Neon4Acc accumulates the four-lag float cross-correlation
// with four phase-parallel NEON FMLA chains (one per sample in each 4-sample
// block), so the accumulation is throughput-bound instead of serializing on a
// single FMLA chain (~3x the single-accumulator kernel it replaced). Lane
// combination order is (acc0+acc1)+(acc2+acc3); the bit-exact contract is
// against xcorrKernel4Float32FourAccRef, the order-matched scalar reference.
// Like libopus' own NEON-vs-scalar split this is the quality-gated arm64
// regime; purego keeps the scalar-order kernel as the byte-exact oracle.
//
//go:noescape
func xcorrKernel4Float32Neon4Acc(x, y []float32, sum *[4]float32, length int)

// pitchXcorrUsesNeonFMA reports whether the fused NEON float pitch kernel runs.
// On arm64 the default (non-purego) build uses it.
const pitchXcorrUsesNeonFMA = true
