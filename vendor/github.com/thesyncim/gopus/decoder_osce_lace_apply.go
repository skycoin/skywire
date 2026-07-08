//go:build gopus_osce

package gopus

import (
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
	"github.com/thesyncim/gopus/internal/silk"
)

// osceLACEMode picks the decoder-complexity selected OSCE method.
type osceLACEMode int

const (
	osceLACEModeNone   osceLACEMode = 0
	osceLACEModeLACE   osceLACEMode = 1
	osceLACEModeNoLACE osceLACEMode = 2
)

func pickOSCELACEMode(complexity int) osceLACEMode {
	if complexity >= 7 {
		return osceLACEModeNoLACE
	}
	if complexity >= 6 {
		return osceLACEModeLACE
	}
	return osceLACEModeNone
}

func (d *Decoder) installOSCELACESilkPostfilterHook(mode Mode, silkBW silk.Bandwidth, packetStereoLocal bool) func() {
	if d == nil || d.silkDecoder == nil {
		return func() {}
	}
	restore := func() {
		d.silkDecoder.SetNativePostfilterHook(nil)
	}
	if !d.osceLACEEnabled || !d.osceLACEModelLoaded {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return restore
	}
	state := d.osceLACE
	if state == nil || state.osceLACEModel == nil || !state.osceLACEModel.Loaded() {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return restore
	}
	if mode != ModeSILK || silkBW != silk.BandwidthWideband {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return restore
	}
	pickedMode := pickOSCELACEMode(int(d.complexity))
	if pickedMode == osceLACEModeNone {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return restore
	}

	channels := 1
	if packetStereoLocal && d.channels == 2 {
		channels = 2
	}
	d.prepareOSCELACEPostfilter(pickedMode, channels)
	d.silkDecoder.SetNativePostfilterHook(func(channel int, samples []int16, ctrl silk.LatestDecoderControl) bool {
		if channel < 0 || channel >= channels {
			return false
		}
		if ctrl.FsKHz != 16 || ctrl.NbSubfr != osceLACESubframesPerFrame || len(samples) < osceLACEFrameSamples {
			d.resetOSCELACEPostfilterState(packetStereoLocal)
			return false
		}
		if !d.applyOSCELACEMonoChannelWithControl(samples, pickedMode, channel, ctrl, true) {
			d.resetOSCELACEPostfilterState(packetStereoLocal)
			return false
		}
		return true
	})
	return restore
}

func (d *Decoder) applyOSCELACEMonoChannelWithControl(native []int16, mode osceLACEMode, channelIdx int, ctrl silk.LatestDecoderControl, ctrlOK bool) bool {
	if d == nil || d.osceLACE == nil {
		return false
	}
	if channelIdx < 0 || channelIdx > 1 {
		return false
	}
	state := d.osceLACE
	// libopus scales by 1/32768.f at the start of osce_enhance_frame; mirror
	// that so the forward pass receives float32 input. Capture the
	// pre-enhancement int16 view in applyIn16 so the cross-fade has the raw
	// input available even after the enhancement overwrites the native lowband.
	for i := 0; i < osceLACEFrameSamples; i++ {
		state.applyIn16[i] = native[i]
		state.applyInFloat[i] = float32(native[i]) * (1.0 / 32768.0)
	}
	// Populate features via the libopus-parity OSCE feature extractor.
	// libopus calls `osce_calculate_features(psDec, psDecCtrl, features,
	// numbits, periods, xq, num_bits)` at the top of `osce_enhance_frame`;
	// we mirror that by reading the cached `silk_decoder_control` for the
	// channel (decoded in the latest finalizeDecodedChannelFrame call) and
	// running the gopus port of the feature extractor on the int16 lowband
	// captured in `applyIn16`.
	//
	// When no decoder control has been cached yet (e.g. the latest decode
	// was a PLC frame which bypasses `finalizeDecodedChannelFrame`), we
	// fall back to zero features + OSCE_NO_PITCH_VALUE periods so the
	// forward pass still runs but the AdaComb stages degenerate to a no-op
	// pitch lag instead of over-reading their history buffers.
	for i := range state.applyFeatures {
		state.applyFeatures[i] = 0
	}
	for i := range state.applyNumBits {
		state.applyNumBits[i] = 0
	}
	for i := range state.applyPeriods {
		state.applyPeriods[i] = 7
	}
	if ctrlOK && ctrl.FsKHz == 16 && ctrl.NbSubfr == osceLACESubframesPerFrame {
		var fc osceLACE.FeatureControl
		fc.LPCOrder = ctrl.LPCOrder
		fc.PredCoefQ12[0] = ctrl.PredCoefQ12[0]
		fc.PredCoefQ12[1] = ctrl.PredCoefQ12[1]
		fc.LTPCoefQ14 = ctrl.LTPCoefQ14
		for sf := 0; sf < osceLACESubframesPerFrame; sf++ {
			fc.GainsQ16[sf] = ctrl.GainsQ16[sf]
			fc.PitchL[sf] = ctrl.PitchL[sf]
		}
		fc.SignalType = ctrl.SignalType
		numBits := ctrl.NumBits
		if numBits < 0 {
			numBits = 0
		}
		state.osceLACEFeatures[channelIdx].CalculateFeatures(
			state.applyFeatures[:],
			state.applyNumBits[:],
			state.applyPeriods[:],
			state.applyIn16[:osceLACEFrameSamples],
			&fc,
			numBits,
		)
	}
	switch mode {
	case osceLACEModeNoLACE:
		if err := state.osceNoLACERuntime[channelIdx].Process(
			state.applyInFloat[:osceLACEFrameSamples],
			state.applyOutFloat[:osceLACEFrameSamples],
			state.applyFeatures[:],
			state.applyNumBits[:],
			state.applyPeriods[:],
		); err != nil {
			return false
		}
	default:
		if err := state.osceLACERuntime[channelIdx].Process(
			state.applyInFloat[:osceLACEFrameSamples],
			state.applyOutFloat[:osceLACEFrameSamples],
			state.applyFeatures[:],
			state.applyNumBits[:],
			state.applyPeriods[:],
		); err != nil {
			return false
		}
	}
	state.applyOSCELACEOutputReset(channelIdx)
	// Requantise to int16 and write back into the native lowband so the
	// downstream silk_resampler / OSCE BWE consumer reads the postfilter
	// output. libopus mutates psDec->outBuf in place; we mirror that by
	// overwriting the scratch slice returned by LatestNativeMono /
	// LatestNativeStereo.
	for i := 0; i < osceLACEFrameSamples; i++ {
		q := osceFloatToInt16(state.applyOutFloat[i])
		state.applyOutInt16[i] = q
		native[i] = q
	}
	return true
}

