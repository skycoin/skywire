//go:build gopus_fixed_point

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// celtDecodeFixedAPIRate runs the FIXED_POINT integer CELT decoder
// (internal/fixedpoint.CELTDecoder) for a CELT-only frame and accumulates its
// libopus-exact int16 (Res2Int16) and int24 (opus_res; RES2INT24(a)==a) output
// for the in-flight DecodeInt16 / DecodeInt24 wrapper. It runs IN ADDITION to
// the float CELT decoder (which has already filled out and advanced the float
// cross-frame state), purely to capture integer-exact PCM.
//
// It only does work when an integer-output packet is active (beginFixedPacket
// armed the accumulation). The float Decode path never arms one, so this is a
// no-op there. A degenerate (len(data) <= 1, DTX/PLC) frame is declined so the
// float conversion is used for that packet.
//
// It returns handled=true when it accumulated a frame.
func (d *Decoder) celtDecodeFixedAPIRate(data []byte, apiFrameSize int, packetStereo bool, celtBW celt.CELTBandwidth, out []float32) (bool, error) {
	if !d.fixedPacketActive {
		return false, nil
	}
	if len(data) <= 1 {
		return false, nil
	}
	channels := int(d.channels)
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(channels, int(d.sampleRate))
	}

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := apiFrameSize * downsample

	// CELT-only frames cover bands [0, EffectiveBands(bw)).
	d.fixedCELT.SetBandRange(0, celtBW.EffectiveBands())

	needed := apiFrameSize * channels

	int16Out := d.fixedCELTScratch(needed)
	d.fixedCELT.DecodeWithEC(data, coreFrameSize, int16Out)
	res := d.fixedCELT.LastRes()

	// Stash the integer-exact output for the int16/int24 wrappers.
	d.appendFixedOutput(int16Out[:needed], res[:needed])
	return true, nil
}

// celtDecodeLostFixedAPIRate runs the integer FIXED_POINT celt_decode_lost for a
// CELT-only packet-loss frame so the DecodeInt16 / DecodeInt24 wrappers can read
// libopus-exact concealment output, matching opus_decode(NULL,...). apiFrameSize
// is the per-channel output sample count of this concealed chunk (already clamped
// to one 20 ms core block by the caller).
//
// The integer decoder's cross-frame state (decode_mem, energy histories,
// post-filter, loss_duration) must have been advanced by the prior received CELT
// frames -- exactly the same `celt_dec` libopus reuses across the loss. When the
// integer decoder was never primed (no preceding integer CELT frame) it declines
// so the caller falls back to the float conversion. It returns true when it
// produced and stashed a concealed frame.
func (d *Decoder) celtDecodeLostFixedAPIRate(apiFrameSize int) bool {
	if !d.fixedPacketActive || d.fixedCELT == nil {
		return false
	}
	// The integer celt_decode_lost concealment runs the synthesis and deemphasis
	// at the 48 kHz core rate only; sub-48k output decimation is not reproduced
	// on the loss path, so decline there and let the float conversion conceal.
	if int(d.sampleRate) != 48000 {
		return false
	}
	channels := int(d.channels)

	// celt_decode_lost retains the band range (st->start / st->end) set by the
	// last received frame, so it is left untouched here.
	needed := apiFrameSize * channels

	int16Out := d.fixedCELTScratch(needed)
	d.fixedCELT.DecodeLost(apiFrameSize, int16Out)
	res := d.fixedCELT.LastRes()

	d.appendFixedOutput(int16Out[:needed], res[:needed])
	return true
}

// fixedHybridLostApplicable reports whether the integer FIXED_POINT hybrid PLC
// path can conceal the in-flight lost hybrid frame bit-exact. It mirrors the
// prepareFixedHybrid gating: an active integer packet, the integer CELT decoder
// already primed by a prior received hybrid frame (so its cross-frame state is
// the same celt_dec libopus reuses across the loss), and the 48 kHz API rate
// (the integer concealment synthesis/deemphasis runs at the 48 kHz core only;
// sub-48k output decimation on the loss path is not reproduced). When false the
// caller marks the packet unhandled and the float PLC conversion is used.
func (d *Decoder) fixedHybridLostApplicable() bool {
	return d.fixedPacketActive && d.fixedCELT != nil && int(d.sampleRate) == 48000
}

