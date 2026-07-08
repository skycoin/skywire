//go:build gopus_fixed_point

package multistream

import "github.com/thesyncim/gopus/internal/fixedpoint"

// streamFixedFields carries the FIXED_POINT integer CELT decoder used by the
// gopus_fixed_point build to produce integer-exact opus_res output for a single
// elementary stream of a multistream packet. It is embedded in streamState and
// the CELT decoder is created lazily on the first CELT-only / Hybrid frame so
// SILK-only streams pay no allocation.
type streamFixedFields struct {
	fixedCELT    *fixedpoint.CELTDecoder
	fixedCELTPCM []int16
	fixedRes     []int32

	// fixedHybridHook implements hybrid.FixedHybridHighband for the integer
	// Hybrid highband decode (start band 17, celt_accum onto the SILK opus_res
	// lowband). It is armed on the stream's hybrid decoder only while an integer
	// Hybrid frame is in flight and shares fixedCELT with the CELT-only path.
	fixedHybridHook *streamFixedHybridHook
	fixedHybridRes  []int32
	fixedHybridEnd  int
	// fixedHybridRedundant records the Opus-layer redundancy decision the float
	// Hybrid afterSilk callback already read from the shared range decoder. The
	// integer highband hook reads it (rather than re-parsing the flag, which the
	// shared decoder has already advanced past) to decline redundant frames.
	fixedHybridRedundant bool
	fixedHybridHandled   bool
}

// setFixedHybridRedundancy records the Opus-layer redundancy decision the float
// Hybrid afterSilk callback read from the shared range decoder, so the integer
// highband hook (which runs after afterSilk) can decline a redundant frame
// without re-parsing the already-consumed flag.
func (d *streamState) setFixedHybridRedundancy(redundant bool) {
	d.fixedHybridRedundant = redundant
}
