package celt

// neonRoundsReductionTerm reports whether term i of an n-term multiply-accumulate
// reduction rounds its product before the add. In the NEON libopus build (clang
// -O3 with auto-vectorization) such loops run in 16-term vector blocks: each
// block's products are rounded (vector fmul) and added to the accumulator in
// element order, and only the n%16 remainder terms fuse (scalar fmadd). Builds
// paired with scalar libopus keep the plain a*b+c form, which fuses every term
// on arm64 and none on amd64, as clang and gcc do there.
func neonRoundsReductionTerm(i, n int) bool {
	return celtFusedFloat && i < n&^15
}