// armFixedHybridLost arms the SILK PLC int16-lowband capture for a lost hybrid
// frame so the integer CELT highband concealment can later accumulate onto the
// exact opus_res SILK lowband. frameSizeAPI is the per-channel output sample
// count. It returns true when capture was armed (the caller must pair it with
// finishFixedHybridLost after the float PLC decode); false declines the integer
// path. silkStereo reports whether the SILK PLC runs in stereo (capture is
// interleaved L/R) or mono (capture is one channel, duplicated to stereo output
// by finishFixedHybridLost).
func (d *Decoder) armFixedHybridLost(frameSizeAPI int, silkStereo bool) bool {
	if !d.fixedHybridLostApplicable() {
		return false
	}
	channels := int(d.channels)
	capLen := frameSizeAPI
	if silkStereo {
		capLen = frameSizeAPI * 2
	}
	if cap(d.fixedHybridPLCSilk) < capLen {
		d.fixedHybridPLCSilk = make([]int16, capLen)
	}
	d.fixedHybridPLCSilk = d.fixedHybridPLCSilk[:capLen]
	d.silkDecoder.ArmPLCLowbandCapture(d.fixedHybridPLCSilk)
	d.fixedHybridPLCStereo = silkStereo
	d.fixedHybridPLCChannels = channels
	return true
}

// finishFixedHybridLost completes the integer FIXED_POINT hybrid PLC after the
// float PLC decode has run the SILK PLC (filling the armed int16 lowband
// capture) and the float CELT PLC. It builds the interleaved opus_res SILK
// lowband (INT16TORES: int16 << RES_SHIFT) for the output channel layout, then
// runs the integer celt_decode_lost (start band 17, accum=1) which advances the
// integer CELT cross-frame state through the loss and accumulates the concealed
// highband onto the lowband, mirroring opus_decode_frame's
// celt_decode_with_ec_dred(NULL, celt_accum=1) on a lost hybrid frame. The
// combined opus_res / int16 output is stashed for the DecodeInt16 / DecodeInt24
// wrappers. It returns true when the integer concealment produced a frame.
func (d *Decoder) finishFixedHybridLost(frameSizeAPI int) bool {
	filled := d.silkDecoder.PLCLowbandCaptured()
	d.silkDecoder.ArmPLCLowbandCapture(nil)
	if d.fixedCELT == nil {
		return false
	}
	channels := d.fixedHybridPLCChannels
	needed := frameSizeAPI * channels

	if cap(d.fixedHybridPLCRes) < needed {
		d.fixedHybridPLCRes = make([]int32, needed)
	}
	res := d.fixedHybridPLCRes[:needed]
	src := d.fixedHybridPLCSilk

	// Build the interleaved opus_res lowband (INT16TORES(a) = a << RES_SHIFT,
	// RES_SHIFT == 8) from the captured int16 SILK PLC lowband, applying the same
	// mono->stereo duplication the float hybrid PLC uses (mono SILK, stereo out).
	switch {
	case channels == 2 && d.fixedHybridPLCStereo:
		for i := 0; i < needed; i++ {
			var s int16
			if i < filled && i < len(src) {
				s = src[i]
			}
			res[i] = int32(s) << 8
		}
	case channels == 2:
		// Mono SILK lowband duplicated to both output channels.
		for i := 0; i < frameSizeAPI; i++ {
			var s int16
			if i < filled && i < len(src) {
				s = src[i]
			}
			v := int32(s) << 8
			res[2*i] = v
			res[2*i+1] = v
		}
	default:
		for i := 0; i < needed; i++ {
			var s int16
			if i < filled && i < len(src) {
				s = src[i]
			}
			res[i] = int32(s) << 8
		}
	}

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSizeAPI * downsample

	// opus_decode_frame sets the CELT start band to 17 for hybrid (start_band=17)
	// and keeps the previous frame's end band (CELT_SET_END_BAND is skipped on the
	// PLC bandwidth==0 path), exactly as celt_decode_lost reads st->start / st->end.
	d.fixedCELT.SetStartBand(celt.HybridCELTStartBand)
	d.fixedCELT.DecodeLostAccum(coreFrameSize, res)

	if cap(d.fixedHybridPLCInt16) < needed {
		d.fixedHybridPLCInt16 = make([]int16, needed)
	}
	int16Out := d.fixedHybridPLCInt16[:needed]
	for i := 0; i < needed; i++ {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}
	d.appendFixedOutput(int16Out, res)
	return true
}