func (d *Decoder) prepareOSCELACEPostfilter(mode osceLACEMode, channels int) {
	if d == nil || d.osceLACE == nil {
		return
	}
	state := d.osceLACE
	if channels < 1 {
		channels = 1
	}
	if channels > len(state.laceResetFrames) {
		channels = len(state.laceResetFrames)
	}
	if !state.prevLACEActive || state.laceMethod != mode {
		for ch := 0; ch < channels; ch++ {
			state.osceLACEFeatures[ch].Reset()
			switch mode {
			case osceLACEModeLACE:
				state.osceLACERuntime[ch].Reset()
			case osceLACEModeNoLACE:
				state.osceNoLACERuntime[ch].Reset()
			default:
				state.osceLACERuntime[ch].Reset()
				state.osceNoLACERuntime[ch].Reset()
			}
			state.laceResetFrames[ch] = 2
		}
	}
	state.laceMethod = mode
	state.prevLACEActive = true
}

func (state *decoderOSCELACEState) applyOSCELACEOutputReset(channelIdx int) {
	if state == nil || channelIdx < 0 || channelIdx >= len(state.laceResetFrames) {
		return
	}
	switch state.laceResetFrames[channelIdx] {
	case 0:
		return
	case 1:
		osceLACECrossFade10ms(state.applyOutFloat[:osceLACEFrameSamples], state.applyInFloat[:osceLACEFrameSamples], osceLACEFrameSamples)
		state.laceResetFrames[channelIdx] = 0
	default:
		copy(state.applyOutFloat[:osceLACEFrameSamples], state.applyInFloat[:osceLACEFrameSamples])
		state.laceResetFrames[channelIdx]--
	}
}

// osceLACEMarkInactiveIfModeIneligible clears the LACE-active flag when the
// current packet's mode/bandwidth does not satisfy the LACE/NoLACE gate
// (SILK-only at 16 kHz internal). This catches Hybrid / CELT / SILK-NB
// packets where the native SILK postfilter hook is not installed but the
// `prevLACEActive` transition tracking still needs to be updated.
//
// Without this clearing the next SILK WB packet would incorrectly skip
// the LACE fade-in cross-fade because `prevLACEActive` could still be true
// from many packets ago.
func (d *Decoder) osceLACEMarkInactiveIfModeIneligible(mode Mode, bandwidth Bandwidth) {
	if d == nil || d.osceLACE == nil {
		return
	}
	// LACE/NoLACE runs in SILK-only mode at 16 kHz internal sample rate.
	// Hybrid, CELT, and lower-bandwidth SILK bypass the postfilter.
	if mode == ModeSILK && bandwidth == BandwidthWideband {
		// SILK packet with a LACE-eligible bandwidth: the SILK-only post-
		// decode hook handles the flag itself based on the actual SILK
		// internal bandwidth.
		return
	}
	d.osceLACE.prevLACEActive = false
}

func (d *Decoder) resetOSCELACEPostfilterState(packetStereoLocal bool) {
	if d == nil || d.osceLACE == nil {
		return
	}
	channels := 1
	if packetStereoLocal && d.channels == 2 {
		channels = 2
	}
	for ch := 0; ch < channels; ch++ {
		d.osceLACE.osceLACEFeatures[ch].Reset()
		d.osceLACE.osceLACERuntime[ch].Reset()
		d.osceLACE.osceNoLACERuntime[ch].Reset()
		d.osceLACE.laceResetFrames[ch] = 0
	}
	d.osceLACE.prevLACEActive = false
	d.osceLACE.laceMethod = osceLACEModeNone
}
