//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

func algUnquantBandQEXT(x []int32, n, k, spread, blocks int, dec, _ *rangecoding.Decoder, gain int32, _ int) uint {
	return uint(AlgUnquant(x, n, k, spread, blocks, dec, gain))
}

func cubicUnquantBandQEXT([]int32, int, int, int, *rangecoding.Decoder, int32) int {
	return 0
}