// prepareFixedHybrid arms the integer hybrid highband hook for the in-flight
// frame and records the CELT end band and reset policy, mirroring the libopus
// opus_decode_frame hybrid CELT path. It is called from the Hybrid dispatch
// before the float hybrid decode runs (which drives the SILK lowband and then
// invokes the hook). It returns false when the integer path is declined (no
// active integer packet, or a degenerate/PLC frame), in which case the caller
// marks the packet unhandled and uses the float conversion.
func (d *Decoder) prepareFixedHybrid(data []byte, celtBW celt.CELTBandwidth, needCeltReset bool) bool {
	if !d.fixedPacketActive || len(data) <= 1 {
		return false
	}
	// Hybrid SILK is always wideband (16 kHz internal). At an API rate below
	// 16 kHz the SILK lowband is produced by the float downsampling resampler,
	// which has no integer int16 output for INT16TORES, so the integer hybrid
	// highband cannot reproduce the FIXED_POINT lowband. Decline so the float
	// conversion handles the packet -- it is bit-exact with the FIXED_POINT
	// opus_decode reference for these rates.
	if int(d.sampleRate) < 16000 {
		return false
	}
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	d.fixedHybridEnd = celtBW.EffectiveBands()
	d.fixedHybridReset = needCeltReset
	d.fixedHybridErr = nil
	d.fixedRedundantValid = false
	d.fixedTransitionValid = false
	d.fixedHybridFrameActive = true
	d.hybridDecoder.SetFixedHighband(d)
	return true
}

// fixedHybridArmed reports whether the in-flight frame is being decoded by the
// integer hybrid path (set by prepareFixedHybrid). It stays true across the
// redundancy / transition post-processing, after the highband hook is disarmed.
func (d *Decoder) fixedHybridArmed() bool {
	return d.fixedPacketActive && d.fixedHybridFrameActive
}

