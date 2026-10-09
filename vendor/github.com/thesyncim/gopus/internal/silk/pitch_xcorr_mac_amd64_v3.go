//go:build amd64.v3

package silk

// pitchXcorrMAC32 follows the MAC16_16 accumulations in celt/pitch.h's
// four-lag xcorr_kernel_c. GCC contracts these accumulations for the AMD64 v3
// target. Keeping the operands in registers makes the Go v3 compiler emit one
// FMA.
//
//go:noinline
func pitchXcorrMAC32(acc, x, y float32) float32 {
	return x*y + acc
}
