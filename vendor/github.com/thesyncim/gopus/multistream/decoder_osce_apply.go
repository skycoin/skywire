//go:build gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
	"github.com/thesyncim/gopus/internal/silk"
)

// applyOSCEPostSilk runs the OSCE BWE 16 kHz -> 48 kHz forward pass on the
// SILK lowband output of this stream decoder. LACE/NoLACE is installed as a
// native SILK postfilter hook before decode so it feeds the normal resampler.
// `out` is overwritten in place by the BWE forward pass when its gates fire.
//
// Both postfilters are SILK-only at 16 kHz internal sample rate. The helper
// is a no-op outside of `gopus_osce`.
func (d *streamState) applyOSCEPostSilk(out []float32, frameSize int, silkBW silk.Bandwidth, packetStereo bool) {
	if d == nil || d.osceState == nil {
		return
	}
	d.applyOSCEBWE(out, frameSize, silkBW, packetStereo)
}

func (d *streamState) applyOSCEPLCSilk(out []float32, frameSize int, silkBW silk.Bandwidth, packetStereo bool) {
	if d == nil || d.osceState == nil {
		return
	}
	if d.sampleRate == 48000 && silkBW == silk.BandwidthWideband {
		if d.osceLACEEnabled {
			d.resetOSCELACEState(packetStereo)
		}
		if d.osceBWEEnabled {
			d.applyOSCEBWE(out, frameSize, silkBW, packetStereo)
		}
		return
	}
	d.markOSCEInactiveIfModeIneligible(streamTOC{
		mode:      streamModeSILK,
		bandwidth: int(silkBW),
		stereo:    packetStereo,
	}, nil, frameSize)
}

func (d *streamState) installOSCELACESilkPostfilterHook(silkBW silk.Bandwidth, packetStereo bool) func() {
	if d == nil || d.silkDec == nil {
		return func() {}
	}
	restore := func() {
		d.silkDec.SetNativePostfilterHook(nil)
	}
	if !d.osceLACEEnabled {
		d.resetOSCELACEState(packetStereo)
		return restore
	}
	state := d.osceState
	if state == nil || state.laceModel == nil || !state.laceModel.Loaded() {
		d.resetOSCELACEState(packetStereo)
		return restore
	}
	if silkBW != silk.BandwidthWideband {
		d.resetOSCELACEState(packetStereo)
		return restore
	}
	mode := pickStreamOSCELACEMode(int(d.complexity))
	if mode == streamOSCELACEModeNone {
		d.resetOSCELACEState(packetStereo)
		return restore
	}

	channels := 1
	if packetStereo && d.channels == 2 {
		channels = 2
	}
	d.prepareOSCELACEState(mode, channels)
	d.silkDec.SetNativePostfilterHook(func(channel int, samples []int16, ctrl silk.LatestDecoderControl) bool {
		if channel < 0 || channel >= channels {
			return false
		}
		if ctrl.FsKHz != 16 || ctrl.NbSubfr != streamOSCELACESubframesPerFrame || len(samples) < streamOSCELACEFrameSamples {
			d.resetOSCELACEState(packetStereo)
			return false
		}
		if !d.runOSCELACEChannel(samples, mode, channel, ctrl, true) {
			d.resetOSCELACEState(packetStereo)
			return false
		}
		return true
	})
	return restore
}

type streamOSCELACEMode int

const (
	streamOSCELACEModeNone   streamOSCELACEMode = 0
	streamOSCELACEModeLACE   streamOSCELACEMode = 1
	streamOSCELACEModeNoLACE streamOSCELACEMode = 2
)

func pickStreamOSCELACEMode(complexity int) streamOSCELACEMode {
	if complexity >= 7 {
		return streamOSCELACEModeNoLACE
	}
	if complexity >= 6 {
		return streamOSCELACEModeLACE
	}
	return streamOSCELACEModeNone
}