// fixedDecodeRedundantCELT decodes the integer (opus_res) CELT redundancy frame
// for a Hybrid packet, mirroring the libopus opus_decode_frame
// celt_decode_with_ec(celt_dec, data+len, redundancy_bytes, redundant_audio, F5,
// NULL, 0) calls. reset mirrors the OPUS_RESET_STATE applied before the
// SILK->CELT redundancy decode; start band is always 0. The decoded F5*channels
// opus_res output is captured in d.fixedRedundantRes. It runs on the same integer
// CELT decoder as the main hybrid highband, in the same order as the reference,
// so the shared decode_mem / energy state stays bit-identical.
func (d *Decoder) fixedDecodeRedundantCELT(redundantData []byte, celtBW celt.CELTBandwidth, reset bool) {
	if !d.fixedHybridArmed() || d.fixedCELT == nil {
		return
	}
	channels := int(d.channels)
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	f5API := int(d.sampleRate) / 200
	needed := f5API * channels
	coreFrameSize := f5API * downsample

	if reset {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetBandRange(0, celtBW.EffectiveBands())

	if cap(d.fixedRedundantScratch) < needed {
		d.fixedRedundantScratch = make([]int16, needed)
	}
	scratch := d.fixedRedundantScratch[:needed]
	d.fixedCELT.DecodeWithEC(redundantData, coreFrameSize, scratch)
	res := d.fixedCELT.LastRes()

	if cap(d.fixedRedundantRes) < needed {
		d.fixedRedundantRes = make([]int32, needed)
	}
	d.fixedRedundantRes = d.fixedRedundantRes[:needed]
	copy(d.fixedRedundantRes, res[:needed])
	d.fixedRedundantValid = true
}

// fixedDecodeTransitionPLC decodes the integer (opus_res) 5 ms CELT PLC transition
// frame for a Hybrid frame whose previous frame was CELT-only, mirroring the
// libopus opus_decode_frame(NULL) transition decode with mode == MODE_CELT_ONLY.
// It runs on the integer CELT decoder (start band 0) before the main hybrid
// accum, whose OPUS_RESET_STATE then discards the PLC decode_mem — matching the
// reference. The transSizeAPI*channels opus_res output is captured in
// d.fixedTransitionRes.
func (d *Decoder) fixedDecodeTransitionPLC(transSizeAPI int) {
	if !d.fixedHybridArmed() || d.fixedCELT == nil {
		return
	}
	channels := int(d.channels)
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	needed := transSizeAPI * channels
	coreFrameSize := transSizeAPI * downsample

	// opus_decode_frame skips CELT_SET_END_BAND for the PLC (bandwidth==0) path,
	// so the transition PLC keeps the previous CELT frame's end band; only the
	// start band is forced to 0.
	d.fixedCELT.SetStartBand(0)
	if cap(d.fixedRedundantScratch) < needed {
		d.fixedRedundantScratch = make([]int16, needed)
	}
	scratch := d.fixedRedundantScratch[:needed]
	// data == nil selects the PLC path inside DecodeWithEC.
	d.fixedCELT.DecodeWithEC(nil, coreFrameSize, scratch)
	res := d.fixedCELT.LastRes()

	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	copy(d.fixedTransitionRes, res[:needed])
	d.fixedTransitionValid = true
}

// fixedSnapshotHandled returns the current fixedAllHandled flag, and
// fixedRestoreHandled restores it. They bracket the recursive float PLC decode of
// a Hybrid transition frame so its markFixedUnhandled (the data==nil CELT PLC
// path declines the integer accumulation) does not clobber the integer status of
// the main hybrid frame, which the integer transition crossfade still recovers
// bit-exact.
func (d *Decoder) fixedSnapshotHandled() bool { return d.fixedAllHandled }

func (d *Decoder) fixedRestoreHandled(v bool) {
	if d.fixedHybridArmed() {
		d.fixedAllHandled = v
	}
}

// fixedSuppressCELTPLC sets fixedSuppressCELTPLCHook to v and returns the prior
// value, so callers can bracket the recursive Hybrid-transition float PLC decode
// without letting the data==nil CELT branch run celtDecodeLostFixedAPIRate a
// second time over the integer CELT decoder.
func (d *Decoder) fixedSuppressCELTPLC(v bool) bool {
	prev := d.fixedSuppressCELTPLCHook
	d.fixedSuppressCELTPLCHook = v
	return prev
}

// fixedCELTPLCHookSuppressed reports whether the integer celt_decode_lost hook is
// currently suppressed for a recursive transition scratch decode.
func (d *Decoder) fixedCELTPLCHookSuppressed() bool { return d.fixedSuppressCELTPLCHook }

// fixedLastFrameRes returns the most recently appended frame's opus_res and int16
// slices within the running packet accumulation (length needed interleaved
// samples), or nil if the integer accumulation is not active / too short.
func (d *Decoder) fixedLastFrameRes(needed int) ([]int32, []int16) {
	if !d.fixedPacketActive || d.fixedCursor < needed {
		return nil, nil
	}
	start := d.fixedCursor - needed
	if len(d.fixedRes) < d.fixedCursor || len(d.fixedInt16) < d.fixedCursor {
		return nil, nil
	}
	return d.fixedRes[start:d.fixedCursor], d.fixedInt16[start:d.fixedCursor]
}

// fixedRefreshInt16 re-derives the int16 view (Res2Int16) of the opus_res slice
// after an in-place integer post-processing step (redundancy copy / smooth_fade /
// transition crossfade) rewrote res.
func fixedRefreshInt16(res []int32, int16Out []int16) {
	for i := range res {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}
}

// fixedApplyRedundancySilkToCelt applies the integer SILK->CELT redundancy
// crossfade onto the in-flight Hybrid frame, mirroring opus_decoder.c:644-645:
// smooth_fade(pcm+C*(frame_size-F2_5), redundant_audio+C*F2_5,
// pcm+C*(frame_size-F2_5), F2_5, C, window, Fs).
func (d *Decoder) fixedApplyRedundancySilkToCelt(frameSize, fs int) {
	channels := int(d.channels)
	f2_5 := fs / 400
	f5 := fs / 200
	needed := frameSize * channels
	res, int16Out := d.fixedLastFrameRes(needed)
	if res == nil || !d.fixedRedundantValid || len(d.fixedRedundantRes) < f5*channels {
		d.markFixedUnhandled()
		return
	}
	start := (frameSize - f2_5) * channels
	if start < 0 || start > len(res) {
		d.markFixedUnhandled()
		return
	}
	fixedpoint.SmoothFadeRes(res[start:], d.fixedRedundantRes[f2_5*channels:], res[start:], f2_5, channels, fs)
	fixedRefreshInt16(res, int16Out)
	d.fixedRedundancyApplied++
}

// fixedApplyRedundancyCeltToSilk applies the integer CELT->SILK redundancy
// crossfade onto the in-flight Hybrid frame, mirroring opus_decoder.c:650-658:
// the first F2_5 samples are overwritten by redundant_audio, then
// smooth_fade(redundant_audio+C*F2_5, pcm+C*F2_5, pcm+C*F2_5, F2_5, C, window, Fs).
func (d *Decoder) fixedApplyRedundancyCeltToSilk(frameSize, fs int) {
	channels := int(d.channels)
	f2_5 := fs / 400
	f5 := fs / 200
	needed := frameSize * channels
	res, int16Out := d.fixedLastFrameRes(needed)
	if res == nil || !d.fixedRedundantValid || len(d.fixedRedundantRes) < f5*channels {
		d.markFixedUnhandled()
		return
	}
	for c := 0; c < channels; c++ {
		for i := 0; i < f2_5; i++ {
			res[channels*i+c] = d.fixedRedundantRes[channels*i+c]
		}
	}
	fixedpoint.SmoothFadeRes(d.fixedRedundantRes[f2_5*channels:], res[f2_5*channels:], res[f2_5*channels:], f2_5, channels, fs)
	fixedRefreshInt16(res, int16Out)
	d.fixedRedundancyApplied++
}

// fixedApplyTransition applies the integer CELT->SILK transition crossfade onto
// the in-flight Hybrid frame, mirroring opus_decoder.c:660-678 with
// pcm_transition being the integer 5 ms CELT PLC frame.
func (d *Decoder) fixedApplyTransition(frameSize, audiosize, fs int) {
	channels := int(d.channels)
	f2_5 := fs / 400
	f5 := fs / 200
	needed := frameSize * channels
	res, int16Out := d.fixedLastFrameRes(needed)
	if res == nil || !d.fixedTransitionValid || len(d.fixedTransitionRes) < f2_5*channels {
		d.markFixedUnhandled()
		return
	}
	trans := d.fixedTransitionRes
	if audiosize >= f5 {
		if len(trans) < f2_5*channels || len(res) < 2*f2_5*channels {
			d.markFixedUnhandled()
			return
		}
		copy(res[:f2_5*channels], trans[:f2_5*channels])
		fixedpoint.SmoothFadeRes(trans[f2_5*channels:], res[f2_5*channels:], res[f2_5*channels:], f2_5, channels, fs)
	} else {
		fixedpoint.SmoothFadeRes(trans, res, res, f2_5, channels, fs)
	}
	fixedRefreshInt16(res, int16Out)
	d.fixedTransitionApplied++
}

// finishFixedHybrid disarms the hook and reports whether the integer hybrid
// highband decode produced a frame the int16/int24 wrappers can read directly.
func (d *Decoder) finishFixedHybrid() error {
	d.hybridDecoder.SetFixedHighband(nil)
	return d.fixedHybridErr
}

// DecodeHybridHighband implements hybrid.FixedHybridHighband. It builds the
// opus_res SILK lowband (INT16TORES: int16 << RES_SHIFT) from the resampled int16
// SILK output, then accumulates the integer CELT highband (start band 17) onto it
// from the cloned shared range decoder, matching libopus celt_decode_with_ec_dred
// with celt_accum=1. The combined opus_res / int16 output is stashed for the
// DecodeInt16 / DecodeInt24 wrappers.
func (d *Decoder) DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, frameSizeAPI, frameSize48 int, packetStereo bool) {
	channels := int(d.channels)
	needed := frameSizeAPI * channels

	if cap(d.fixedHybridRes) < needed {
		d.fixedHybridRes = make([]int32, needed)
	}
	res := d.fixedHybridRes[:needed]
	// INT16TORES(a) = SHL32(EXTEND32(a), RES_SHIFT); RES_SHIFT == 8.
	for i := 0; i < needed; i++ {
		var s int16
		if i < filled && i < len(silkInt16) {
			s = silkInt16[i]
		}
		res[i] = int32(s) << 8
	}

	if d.fixedHybridReset {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSizeAPI * downsample

	d.fixedCELT.DecodeHybridAccum(rd, coreFrameSize, res)

	if cap(d.fixedHybridInt16) < needed {
		d.fixedHybridInt16 = make([]int16, needed)
	}
	int16Out := d.fixedHybridInt16[:needed]
	for i := 0; i < needed; i++ {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}

	d.appendFixedOutput(int16Out, res)
}

// beginFixedPacket arms the per-packet integer-output accumulation used by the
// DecodeInt16 / DecodeInt24 wrappers. It must be paired with a read of
// fixedAllHandled / fixedInt16 / fixedRes after the float decode completes.
func (d *Decoder) beginFixedPacket() {
	d.fixedPacketActive = true
	d.fixedAllHandled = true
	d.fixedCursor = 0
	d.fixedInt16 = d.fixedInt16[:0]
	d.fixedRes = d.fixedRes[:0]
	d.fixedHybridFrameActive = false
}

// fixedApplyDecodeGain applies the FIXED_POINT decode-gain stage to the integer
// accumulation of the just-decoded packet, mirroring opus_decode_frame
// (st->decode_gain != 0): x = MULT32_32_Q16(pcm[i], gain); pcm[i] =
// SATURATE(x, 32767), with gain = celt_exp2(MULT16_16_P15(QCONST16(6.48814081e-4f,
// 25), st->decode_gain)). It runs on the accumulated opus_res buffer (the
// FIXED_POINT pcm is opus_res) and re-derives the int16 view, so the int16/int24
// wrappers read the gained, integer-exact output. It is a no-op when no integer
// frame was accumulated or the gain is zero.
//
// libopus applies the gain per opus_decode_frame on frame_size*channels samples;
// the gain is a constant and SATURATE is per-sample, so applying it once over the
// whole packet's accumulation is identical.
func (d *Decoder) fixedApplyDecodeGain(needed int) {
	if d.decodeGainQ8 == 0 {
		return
	}
	if !d.fixedInt16Ready(needed) || len(d.fixedRes) < needed {
		return
	}
	gain := fixedpoint.DecodeGainQ16(d.decodeGainQ8)
	fixedpoint.ApplyDecodeGainRes(d.fixedRes[:needed], gain)
	for i := 0; i < needed; i++ {
		d.fixedInt16[i] = fixedpoint.Res2Int16(d.fixedRes[i])
	}
}

// fixedClearHybridFrame clears the per-frame integer hybrid flags after a frame's
// redundancy / transition post-processing completes, so a following non-hybrid
// frame in the same packet does not inherit them.
func (d *Decoder) fixedClearHybridFrame() {
	d.fixedHybridFrameActive = false
	d.fixedRedundantValid = false
	d.fixedTransitionValid = false
}

// endFixedPacket disarms the accumulation. fixedAllHandled stays valid for the
// caller to inspect immediately after.
func (d *Decoder) endFixedPacket() {
	d.fixedPacketActive = false
}

// appendFixedOutput records one frame's int16 and opus_res output at the
// running packet cursor.
func (d *Decoder) appendFixedOutput(int16Out []int16, res []int32) {
	d.fixedInt16 = append(d.fixedInt16, int16Out...)
	d.fixedRes = append(d.fixedRes, res...)
	d.fixedCursor += len(int16Out)
}

// markFixedUnhandled records that the in-flight packet contains a frame the
// integer CELT path did not produce (a SILK/Hybrid frame, a float CELT
// fallback, or a concealment frame), so the int16/int24 wrappers must use the
// float conversion for this packet.
func (d *Decoder) markFixedUnhandled() {
	d.fixedAllHandled = false
}

// fixedInt16Ready reports whether the just-decoded packet was produced entirely
// by the integer CELT path and the accumulated int16 output covers exactly the
// expected interleaved length.
func (d *Decoder) fixedInt16Ready(needed int) bool {
	return d.fixedPacketActive && d.fixedAllHandled && d.fixedCursor == needed && len(d.fixedInt16) == needed
}

// finishInt16Output writes n*channels int16 PCM samples into pcm. When the
// just-decoded packet was produced entirely by the integer CELT path, the
// libopus-exact int16 output (Res2Int16 of each opus_res sample) is copied
// directly, avoiding the lossy float32->int16 conversion. Otherwise it falls
// back to the shared float path (soft clip + float32ToInt16), exactly as the
// default build does. It returns true when the integer path was used.
func (d *Decoder) finishInt16Output(pcm []int16, scratch []float32, n, channels int) bool {
	needed := n * channels
	if d.fixedInt16Ready(needed) {
		copy(pcm[:needed], d.fixedInt16[:needed])
		return true
	}
	softClipAndFloat32ToInt16(pcm, scratch, n, channels, d.softClipMem[:])
	return false
}

// finishInt24Output writes n*channels int24 PCM samples (right-justified in
// int32) into pcm. When the just-decoded packet was produced entirely by the
// integer CELT path, the opus_res output is copied directly: for the
// FIXED_POINT ENABLE_RES24 build RES2INT24(a) == a, so the opus_res value is
// the int24 sample. Otherwise it falls back to the shared float path
// (float32ToInt24), exactly as the default build does. Returns true when the
// integer path was used.
func (d *Decoder) finishInt24Output(pcm []int32, scratch []float32, n, channels int) bool {
	needed := n * channels
	if d.fixedInt16Ready(needed) && len(d.fixedRes) >= needed {
		copy(pcm[:needed], d.fixedRes[:needed])
		return true
	}
	float32ToInt24Slice(pcm, scratch, n, channels)
	return false
}

// fixedInt16PLCOutput copies the integer FIXED_POINT concealment output (Res2Int16
// of each opus_res sample) into pcm when the just-concealed PLC frame was produced
// entirely by the integer CELT path. It returns false (leaving pcm untouched) when
// the integer path did not handle the loss, so the caller applies its own float
// fallback (float32ToInt16NoSoftClip, no soft clip on concealed audio).
func (d *Decoder) fixedInt16PLCOutput(pcm []int16, n, channels int) bool {
	needed := n * channels
	if !d.fixedInt16Ready(needed) {
		return false
	}
	copy(pcm[:needed], d.fixedInt16[:needed])
	return true
}

// fixedInt24PLCOutput copies the integer FIXED_POINT concealment opus_res output
// (RES2INT24(a)==a) into pcm when the PLC frame was produced entirely by the
// integer CELT path; otherwise it returns false for the float fallback.
func (d *Decoder) fixedInt24PLCOutput(pcm []int32, n, channels int) bool {
	needed := n * channels
	if !d.fixedInt16Ready(needed) || len(d.fixedRes) < needed {
		return false
	}
	copy(pcm[:needed], d.fixedRes[:needed])
	return true
}

// resetFixedCELT clears the integer CELT decoder cross-frame state on a CELT
// mode change, mirroring the float path's d.celtDecoder.Reset().
func (d *Decoder) resetFixedCELT() {
	if d.fixedCELT != nil {
		d.fixedCELT.Reset()
	}
}

func (d *Decoder) fixedCELTScratch(n int) []int16 {
	if cap(d.fixedCELTPCM) < n {
		d.fixedCELTPCM = make([]int16, n)
		return d.fixedCELTPCM
	}
	d.fixedCELTPCM = d.fixedCELTPCM[:n]
	return d.fixedCELTPCM
}
