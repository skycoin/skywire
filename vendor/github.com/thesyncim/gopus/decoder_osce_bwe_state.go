//go:build gopus_osce

package gopus

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// OSCE extended-mode values mirror the libopus dnn/osce.h OSCE_MODE_* enum that
// silk/dec_API.c keys the BWE cross-fade transitions on. bweModeNone is the
// zero value: it matches libopus' zero-initialised DecControl.prev_osce_extended_mode
// on a cold-started decoder (no preceding frame), which is deliberately NOT one
// of SILK_ONLY/HYBRID so the first BWE frame is emitted without a fade-in.
const (
	bweModeNone     = 0
	bweModeSilkOnly = 1000 // OSCE_MODE_SILK_ONLY
	bweModeHybrid   = 1001 // OSCE_MODE_HYBRID
	bweModeCeltOnly = 1002 // OSCE_MODE_CELT_ONLY
	bweModeSilkBBWE = 1003 // OSCE_MODE_SILK_BBWE
)

// decoderOSCEBWEState carries decoder-side OSCE BWE runtime bookkeeping under
// the explicit extra-controls build. The `osceBWEModel` field follows the same
// pattern as the FARGAN / Predictor bindings: it is non-nil once
// `SetDNNBlob` has successfully bound an OSCE BWE-capable weights blob.
//
// libopus keeps a per-channel `silk_OSCE_BWE_struct` in each
// `silk_channel_state` and runs `osce_bwe(...)` independently on each SILK
// lowband channel (see `silk/dec_API.c` around the `OSCE_MODE_SILK_BBWE`
// branch). The gopus runtime mirrors that with one `osceBWE.State` per
// channel; index 0 carries the mid/left channel state, index 1 the side/
// right channel state. Mono decode paths only use slot 0.
type decoderOSCEBWEState struct {
	osceBWEModel    *osceBWE.Model
	osceBWERuntime  [2]osceBWE.State
	osceBWEFeatures [2]osceBWE.FeatureState

	// Pre-allocated working buffers for the post-SILK BWE forward pass so
	// the decoder hot path does not allocate per-frame. The input/output
	// buffers are sized for one channel; stereo runs the forward pass
	// sequentially on each channel re-using the same scratch.
	applyIn16      [320]float32 // 20 ms @ 16 kHz max
	applyIn16Int   [320]int16   // signed-int16 view consumed by the feature extractor
	applyOut48     [3 * 320]float32
	applyFadeout48 [3 * 320]float32
	applyFeatures  [2 * osceBWE.FeatureDim]float32

	// prevBWEActive mirrors libopus DecControl.prev_osce_extended_mode for the
	// OSCE_MODE_SILK_BBWE bit. When the previous frame ran BWE but the current
	// frame does not (or vice versa), maybeApplyOSCEBWEPostSilk applies a 10 ms
	// cross-fade between the BWE output and the standard silk_resampler
	// output. This mirrors osce_bwe_cross_fade_10ms in libopus dec_API.c.
	prevBWEActive bool

	// prevExtendedMode tracks the full libopus DecControl.prev_osce_extended_mode
	// (one of the bweMode* values) across decoded frames. libopus only
	// cross-fades INTO BWE when the preceding frame was OSCE_MODE_SILK_ONLY or
	// OSCE_MODE_HYBRID (silk/dec_API.c): on a cold start (bweModeNone) or a
	// CELT->SILK_BBWE transition (bweModeCeltOnly, set by opus_decoder.c when
	// prev_mode == MODE_CELT_ONLY) the first BWE frame is emitted without a
	// fade-in. prevBWEActive alone cannot distinguish those cases.
	prevExtendedMode int

	// monoPrevNativeLast carries the last native (16 kHz) SILK sample of the
	// previous mono BWE frame. libopus feeds osce_bwe (both the BBWENet
	// forward pass and osce_bwe_calculate_features) from
	// &samplesOut1_tmp[n][1] -- the 16 kHz lowband shifted one sample earlier
	// than the freshly decoded frame, with samplesOut1_tmp[n][1] holding the
	// previous frame's final sample (the SILK sStereo.sMid[1] history). The
	// gopus mono lowband accessor (LatestNativeMono) returns the raw decoded
	// frame at [0]; staging this one-sample history reproduces libopus' BWE
	// input exactly. The stereo path already captures midFrame[1:] (the same
	// [1]-aligned slice) so it needs no separate history.
	monoPrevNativeLast int16
}
