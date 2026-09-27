//go:build arm64 && !purego

package celt

// combUsesNeon enables the vectorized constant-gain comb body on the fused
// arm64 build. It is bit-identical per element to the contracted scalar
// combFilterConstValue sequence (TestCombFilterConstNeonBitExact); purego
// keeps the scalar loops.
const combUsesNeon = true

//go:noescape
func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int)
