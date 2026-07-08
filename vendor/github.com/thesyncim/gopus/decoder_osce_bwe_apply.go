//go:build gopus_osce

package gopus

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	"github.com/thesyncim/gopus/internal/silk"
)

// maybeApplyOSCEBWEPostSilk runs the OSCE BWE 16 kHz -> 48 kHz forward pass on
// the SILK lowband output and writes the bandwidth-extended PCM into `out`,
// overwriting the standard silk_resampler output.
//
// The hook mirrors the libopus SILK_BBWE extended mode:
//
//	st->DecControl.osce_extended_mode == OSCE_MODE_SILK_BBWE
//
// which triggers when:
//   - the OSCE BWE control is enabled (SetOSCEBWE(true))
//   - a valid OSCE BWE model was bound via SetDNNBlob
//   - the packet was decoded as SILK-only at WB internal sample rate (or PLC
//     is running with the previous packet matching that profile)
//   - the API sample rate is 48 kHz
//
// Phase 3 of the wiring computes the per-10ms BBWENet feature vector from the
// raw int16 SILK lowband samples via the ported `osce_bwe_calculate_features`
// (see internal/osce/bwe/features.go). On transitions between BWE-active and
// BWE-inactive frames the helper runs a 10 ms cross-fade between the BWE
// output and the standard silk_resampler output, mirroring
// osce_bwe_cross_fade_10ms in libopus dec_API.c. Errors from the runtime
// (e.g. unsupported frame size) fall through silently so the standard
// resampler output is retained.
//
// `out` is the gopus output buffer holding `frameSize * channels` float32
// samples in [-1, 1]. Returns true when the BWE pass executed and overwrote
// the output; returns false when conditions are not met (callers keep the
// standard resampler output untouched).
//
// Stereo handling: libopus runs `osce_bwe(...)` independently on each SILK
// lowband channel using a per-channel `silk_OSCE_BWE_struct` (see
// `silk/dec_API.c` around `OSCE_MODE_SILK_BBWE`). The gopus runtime mirrors
// that by keeping `[2]osceBWE.State` slots in `decoderOSCEBWEState`. For a
// stereo packet at a stereo decoder both per-channel runtimes are invoked
// sequentially and the result is interleaved into `out`.
func (d *Decoder) maybeApplyOSCEBWEPostSilk(
	out []float32,
	frameSize int,
	mode Mode,
	silkBW silk.Bandwidth,
	packetStereoLocal bool,
) bool {
	if d == nil || d.osceBWE == nil {
		return false
	}
	if !d.osceBWEEnabled || !d.osceBWEModelLoaded {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		// This SILK frame fell back to the standard resampler -> libopus
		// osce_extended_mode == OSCE_MODE_SILK_ONLY, which arms the fade-in for
		// the next BWE frame.
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	// libopus only enables OSCE_MODE_SILK_BBWE for SILK-only mode at 48 kHz
	// API and 16 kHz internal sample rate. Hybrid mode keeps the standard
	// silk_resampler path even when BWE is requested. See opus_decoder.c
	// around `OSCE_MODE_SILK_BBWE`. PLC re-uses this same gate (the caller
	// passes the previous packet's mode/bandwidth, which is how libopus
	// derives the BWE eligibility on `data == NULL`).
	if d.complexity < 4 || mode != ModeSILK || d.sampleRate != 48000 || silkBW != silk.BandwidthWideband {
		// BWE is inactive this frame. If the previous frame ran BWE we still
		// need a cross-fade so the resampler/BWE boundary is not audible.
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		// SILK-only fallback to the standard resampler (mode == SILK_ONLY).
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	// The runtime only supports 10 ms (160 sample) and 20 ms (320 sample)
	// frames at 16 kHz, which map to 480 / 960 samples per channel at 48 kHz.
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	in16Per := in48Per / 3
	state := d.osceBWE
	if !state.prevBWEActive {
		// libopus calls osce_bwe_reset when prev_osce_extended_mode is not
		// OSCE_MODE_SILK_BBWE (silk/dec_API.c).
		for i := range state.osceBWERuntime {
			state.osceBWERuntime[i].Reset()
			state.osceBWEFeatures[i].Reset()
		}
		state.monoPrevNativeLast = 0
	}
	// libopus only cross-fades the BWE output against a fresh standard-resampler
	// pass when the preceding frame was OSCE_MODE_SILK_ONLY or OSCE_MODE_HYBRID
	// (silk/dec_API.c). A cold start (bweModeNone) or a CELT->SILK_BBWE
	// transition (bweModeCeltOnly) emits the BWE output directly with no
	// fade-in, so the first BWE frame matches libopus sample-for-sample.
	fadeInIntoBWE := state.prevExtendedMode == bweModeSilkOnly || state.prevExtendedMode == bweModeHybrid

	// Stereo packet on a stereo decoder: run the per-channel forward pass
	// for the mid/left and side/right lowband signals independently and
	// interleave the result. This mirrors libopus where the
	// `for ( n = 0; n < ... nChannelsInternal; n++ )` loop calls
	// `osce_bwe(...)` once per channel using `channel_state[n].osce_bwe`.
	if packetStereoLocal && d.channels == 2 {
		if !d.osceBWE.osceBWERuntime[0].Loaded() || !d.osceBWE.osceBWERuntime[1].Loaded() {
			return false
		}
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDecoder.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return false
		}
		numFrames := in16Per / 160

		// Left/mid channel forward pass. Stage the int16 lowband for the
		// per-channel feature extractor and normalise to float32 [-1, 1]
		// for the BBWENet forward pass. libopus computes features
		// independently for each channel using channel_state[n].osce_bwe.
		for i := 0; i < in16Per; i++ {
			state.applyIn16Int[i] = leftNative[i]
			state.applyIn16[i] = float32(leftNative[i]) / 32768.0
		}
		state.osceBWEFeatures[0].CalculateFeatures(
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
			state.applyIn16Int[:in16Per],
		)
		if err := state.osceBWERuntime[0].ProcessDelayed(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			return false
		}
		if fadeInIntoBWE {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i]
			}
			osceBWECrossFade10ms(state.applyOut48[:in48Per], state.applyFadeout48[:in48Per], 480)
		}
		// Interleave left channel into out[2*i].
		for i := 0; i < in48Per; i++ {
			out[2*i] = state.applyOut48[i]
		}

		// Right/side channel forward pass. Compute per-channel features
		// from the right-channel int16 lowband.
		for i := 0; i < in16Per; i++ {
			state.applyIn16Int[i] = rightNative[i]
			state.applyIn16[i] = float32(rightNative[i]) / 32768.0
		}
		state.osceBWEFeatures[1].CalculateFeatures(
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
			state.applyIn16Int[:in16Per],
		)
		if err := state.osceBWERuntime[1].ProcessDelayed(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			// Left channel was already overwritten with the BWE output; we
			// cannot cleanly roll back to the standard resampler output
			// here. Returning false would leave a mixed left=BWE /
			// right=resampler buffer which is worse than committing fully
			// to the resampler path on failure. Because the right-channel
			// state shares the same model as the left, a failure on the
			// second pass implies a transient runtime issue (e.g. NaN);
			// surface the mid result by copying it to the right channel so
			// the output is at least coherent.
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = out[2*i]
			}
			state.prevBWEActive = true
			return true
		}
		if fadeInIntoBWE {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i+1]
			}
			osceBWECrossFade10ms(state.applyOut48[:in48Per], state.applyFadeout48[:in48Per], 480)
		}
		for i := 0; i < in48Per; i++ {
			out[2*i+1] = state.applyOut48[i]
		}
		state.prevBWEActive = true
		state.prevExtendedMode = bweModeSilkBBWE
		return true
	}

	// Mono SILK packet path (mono decoder or stereo decoder up-mixing a
	// mono packet). Only the slot-0 runtime is used.
	if !d.osceBWE.osceBWERuntime[0].Loaded() {
		return false
	}
	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	if len(native) < in16Per {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}

	// Stage the int16 lowband for the feature extractor and normalise to
	// float32 [-1, 1] for the BBWENet forward pass. libopus performs both
	// conversions internally; keeping them side-by-side avoids re-scanning
	// the input on the hot path.
	//
	// libopus drives osce_bwe (both osce_bwe_calculate_features and the
	// BBWENet forward pass) from &samplesOut1_tmp[n][1]: the 16 kHz lowband
	// shifted one sample earlier than the freshly decoded frame, with index 0
	// carrying the previous frame's final sample (the SILK sStereo.sMid[1]
	// history). LatestNativeMono returns the raw decoded frame at [0], so the
	// previous frame's last sample is prepended and the final decoded sample
	// is carried into the next call -- reproducing libopus' one-sample input
	// delay that otherwise leaves the BWE output misaligned.
	state.applyIn16Int[0] = state.monoPrevNativeLast
	state.applyIn16[0] = float32(state.monoPrevNativeLast) / 32768.0
	for i := 1; i < in16Per; i++ {
		state.applyIn16Int[i] = native[i-1]
		state.applyIn16[i] = float32(native[i-1]) / 32768.0
	}
	state.monoPrevNativeLast = native[in16Per-1]
	numFrames := in16Per / 160

	// Port of libopus `osce_bwe_calculate_features`: produces a 114-float
	// feature vector per 10 ms hop (log-mag spectrogram + instantaneous-
	// frequency cross-power) consumed by the BBWENet feature net.
	state.osceBWEFeatures[0].CalculateFeatures(
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		state.applyIn16Int[:in16Per],
	)

	if err := state.osceBWERuntime[0].ProcessDelayed(
		state.applyIn16[:in16Per],
		state.applyOut48[:in48Per],
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
	); err != nil {
		// Runtime failed -- keep the standard resampler output. Treat as a
		// BWE-inactive frame for transition tracking; if we were previously
		// active a fade-out would have been ideal but we have no usable BWE
		// data to cross-fade with, so just mark prev as inactive.
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}

	// If the previous frame did NOT run BWE we are transitioning into BWE.
	// libopus cross-fades the BWE output (fadein) against the standard
	// silk_resampler output (fadeout). The standard output is already in
	// `out` (channels==1 here -- stereo is bypassed above). Mix the BWE
	// buffer in via osceBWECrossFade10ms which writes the cross-fade
	// samples directly back into the BWE output buffer; we then overwrite
	// `out` from there.
	if fadeInIntoBWE {
		if d.channels == 1 {
			osceBWECrossFade10ms(state.applyOut48[:in48Per], out[:in48Per], 480)
		} else {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i]
			}
			osceBWECrossFade10ms(state.applyOut48[:in48Per], state.applyFadeout48[:in48Per], 480)
		}
	}

	// Write BWE output to the mono channel of `out`. For mono channels==1 we
	// overwrite directly; mono packet on a stereo decoder duplicates the
	// BWE result on both channels.
	if d.channels == 1 {
		copy(out[:in48Per], state.applyOut48[:in48Per])
	} else {
		for i := 0; i < in48Per; i++ {
			v := state.applyOut48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
	state.prevBWEActive = true
	return true
}

// osceBWEMarkInactiveIfModeIneligible clears the BWE-active flag and records the
// libopus osce_extended_mode for packets whose mode/bandwidth does not satisfy
// OSCE_MODE_SILK_BBWE. This catches Hybrid / CELT / SILK-NB-MB packets where
// maybeApplyOSCEBWEPostSilk is not invoked but the transition tracking still
// needs to advance. Recording the precise mode (not just clearing a bool) lets
// the next BWE frame reproduce libopus' fade-in gating: a Hybrid or SILK-only
// predecessor arms the fade-in, while a CELT-only predecessor suppresses it
// (opus_decoder.c forces prev_osce_extended_mode == OSCE_MODE_CELT_ONLY on a
// CELT->SILK transition).
func (d *Decoder) osceBWEMarkInactiveIfModeIneligible(mode Mode, bandwidth Bandwidth, out []float32, frameSize int, packetStereoLocal bool) {
	if d == nil || d.osceBWE == nil {
		return
	}
	if mode == ModeSILK && bandwidth == BandwidthWideband {
		// SILK WB packets go through maybeApplyOSCEBWEPostSilk which manages
		// the flag itself.
		return
	}
	if d.osceBWE.prevBWEActive && len(out) >= frameSize*int(d.channels) {
		d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
	}
	d.osceBWE.prevBWEActive = false
	switch mode {
	case ModeCELT:
		d.osceBWE.prevExtendedMode = bweModeCeltOnly
	case ModeHybrid:
		d.osceBWE.prevExtendedMode = bweModeHybrid
	default:
		// SILK-only NB/MB: libopus osce_extended_mode == OSCE_MODE_SILK_ONLY.
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
	}
}

func (d *Decoder) resetOSCEBWEPostfilterState() {
	if d == nil || d.osceBWE == nil {
		return
	}
	for ch := range d.osceBWE.osceBWERuntime {
		d.osceBWE.osceBWERuntime[ch].Reset()
		d.osceBWE.osceBWEFeatures[ch].Reset()
	}
	d.osceBWE.prevBWEActive = false
	d.osceBWE.prevExtendedMode = bweModeNone
	d.osceBWE.monoPrevNativeLast = 0
}

// applyOSCEBWEFadeOut runs a fade-out cross-fade when leaving BWE: BWE on the
// previous lowband -> standard upsampled `out`. Mirrors the second branch of
// the libopus dec_API.c BWE handler where, after a BWE-active frame, the new
// frame's standard silk_resampler output is cross-faded against a fresh BWE
// pass on the same native lowband. We approximate that by running BWE on the
// current native lowband (if available) and fading the existing `out` against
// it. When BWE cannot run (e.g. native unavailable), the helper is a no-op.
func (d *Decoder) applyOSCEBWEFadeOut(out []float32, frameSize int, packetStereoLocal bool) {
	if d == nil || d.osceBWE == nil || !d.osceBWE.osceBWERuntime[0].Loaded() {
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

	state := d.osceBWE
	numFrames := in16Per / 160
	// stage applies the libopus &samplesOut1_tmp[n][1] one-sample input delay
	// used by osce_bwe for mono. The stereo accessor already returns the
	// [1]-aligned midFrame/sideFrame slices, so stereo passes stage=false.
	runChannel := func(native []int16, channelIdx int, stage bool) bool {
		if channelIdx < 0 || channelIdx > 1 || !state.osceBWERuntime[channelIdx].Loaded() {
			return false
		}
		if len(native) < in16Per {
			return false
		}
		if stage {
			state.applyIn16Int[0] = state.monoPrevNativeLast
			state.applyIn16[0] = float32(state.monoPrevNativeLast) / 32768.0
			for i := 1; i < in16Per; i++ {
				state.applyIn16Int[i] = native[i-1]
				state.applyIn16[i] = float32(native[i-1]) / 32768.0
			}
			state.monoPrevNativeLast = native[in16Per-1]
		} else {
			for i := 0; i < in16Per; i++ {
				state.applyIn16Int[i] = native[i]
				state.applyIn16[i] = float32(native[i]) / 32768.0
			}
		}
		// Port of libopus `osce_bwe_calculate_features` -- compute the same
		// 114-float feature vector per 10 ms hop that the BWE-active path
		// uses, so the fade-out BWE pass produces output matching the per-
		// channel BWE state libopus cross-fades against.
		state.osceBWEFeatures[channelIdx].CalculateFeatures(
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
			state.applyIn16Int[:in16Per],
		)
		return state.osceBWERuntime[channelIdx].ProcessDelayed(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		) == nil
	}

	if packetStereoLocal && d.channels == 2 {
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDecoder.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return
		}
		if runChannel(leftNative, 0, false) {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i]
			}
			osceBWECrossFade10ms(state.applyFadeout48[:in48Per], state.applyOut48[:in48Per], 480)
			for i := 0; i < in48Per; i++ {
				out[2*i] = state.applyFadeout48[i]
			}
		}
		if runChannel(rightNative, 1, false) {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i+1]
			}
			osceBWECrossFade10ms(state.applyFadeout48[:in48Per], state.applyOut48[:in48Per], 480)
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = state.applyFadeout48[i]
			}
		}
		return
	}

	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 || !runChannel(native, 0, true) {
		return
	}
	if d.channels == 1 {
		// `out` is the standard upsampled output (fadein), `applyOut48` is
		// the BWE output (fadeout). osceBWECrossFade10ms writes the cross-
		// fade into its first argument.
		osceBWECrossFade10ms(out[:in48Per], state.applyOut48[:in48Per], 480)
		return
	}
	if d.channels == 2 {
		for i := 0; i < in48Per; i++ {
			state.applyFadeout48[i] = out[2*i]
		}
		osceBWECrossFade10ms(state.applyFadeout48[:in48Per], state.applyOut48[:in48Per], 480)
		for i := 0; i < in48Per; i++ {
			v := state.applyFadeout48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
}
