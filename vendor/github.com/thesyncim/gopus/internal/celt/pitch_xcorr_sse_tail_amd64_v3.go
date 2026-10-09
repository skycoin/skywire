//go:build amd64.v3

package celt

// pitchXcorrSSETailMAC32 follows the MAC16_16 tail in
// celt/x86/pitch_sse.c:celt_inner_prod_sse. GCC contracts that scalar tail
// for the AMD64 v3 target. Keeping the operands in registers in this helper
// makes the Go v3 compiler emit the same single-rounding instruction.
//
//go:noinline
func pitchXcorrSSETailMAC32(acc, x, y float32) float32 {
	return x*y + acc
}
