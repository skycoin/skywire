//go:build arm64 && !purego

package celt

// haar1Stride1NEON applies the stride==1 Hadamard butterfly to n0 contiguous
// (even,odd) float32 pairs in place, using NEON LD2/ST2 deinterleave plus
// lane-wise FMUL then FADD/FSUB. Each product is bare and each add/sub is
// separate (the same lane math as libopus's NEON kernels): bit-exact with the
// non-fused scalar oracle (purego/amd64), opus_compare-gated on the fused arm64
// build where the scalar reference contracts a*b+c into FMA.
//
//go:noescape
func haar1Stride1NEON(x []float32, n0 int)

// haar1Stride2NEON applies the stride==2 Hadamard butterfly to n0 per-outer
// pairs (n0 4-element groups) in place, gathering pair halves with UZP/ZIP. Like
// the stride==1 kernel it uses separate FMUL/FADD/FSUB lane math: bit-exact with
// the non-fused scalar oracle (purego/amd64), opus_compare-gated on the fused
// arm64 build.
//
//go:noescape
func haar1Stride2NEON(x []float32, n0 int)

// haar1Stride4NEON applies the stride==4 Hadamard butterfly to n0 per-outer
// pairs (n0 8-element groups) in place. Each group's low four lanes butterfly
// directly against its high four lanes, so no shuffle is needed. Same lane math
// and bit-exactness posture as the other haar1 NEON kernels.
//
//go:noescape
func haar1Stride4NEON(x []float32, n0 int)
