//go:build arm64 && !purego

package celt

// mdctUsePostTwiddleNeon enables the vectorized forward-MDCT post-twiddle on
// the fused arm64 build. Bit-identical per element to the scalar loop
// (TestMDCTPostTwiddleNeonBitExact); purego keeps the scalar loop.
const mdctUsePostTwiddleNeon = true

//go:noescape
func mdctPostTwiddleNeon(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int)
