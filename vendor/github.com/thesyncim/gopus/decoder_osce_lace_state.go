//go:build gopus_osce

package gopus

import (
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// decoderOSCELACEState carries decoder-side OSCE LACE/NoLACE runtime
// bookkeeping under the explicit extra-controls build. The `osceLACEModel`
// field follows the same pattern as the OSCE BWE binding: it is non-nil
// once `SetDNNBlob` has successfully bound an OSCE LACE-capable weights
// blob.
//
// libopus keeps a single shared OSCEModel inside `OpusDecoder` (see
// `dnn/osce.c`: `osce_init`) carrying both LACE and NoLACE postfilter
// weights; the per-channel postfilter state (LACEState / NoLACEState)
// lives in the silk decoder structs. The extra-controls build mirrors that shape
// with one LACE and one NoLACE runtime state per channel.
//
// Both LACE and NoLACE operate on a 20 ms @ 16 kHz int16 frame (320
// samples) in libopus; the scratch buffers below are sized accordingly.
// Stereo decode runs the forward pass sequentially on each channel,
// re-using the same scratch arena.
const (
	// osceLACEFrameSamples is the int16 sample count one libopus
	// `osce_enhance_frame` invocation consumes / produces (20 ms @ 16
	// kHz). Both LACE and NoLACE share this footprint.
	osceLACEFrameSamples = 320
	// osceLACEFeatureDim mirrors libopus dnn/osce_config.h
	// `OSCE_FEATURE_DIM = 93`. Features are emitted at the SILK
	// sub-frame cadence (4 sub-frames per 20 ms frame).
	osceLACEFeatureDim = 93
	// osceLACESubframesPerFrame matches libopus `psDec->nb_subfr` when
	// the postfilter is enabled (the LACE/NoLACE path is only valid
	// for 4-subframe frames at 16 kHz; see dnn/osce.c::osce_enhance_frame).
	osceLACESubframesPerFrame = 4
)

type decoderOSCELACEState struct {
	osceLACEModel *osceLACE.Model

	// Per-channel forward-pass runtime state mirroring libopus
	// `silk_channel_state.osce.state.{lace,nolace}`. The decoder keeps both
	// LACE and NoLACE state slots per channel because `pickOSCELACEMode`
	// can switch between the two modes from one packet to the next (e.g.
	// SILK NB -> SILK WB transition). libopus calls `osce_reset` on a mode
	// change; here we keep the inactive runtime's history zeroed via
	// Reset() so a return to that mode starts from a clean state.
	//
	// Slot 0 carries the mid/left channel, slot 1 the side/right channel
	// when a stereo packet is decoded on a stereo decoder. Mono decode
	// paths only use slot 0.
	osceLACERuntime   [2]osceLACE.LACEState
	osceNoLACERuntime [2]osceLACE.NoLACEState

	// Per-channel OSCE feature extractor state mirroring libopus
	// `silk_channel_state.osce.features` (the `OSCEFeatureState` struct):
	// the rolling 350-sample signal history, smoothed bit count and
	// pitch-hangover bookkeeping. Both LACE and NoLACE share one feature
	// extractor per channel because libopus emits a single 4 * 93 feature
	// vector per 20 ms frame independent of the postfilter method.
	osceLACEFeatures [2]osceLACE.FeatureState

	// Pre-allocated working buffers for the post-SILK LACE/NoLACE forward
	// pass so the decoder hot path does not allocate per-frame. The buffers
	// are sized for one channel; stereo runs the forward pass sequentially
	// on each channel re-using the same scratch.
	//
	// applyIn16 holds the int16 SILK lowband samples read from
	// `silk.Decoder.LatestNativeMono()` / `LatestNativeStereo()` before any
	// scaling. applyInFloat is the float32 view consumed by the LACE /
	// NoLACE forward pass (libopus scales by 1/32768.f at the start of
	// `osce_enhance_frame`). applyOutFloat is the enhanced float32 output
	// the network writes. applyOutInt16 is the requantised int16 view that
	// downstream consumers (e.g. the silk_resampler PCM path or OSCE BWE
	// when both are active) read from.
	applyIn16     [osceLACEFrameSamples]int16
	applyInFloat  [osceLACEFrameSamples]float32
	applyOutFloat [osceLACEFrameSamples]float32
	applyOutInt16 [osceLACEFrameSamples]int16

	// Per-frame conditioning features consumed by the LACE / NoLACE
	// pitch / feature embedding net. Sized for the maximum 4-subframe
	// invocation libopus supports.
	applyFeatures [osceLACESubframesPerFrame * osceLACEFeatureDim]float32
	applyNumBits  [2]float32
	applyPeriods  [osceLACESubframesPerFrame]int

	// prevLACEActive tracks whether the previous decoded frame ran LACE /
	// NoLACE. laceMethod and laceResetFrames mirror libopus osce.method and
	// features.reset: after a reset, the first eligible frame passes raw and
	// the second eligible frame cross-fades before steady enhancement resumes.
	prevLACEActive  bool
	laceMethod      osceLACEMode
	laceResetFrames [2]int
}
