//go:build gopus_fixed_point

package gopus

import "github.com/thesyncim/gopus/internal/fixedpoint"

// decoderFixedFields carries the FIXED_POINT integer CELT decoder used by the
// gopus_fixed_point build to produce integer-exact CELT-only output through the
// public Decoder. It is created lazily on the first CELT-only frame so the
// allocation is avoided for SILK-only streams.
//
// For a packet whose every frame is handled by the integer CELT decoder, the
// int16 and opus_res (int24) outputs are accumulated here so the int16/int24
// public wrappers can read them directly, bypassing the lossy float32->int
// conversion. fixedAllHandled records whether the in-flight packet qualified.
type decoderFixedFields struct {
	fixedCELT    *fixedpoint.CELTDecoder
	fixedCELTPCM []int16

	// fixedInt16 / fixedRes accumulate the integer CELT output of the current
	// packet, interleaved at the API rate (channels stride). fixedCursor is the
	// running write offset (in interleaved elements) used while a packet is
	// decoded.
	fixedInt16  []int16
	fixedRes    []int32
	fixedCursor int

	// fixedAllHandled is true while every frame of the in-flight packet has been
	// decoded by the integer CELT path (and is therefore recoverable bit-exact
	// from fixedInt16/fixedRes). It is reset at packet start and cleared the
	// moment any frame falls through to the float decoder.
	fixedAllHandled bool
	// fixedPacketActive guards the accumulation: it is true only between
	// beginFixedPacket and the int16/int24 wrapper consuming the result.
	fixedPacketActive bool

	// fixedHybridRes is the per-frame interleaved opus_res scratch that holds the
	// SILK lowband (INT16TORES) before the integer CELT highband accumulates onto
	// it; it then carries the combined hybrid opus_res output.
	fixedHybridRes []int32
	// fixedHybridInt16 is the per-frame int16 view (Res2Int16) of fixedHybridRes.
	fixedHybridInt16 []int16
	// fixedHybridEnd is the CELT end band (CELT_SET_END_BAND) for the in-flight
	// hybrid frame, captured before the hybrid decode invokes the highband hook.
	fixedHybridEnd int
	// fixedHybridErr captures an error from the highband hook (the hook signature
	// is void to match the hybrid float path; the dispatch checks it afterwards).
	fixedHybridErr error
	// fixedHybridReset records whether the integer CELT cross-frame state must be
	// reset before the in-flight hybrid frame (mirroring the float OPUS_RESET_STATE
	// on a mode transition), set before the hybrid decode.
	fixedHybridReset bool

	// fixedRedundantRes holds the integer (opus_res) redundancy frame decoded by
	// the integer CELT decoder for a Hybrid packet carrying CELT redundancy. It is
	// F5*channels interleaved samples, mirroring the float redundant_audio buffer.
	fixedRedundantRes []int32
	// fixedRedundantValid reports that fixedRedundantRes holds a usable integer
	// redundancy frame for the in-flight Hybrid frame.
	fixedRedundantValid bool
	// fixedTransitionRes holds the integer (opus_res) 5 ms CELT PLC transition
	// frame decoded by the integer CELT decoder for a Hybrid frame whose previous
	// frame was CELT-only, mirroring the float pcm_transition buffer.
	fixedTransitionRes []int32
	// fixedTransitionValid reports that fixedTransitionRes holds a usable integer
	// transition frame for the in-flight Hybrid frame.
	fixedTransitionValid bool
	// fixedRedundantScratch is reusable int16 scratch for the integer redundancy /
	// transition CELT decodes (whose opus_res output is read via LastRes).
	fixedRedundantScratch []int16
	// fixedHybridFrameActive is true between prepareFixedHybrid and the end of the
	// frame's redundancy / transition post-processing, so the integer redundancy /
	// transition helpers run even after the highband hook is disarmed.
	fixedHybridFrameActive bool

	// fixedSuppressCELTPLCHook suppresses the integer FIXED_POINT celt_decode_lost
	// hook on a recursive data==nil CELT frame. The Hybrid transition path already
	// produced the integer 5 ms CELT PLC frame via fixedDecodeTransitionPLC; the
	// recursive float PLC decode that follows it only fills the float pcmTransition
	// buffer and must not advance the integer CELT PLC state a second time. It is
	// set around that recursive decode and cleared afterwards.
	fixedSuppressCELTPLCHook bool

	// fixedRedundancyApplied / fixedTransitionApplied count the frames whose
	// integer output was finished by the opus_res-domain redundancy / transition
	// crossfade. They are diagnostic counters used by the parity gate to confirm
	// the paths are exercised.
	fixedRedundancyApplied int
	fixedTransitionApplied int

	// fixedHybridPLCSilk receives the resampled int16 SILK PLC lowband captured
	// during the float hybrid PLC decode (armed via the SILK decoder), used to
	// build the opus_res lowband the integer CELT highband concealment accumulates
	// onto for a lost hybrid frame.
	fixedHybridPLCSilk []int16
	// fixedHybridPLCRes / fixedHybridPLCInt16 hold the combined opus_res / int16
	// integer hybrid PLC output stashed for the int16/int24 wrappers.
	fixedHybridPLCRes   []int32
	fixedHybridPLCInt16 []int16
	// fixedHybridPLCStereo records whether the captured SILK PLC lowband is stereo
	// (interleaved L/R) or mono (one channel); fixedHybridPLCChannels is the output
	// channel count for the in-flight lost hybrid frame.
	fixedHybridPLCStereo   bool
	fixedHybridPLCChannels int
}
