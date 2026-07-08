//go:build gopus_osce

package multistream

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// OSCE LACE/NoLACE 20 ms @ 16 kHz frame footprint (mirrors libopus
// `osce_enhance_frame`). Both the LACE and NoLACE families share this layout.
const (
	streamOSCELACEFrameSamples      = 320
	streamOSCELACEFeatureDim        = 93
	streamOSCELACESubframesPerFrame = 4
)

// streamOSCEState carries decoder-side OSCE LACE/NoLACE + BWE runtime
// bookkeeping under the explicit extra-controls build. It is attached to the
// per-stream `streamState` so a multistream decoder can run the libopus OSCE
// postfilter family on its SILK lowband output, just like the single-stream
// decoder in package gopus. The state is lazily allocated when models are
// bound; the multistream Decoder fans `SetDNNBlob` / `SetOSCEBWE` /
// `SetOSCELACE` out to every stream so each child carries an independent
// per-channel runtime (libopus does the same: per-`silk_channel_state` LACE/
// NoLACE state and per-stream `silk_OSCE_BWE_struct`).
type streamOSCEState struct {
	// LACE/NoLACE postfilter on the SILK 16 kHz lowband.
	laceModel        *osceLACE.Model
	laceRuntime      [2]osceLACE.LACEState
	noLACERuntime    [2]osceLACE.NoLACEState
	laceFeatureState [2]osceLACE.FeatureState
	laceApplyIn16    [streamOSCELACEFrameSamples]int16
	laceApplyInF     [streamOSCELACEFrameSamples]float32
	laceApplyOutF    [streamOSCELACEFrameSamples]float32
	laceApplyOutI16  [streamOSCELACEFrameSamples]int16
	laceFeatures     [streamOSCELACESubframesPerFrame * streamOSCELACEFeatureDim]float32
	laceNumBits      [2]float32
	lacePeriods      [streamOSCELACESubframesPerFrame]int
	prevLACEActive   bool
	laceMethod       streamOSCELACEMode
	laceResetFrames  [2]int

	// OSCE BWE 16 kHz -> 48 kHz forward pass replacing `silk_resampler`.
	bweModel      *osceBWE.Model
	bweRuntime    [2]osceBWE.State
	bweFeatures   [2]osceBWE.FeatureState
	bweIn16       [320]float32
	bweIn16Int    [320]int16
	bweOut48      [3 * 320]float32
	bweFadeout48  [3 * 320]float32
	bweFeatBuf    [2 * osceBWE.FeatureDim]float32
	prevBWEActive bool

	// prevExtendedMode and bweMonoPrevNativeLast mirror the single-stream
	// gopus decoder's OSCE BWE bookkeeping (see package gopus
	// decoder_osce_bwe_apply.go). prevExtendedMode tracks the libopus
	// DecControl.prev_osce_extended_mode so the fade-in into BWE only fires
	// after an OSCE_MODE_SILK_ONLY / OSCE_MODE_HYBRID frame (not on a cold
	// start or CELT->BBWE transition). bweMonoPrevNativeLast carries the
	// previous mono frame's final 16 kHz sample, reproducing libopus'
	// &samplesOut1_tmp[n][1] one-sample input delay for osce_bwe.
	prevExtendedMode      int
	bweMonoPrevNativeLast int16
}

// OSCE extended-mode values mirror libopus dnn/osce.h OSCE_MODE_*. bweModeNone
// is the zero value matching a cold-started decoder, deliberately not one of
// SILK_ONLY/HYBRID so the first BWE frame is emitted without a fade-in.
const (
	bweModeNone     = 0
	bweModeSilkOnly = 1000
	bweModeHybrid   = 1001
	bweModeCeltOnly = 1002
	bweModeSilkBBWE = 1003
)