func (d *streamState) runOSCELACEChannel(native []int16, mode streamOSCELACEMode, channelIdx int, ctrl silk.LatestDecoderControl, ctrlOK bool) bool {
	if d == nil || d.osceState == nil {
		return false
	}
	if channelIdx < 0 || channelIdx > 1 {
		return false
	}
	state := d.osceState
	for i := 0; i < streamOSCELACEFrameSamples; i++ {
		state.laceApplyIn16[i] = native[i]
		state.laceApplyInF[i] = float32(native[i]) * (1.0 / 32768.0)
	}
	for i := range state.laceFeatures {
		state.laceFeatures[i] = 0
	}
	for i := range state.laceNumBits {
		state.laceNumBits[i] = 0
	}
	for i := range state.lacePeriods {
		state.lacePeriods[i] = 7
	}
	if ctrlOK && ctrl.FsKHz == 16 && ctrl.NbSubfr == streamOSCELACESubframesPerFrame {
		var fc osceLACE.FeatureControl
		fc.LPCOrder = ctrl.LPCOrder
		fc.PredCoefQ12[0] = ctrl.PredCoefQ12[0]
		fc.PredCoefQ12[1] = ctrl.PredCoefQ12[1]
		fc.LTPCoefQ14 = ctrl.LTPCoefQ14
		for sf := 0; sf < streamOSCELACESubframesPerFrame; sf++ {
			fc.GainsQ16[sf] = ctrl.GainsQ16[sf]
			fc.PitchL[sf] = ctrl.PitchL[sf]
		}
		fc.SignalType = ctrl.SignalType
		numBits := ctrl.NumBits
		if numBits < 0 {
			numBits = 0
		}
		state.laceFeatureState[channelIdx].CalculateFeatures(
			state.laceFeatures[:],
			state.laceNumBits[:],
			state.lacePeriods[:],
			state.laceApplyIn16[:streamOSCELACEFrameSamples],
			&fc,
			numBits,
		)
	}
	switch mode {
	case streamOSCELACEModeNoLACE:
		if err := state.noLACERuntime[channelIdx].Process(
			state.laceApplyInF[:streamOSCELACEFrameSamples],
			state.laceApplyOutF[:streamOSCELACEFrameSamples],
			state.laceFeatures[:],
			state.laceNumBits[:],
			state.lacePeriods[:],
		); err != nil {
			return false
		}
	default:
		if err := state.laceRuntime[channelIdx].Process(
			state.laceApplyInF[:streamOSCELACEFrameSamples],
			state.laceApplyOutF[:streamOSCELACEFrameSamples],
			state.laceFeatures[:],
			state.laceNumBits[:],
			state.lacePeriods[:],
		); err != nil {
			return false
		}
	}
	state.applyOSCELACEOutputReset(channelIdx)
	for i := 0; i < streamOSCELACEFrameSamples; i++ {
		q := streamOSCEFloatToInt16(state.laceApplyOutF[i])
		state.laceApplyOutI16[i] = q
		native[i] = q
	}
	return true
}

func (d *streamState) prepareOSCELACEState(mode streamOSCELACEMode, channels int) {
	if d == nil || d.osceState == nil {
		return
	}
	state := d.osceState
	if channels < 1 {
		channels = 1
	}
	if channels > len(state.laceResetFrames) {
		channels = len(state.laceResetFrames)
	}
	if !state.prevLACEActive || state.laceMethod != mode {
		for ch := 0; ch < channels; ch++ {
			state.laceFeatureState[ch].Reset()
			switch mode {
			case streamOSCELACEModeLACE:
				state.laceRuntime[ch].Reset()
			case streamOSCELACEModeNoLACE:
				state.noLACERuntime[ch].Reset()
			default:
				state.laceRuntime[ch].Reset()
				state.noLACERuntime[ch].Reset()
			}
			state.laceResetFrames[ch] = 2
		}
	}
	state.laceMethod = mode
	state.prevLACEActive = true
}

func (d *streamState) resetOSCELACEState(packetStereo bool) {
	if d == nil || d.osceState == nil {
		return
	}
	state := d.osceState
	channels := 1
	if packetStereo && d.channels == 2 {
		channels = 2
	}
	for ch := 0; ch < channels; ch++ {
		state.laceFeatureState[ch].Reset()
		state.laceRuntime[ch].Reset()
		state.noLACERuntime[ch].Reset()
		state.laceResetFrames[ch] = 0
	}
	state.prevLACEActive = false
	state.laceMethod = streamOSCELACEModeNone
}

