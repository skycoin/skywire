//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// fixedCustomTables is nil in standard Opus modes. A custom-mode build supplies
// CELTMode's allocation and pulse-cache tables without linking that machinery
// into the default fixed-point codec.
type fixedCustomTables interface {
	InitCapsInto([]int32, int, int, int)
	BitsToPulses(int, int, int) int
	PulsesToBits(int, int, int) int
	MaxPulsesBits(int, int) int
	ComputeAllocationWithEncoderStartInto(*celt.AllocEncodeScratch, *rangecoding.Encoder,
		int, int, int, int, []int32, []int32, int, int, bool, int, int, int) *celt.AllocationResult
	DecodeCELTAllocation(*rangecoding.Decoder, int, int, int, int, int, bool) celt.CELTDecodeAllocation
}
