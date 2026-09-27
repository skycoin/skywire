//go:build arm64 && !purego

package celt

// mdctFold1StoreNeon writes blocks*4 outputs of the forward-MDCT leading
// windowed fold (see mdct_fold_arm64.s for the per-element formulas). It is
// bit-identical per element to the scalar mdctMulAddMix/mdctMulSubMix +
// mdctStoreDirectStageFMALike sequence (TestMDCTFoldStoreNeonBitExact).
//
//go:noescape
func mdctFold1StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32)

// mdctFold3StoreNeon writes blocks*4 outputs of the forward-MDCT trailing
// windowed fold, bit-identical per element to the scalar
// mdctMulSubMixAlt/mdctMulAddMix + mdctStoreDirectStageFMALike sequence.
//
//go:noescape
func mdctFold3StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32)
