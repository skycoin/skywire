//go:build arm64 && !purego

package celt

// mdctUseNeonMidFold enables the NEON middle-fold/store kernel in the forward
// MDCT. It is bit-identical per element to the scalar
// mdctStoreDirectStageFMALike path (TestMDCTMidFoldStoreNeonBitExact), so it
// runs on the default arm64 build only as a throughput optimization.
const mdctUseNeonMidFold = true

// mdctMidFoldStoreNeon writes blocks*4 outputs of the forward-MDCT middle
// fold: dst[bitrev[i0+j]] gets the pre-twiddled, scaled (re, im) pair built
// from samples[xp1+2j] (im, ascending) and samples[xp2-2j] (re, descending)
// with twiddles trig[i0+j] and trig[n4+i0+j].
//
//go:noescape
func mdctMidFoldStoreNeon(dst []kissCpx, bitrev []int, samples []float32, trig []float32, i0, n4, xp1, xp2, blocks int, preScale float32)