func (state *streamOSCEState) applyOSCELACEOutputReset(channelIdx int) {
	if state == nil || channelIdx < 0 || channelIdx >= len(state.laceResetFrames) {
		return
	}
	switch state.laceResetFrames[channelIdx] {
	case 0:
		return
	case 1:
		streamOSCELACECrossFade10msFloat(state.laceApplyOutF[:streamOSCELACEFrameSamples], state.laceApplyInF[:streamOSCELACEFrameSamples])
		state.laceResetFrames[channelIdx] = 0
	default:
		copy(state.laceApplyOutF[:streamOSCELACEFrameSamples], state.laceApplyInF[:streamOSCELACEFrameSamples])
		state.laceResetFrames[channelIdx]--
	}
}

func (d *streamState) applyOSCEBWE(out []float32, frameSize int, silkBW silk.Bandwidth, packetStereo bool) bool {
	if d == nil || d.osceState == nil {
		return false
	}
	state := d.osceState
	if !d.osceBWEEnabled || state.bweModel == nil {
		if state.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereo)
		}
		// SILK frame fell back to the standard resampler (mode == SILK_ONLY).
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}
	if d.complexity < 4 || d.sampleRate != 48000 || silkBW != silk.BandwidthWideband {
		if state.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereo)
		}
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		if state.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereo)
		}
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}
	in16Per := in48Per / 3
	if !state.prevBWEActive {
		// libopus calls osce_bwe_reset when prev_osce_extended_mode is not
		// OSCE_MODE_SILK_BBWE.
		for i := range state.bweRuntime {
			state.bweRuntime[i].Reset()
			state.bweFeatures[i].Reset()
		}
		state.bweMonoPrevNativeLast = 0
	}
	// libopus only cross-fades into BWE when the preceding frame was
	// OSCE_MODE_SILK_ONLY or OSCE_MODE_HYBRID (silk/dec_API.c); a cold start or
	// CELT->SILK_BBWE transition emits the BWE output directly.
	fadeInIntoBWE := state.prevExtendedMode == bweModeSilkOnly || state.prevExtendedMode == bweModeHybrid
	if packetStereo && d.channels == 2 {
		if !state.bweRuntime[0].Loaded() || !state.bweRuntime[1].Loaded() {
			return false
		}
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDec.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return false
		}
		numFrames := in16Per / 160

		for i := 0; i < in16Per; i++ {
			state.bweIn16Int[i] = leftNative[i]
			state.bweIn16[i] = float32(leftNative[i]) / 32768.0
		}
		state.bweFeatures[0].CalculateFeatures(
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
			state.bweIn16Int[:in16Per],
		)
		if err := state.bweRuntime[0].ProcessDelayed(
			state.bweIn16[:in16Per],
			state.bweOut48[:in48Per],
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			return false
		}
		if fadeInIntoBWE {
			for i := 0; i < in48Per; i++ {
				state.bweFadeout48[i] = out[2*i]
			}
			streamOSCEBWECrossFade10ms(state.bweOut48[:in48Per], state.bweFadeout48[:in48Per], 480)
		}
		for i := 0; i < in48Per; i++ {
			out[2*i] = state.bweOut48[i]
		}
		for i := 0; i < in16Per; i++ {
			state.bweIn16Int[i] = rightNative[i]
			state.bweIn16[i] = float32(rightNative[i]) / 32768.0
		}
		state.bweFeatures[1].CalculateFeatures(
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
			state.bweIn16Int[:in16Per],
		)
		if err := state.bweRuntime[1].ProcessDelayed(
			state.bweIn16[:in16Per],
			state.bweOut48[:in48Per],
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = out[2*i]
			}
			state.prevBWEActive = true
			return true
		}
		if fadeInIntoBWE {
			for i := 0; i < in48Per; i++ {
				state.bweFadeout48[i] = out[2*i+1]
			}
			streamOSCEBWECrossFade10ms(state.bweOut48[:in48Per], state.bweFadeout48[:in48Per], 480)
		}
		for i := 0; i < in48Per; i++ {
			out[2*i+1] = state.bweOut48[i]
		}
		state.prevBWEActive = true
		state.prevExtendedMode = bweModeSilkBBWE
		return true
	}

	if !state.bweRuntime[0].Loaded() {
		return false
	}
	native, fsKHz := d.silkDec.LatestNativeMono()
	if native == nil || fsKHz != 16 || len(native) < in16Per {
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}
	// libopus drives osce_bwe from &samplesOut1_tmp[n][1] -- the 16 kHz lowband
	// shifted one sample earlier than the freshly decoded frame, with index 0
	// carrying the previous frame's final sample. LatestNativeMono returns the
	// raw decoded frame at [0], so prepend the previous frame's last sample and
	// carry the final decoded sample into the next call.
	state.bweIn16Int[0] = state.bweMonoPrevNativeLast
	state.bweIn16[0] = float32(state.bweMonoPrevNativeLast) / 32768.0
	for i := 1; i < in16Per; i++ {
		state.bweIn16Int[i] = native[i-1]
		state.bweIn16[i] = float32(native[i-1]) / 32768.0
	}
	state.bweMonoPrevNativeLast = native[in16Per-1]
	numFrames := in16Per / 160
	state.bweFeatures[0].CalculateFeatures(
		state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		state.bweIn16Int[:in16Per],
	)
	if err := state.bweRuntime[0].ProcessDelayed(
		state.bweIn16[:in16Per],
		state.bweOut48[:in48Per],
		state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
	); err != nil {
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}
	if fadeInIntoBWE {
		if d.channels == 1 {
			streamOSCEBWECrossFade10ms(state.bweOut48[:in48Per], out[:in48Per], 480)
		} else {
			for i := 0; i < in48Per; i++ {
				state.bweFadeout48[i] = out[2*i]
			}
			streamOSCEBWECrossFade10ms(state.bweOut48[:in48Per], state.bweFadeout48[:in48Per], 480)
		}
	}
	if d.channels == 1 {
		copy(out[:in48Per], state.bweOut48[:in48Per])
	} else {
		for i := 0; i < in48Per; i++ {
			v := state.bweOut48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
	state.prevBWEActive = true
	return true
}

func (d *streamState) markOSCEInactiveIfModeIneligible(toc streamTOC, out []float32, frameSize int) {
	if d == nil || d.osceState == nil {
		return
	}
	if toc.mode == streamModeSILK && toc.bandwidth == 2 {
		return
	}
	d.resetOSCEInactiveState(toc.stereo)
	// Record the libopus osce_extended_mode so the next BWE frame reproduces
	// the fade-in gating: a Hybrid or SILK-only predecessor arms the fade-in,
	// a CELT-only predecessor suppresses it.
	switch toc.mode {
	case streamModeCELT:
		d.osceState.prevExtendedMode = bweModeCeltOnly
	case streamModeHybrid:
		d.osceState.prevExtendedMode = bweModeHybrid
	default:
		d.osceState.prevExtendedMode = bweModeSilkOnly
	}
}

func (d *streamState) resetOSCEInactiveState(packetStereo bool) {
	if d == nil || d.osceState == nil {
		return
	}
	if d.osceState.prevLACEActive {
		d.resetOSCELACEState(packetStereo)
	}
	d.osceState.prevBWEActive = false
}

func (d *streamState) applyOSCEBWEFadeOut(out []float32, frameSize int, packetStereo bool) {
	if d == nil || d.osceState == nil || d.silkDec == nil || !d.osceState.bweRuntime[0].Loaded() {
		return
	}
	if d.sampleRate != 48000 {
		return
	}
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		return
	}
	in16Per := in48Per / 3
	state := d.osceState
	numFrames := in16Per / 160

	// stage applies the libopus &samplesOut1_tmp[n][1] one-sample input delay
	// for mono; the stereo accessor already returns [1]-aligned slices.
	runChannel := func(native []int16, channelIdx int, stage bool) bool {
		if channelIdx < 0 || channelIdx > 1 || !state.bweRuntime[channelIdx].Loaded() || len(native) < in16Per {
			return false
		}
		if stage {
			state.bweIn16Int[0] = state.bweMonoPrevNativeLast
			state.bweIn16[0] = float32(state.bweMonoPrevNativeLast) / 32768.0
			for i := 1; i < in16Per; i++ {
				state.bweIn16Int[i] = native[i-1]
				state.bweIn16[i] = float32(native[i-1]) / 32768.0
			}
			state.bweMonoPrevNativeLast = native[in16Per-1]
		} else {
			for i := 0; i < in16Per; i++ {
				state.bweIn16Int[i] = native[i]
				state.bweIn16[i] = float32(native[i]) / 32768.0
			}
		}
		state.bweFeatures[channelIdx].CalculateFeatures(
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
			state.bweIn16Int[:in16Per],
		)
		return state.bweRuntime[channelIdx].ProcessDelayed(
			state.bweIn16[:in16Per],
			state.bweOut48[:in48Per],
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		) == nil
	}

	if packetStereo && d.channels == 2 {
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDec.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return
		}
		if runChannel(leftNative, 0, false) {
			for i := 0; i < in48Per; i++ {
				state.bweFadeout48[i] = out[2*i]
			}
			streamOSCEBWECrossFade10ms(state.bweFadeout48[:in48Per], state.bweOut48[:in48Per], 480)
			for i := 0; i < in48Per; i++ {
				out[2*i] = state.bweFadeout48[i]
			}
		}
		if runChannel(rightNative, 1, false) {
			for i := 0; i < in48Per; i++ {
				state.bweFadeout48[i] = out[2*i+1]
			}
			streamOSCEBWECrossFade10ms(state.bweFadeout48[:in48Per], state.bweOut48[:in48Per], 480)
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = state.bweFadeout48[i]
			}
		}
		return
	}

	native, fsKHz := d.silkDec.LatestNativeMono()
	if native == nil || fsKHz != 16 || !runChannel(native, 0, true) {
		return
	}
	if d.channels == 1 {
		streamOSCEBWECrossFade10ms(out[:in48Per], state.bweOut48[:in48Per], 480)
		return
	}
	if d.channels == 2 {
		for i := 0; i < in48Per; i++ {
			state.bweFadeout48[i] = out[2*i]
		}
		streamOSCEBWECrossFade10ms(state.bweFadeout48[:in48Per], state.bweOut48[:in48Per], 480)
		for i := 0; i < in48Per; i++ {
			v := state.bweFadeout48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
}

// streamOSCEBWECrossFade10ms mirrors libopus dnn/osce_features.c
// osce_bwe_cross_fade_10ms for float32 PCM. It writes the blended samples back
// into xFadein.
func streamOSCEBWECrossFade10ms(xFadein, xFadeout []float32, length int) {
	if length < 480 || len(xFadein) < 480 || len(xFadeout) < 480 {
		return
	}
	const oneThird = 1.0 / 3.0
	for i := 0; i < 160; i++ {
		var diff float32
		if i != 159 {
			diff = streamOSCEWindow[i+1] - streamOSCEWindow[i]
		}
		wCurr := streamOSCEWindow[i]
		xFadein[3*i+0] = float32(streamOSCEBWECrossFadeSample(wCurr, xFadein[3*i+0], xFadeout[3*i+0])) * (1.0 / 32768.0)
		wCurr += diff * oneThird
		xFadein[3*i+1] = float32(streamOSCEBWECrossFadeSample(wCurr, xFadein[3*i+1], xFadeout[3*i+1])) * (1.0 / 32768.0)
		wCurr += diff * oneThird
		xFadein[3*i+2] = float32(streamOSCEBWECrossFadeSample(wCurr, xFadein[3*i+2], xFadeout[3*i+2])) * (1.0 / 32768.0)
	}
}

func streamOSCEBWECrossFadeSample(weight, fadein, fadeout float32) int16 {
	fi := streamOSCEFloatToInt16(fadein)
	fo := streamOSCEFloatToInt16(fadeout)
	v := weight*float32(fi) + (1.0-weight)*float32(fo) + 0.5
	return int16(int32(v))
}

func streamOSCEFloatToInt16(x float32) int16 {
	return opusmath.Float32ToInt16OSCEOutputScale(x)
}

// streamOSCELACECrossFade10msInt16 mirrors `osceLACECrossFade10msInt16` in
// package gopus: 10 ms (160 sample) cross-fade between the postfilter output
// (`xEnhanced`) and the raw pre-enhancement input (`xIn`), written back into
// `xEnhanced`. Re-uses the libopus `osce_window[]` half-window weights.
func streamOSCELACECrossFade10msFloat(xEnhanced, xIn []float32) {
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := streamOSCEWindow[i]
		xEnhanced[i] = w*xEnhanced[i] + (1.0-w)*xIn[i]
	}
}
