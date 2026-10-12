//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

// These hooks keep the shared band driver buildable when ENABLE_QEXT is off.
// The QEXT encoder never receives an extension coder in this build.
func AlgQuantQEXT(x []int32, n, k, spread, blocks int, enc, extEnc *rangecoding.Encoder,
	gain int32, resynth bool, extraBits int, scratch *celtEncodeScratch) uint32 {
	return AlgQuant(x, n, k, spread, blocks, enc, gain, resynth, scratch)
}

func computeQEXTPVQRefineBits(_ *rangecoding.Encoder, _, _ int32, _ int) int {
	return 0
}
