//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

func algUnquantBandQEXT(x []int32, n, k, spread, blocks int, dec, extDec *rangecoding.Decoder, gain int32, extraBits int) uint {
	return uint(AlgUnquantQEXT(x, n, k, spread, blocks, dec, extDec, gain, extraBits))
}

func cubicUnquantBandQEXT(x []int32, n, resolution, blocks int, dec *rangecoding.Decoder, gain int32) int {
	return int(cubicUnquantQEXT(x, n, resolution, blocks, dec, gain))
}
