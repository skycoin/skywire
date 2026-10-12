//go:build gopus_fixed_point

package multistream

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeToResFixed decodes a packet to interleaved opus_res samples in
// gopus_fixed_point builds. The returned slice aliases decoder scratch and is
// overwritten by the next successful DecodeToResFixed or DecodePLCToResFixed
// call. handled is false when this decoder or packet needs another decode path.
func (d *Decoder) DecodeToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	if len(data) == 0 {
		return d.DecodePLCToResFixed(frameSize)
	}
	if frameSize <= 0 {
		return nil, false, nil
	}
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return nil, false, nil
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		return nil, false, nil
	}

	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return nil, false, err
	}
	d.packetsScratch = packets
	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return nil, false, err
	}
	if duration > frameSize {
		return nil, false, ErrBufferTooSmall
	}
	decodeFrameSize := duration

	// Classify every stream up front without decoding so a packet containing a
	// frame the integer path does not cover is declined before any decode runs. This avoids double-decoding (which would
	// corrupt the shared float cross-frame state) when the caller falls back to
	// the float conversion.
	for i := 0; i < d.streams; i++ {
		st, ok := d.decoders[i].(*streamState)
		if !ok {
			return nil, false, nil
		}
		if !fixedHandleableStreamPacket(packets[i], &st.packetParser) {
			return nil, false, nil
		}
	}

	// Every stream passed the pre-check, so each decode advances the shared
	// float state exactly once and yields bit-exact opus_res. A post-check
	// decline would mean state was advanced but the result is unusable, so it is
	// surfaced as an error rather than silently re-decoded by the caller.
	if cap(d.fixedStreamRes) < d.streams {
		d.fixedStreamRes = make([][]int32, d.streams)
	}
	streamRes := d.fixedStreamRes[:d.streams]
	for i := 0; i < d.streams; i++ {
		st := d.decoders[i].(*streamState)
		res, handled, derr := st.decodePacketToResFixed(packets[i], decodeFrameSize)
		if derr != nil {
			return nil, false, derr
		}
		if !handled {
			return nil, false, ErrInvalidPacket
		}
		streamRes[i] = res
	}

	// Reset the float PLC bookkeeping the same way the float decode does so a
	// following concealment frame behaves identically.
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		for i := 0; i < d.streams; i++ {
			d.markDREDUpdated(i)
		}
	}

	d.fixedOutput = applyChannelMappingResInto(d.fixedOutput, streamRes, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return d.fixedOutput, true, nil
}

// DecodePLCToResFixed conceals a loss request in the fixed-point domain and
// returns interleaved opus_res samples. frameSize must be a positive multiple
// of 2.5 ms and at most 120 ms; otherwise handled is false. The returned slice
// aliases decoder scratch and is overwritten by the next successful call to
// DecodeToResFixed or DecodePLCToResFixed.
func (d *Decoder) DecodePLCToResFixed(frameSize int) ([]int32, bool, error) {
	f2_5 := int(d.sampleRate) / 400
	if frameSize <= 0 || f2_5 <= 0 || frameSize%f2_5 != 0 || frameSize > int(d.sampleRate)*3/25 {
		return nil, false, nil
	}
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return nil, false, nil
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		return nil, false, nil
	}
	for _, decoder := range d.decoders {
		st, ok := decoder.(*streamState)
		if !ok || !st.haveDecoded || !st.canDecodeLostFixed() {
			return nil, false, nil
		}
	}

	needed := frameSize * d.outputChannels
	if cap(d.fixedOutput) < needed {
		d.fixedOutput = make([]int32, needed)
	} else {
		d.fixedOutput = d.fixedOutput[:needed]
		clear(d.fixedOutput)
	}
	if cap(d.fixedStreamRes) < d.streams {
		d.fixedStreamRes = make([][]int32, d.streams)
	}
	streamRes := d.fixedStreamRes[:d.streams]
	maxChunk := int(d.sampleRate) / 50
	if maxChunk <= 0 {
		return nil, false, nil
	}
	d.plcState.RecordLoss()

	for offset := 0; offset < frameSize; {
		chunk := nextCELTPLCChunk(frameSize-offset, maxChunk, maxChunk)
		outOffset := offset * d.outputChannels
		outEnd := outOffset + chunk*d.outputChannels
		for i := 0; i < d.streams; i++ {
			st := d.decoders[i].(*streamState)
			st.beginFixedHybridPLCCapture(chunk)
			floatPCM, err := st.decodePacketToFloat32Unscaled(nil, chunk)
			st.endFixedHybridPLCCapture()
			if err != nil {
				return nil, false, err
			}
			res, err := st.decodeLostFixed(chunk, floatPCM)
			if err != nil {
				return nil, false, err
			}
			if st.decodeGainQ8 != 0 {
				fixedpoint.ApplyDecodeGainRes(res, fixedpoint.DecodeGainQ16(int(st.decodeGainQ8)))
			}
			streamRes[i] = res
		}
		applyChannelMappingResInto(d.fixedOutput[outOffset:outEnd], streamRes, d.mapping, d.coupledStreams, chunk, d.outputChannels)
		offset += chunk
	}
	d.recordCompletedPLCPacket(frameSize)
	return d.fixedOutput, true, nil
}

// beginFixedHybridPLCCapture records the resampled integer SILK lowband while
// the float shadow decoder advances a lost Hybrid frame. The fixed CELT PLC
// path adds its highband to this same opus_res lowband afterward.
func (d *streamState) beginFixedHybridPLCCapture(frameSize int) {
	d.fixedHybridPLCCapturing = d.concealmentMode() == streamModeHybrid
	d.fixedHybridPLCCursor = 0
	if !d.fixedHybridPLCCapturing {
		return
	}
	needed := frameSize * int(d.channels)
	if cap(d.fixedHybridPLCLowband) < needed {
		d.fixedHybridPLCLowband = make([]int16, needed)
	} else {
		d.fixedHybridPLCLowband = d.fixedHybridPLCLowband[:needed]
		clear(d.fixedHybridPLCLowband)
	}
}

func (d *streamState) endFixedHybridPLCCapture() {
	d.fixedHybridPLCCapturing = false
	d.fixedHybridPLCCursor = 0
	if d.hybridDec != nil {
		d.hybridDec.ArmFixedPLCLowbandCapture(nil)
	}
}

// decodeHybridPLCChunkToFloat32 is the common float shadow decode entry point.
// The fixed build arms a capture only while constructing the integer Hybrid
// PLC output; the default build directly delegates to the float decoder.
func (d *streamState) decodeHybridPLCChunkToFloat32(frameSize int, out []float32) error {
	if !d.fixedHybridPLCCapturing || d.hybridDec == nil {
		return d.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, d.lastPacketStereo, out)
	}
	channels := int(d.channels)
	start := d.fixedHybridPLCCursor
	want := min(frameSize*channels, len(d.fixedHybridPLCLowband)-start)
	if want <= 0 {
		return d.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, d.lastPacketStereo, out)
	}
	capture := d.fixedHybridPLCLowband[start : start+want]
	d.hybridDec.ArmFixedPLCLowbandCapture(capture)
	err := d.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, d.lastPacketStereo, out)
	captured := d.hybridDec.FixedPLCLowbandCaptured()
	d.hybridDec.ArmFixedPLCLowbandCapture(nil)
	if !d.lastPacketStereo && channels == 2 {
		monoSamples := min(captured, frameSize)
		for i := monoSamples - 1; i >= 0; i-- {
			value := capture[i]
			capture[2*i] = value
			capture[2*i+1] = value
		}
		captured = monoSamples * 2
	}
	if captured < want {
		clear(capture[captured:])
	}
	d.fixedHybridPLCCursor += want
	return err
}

// decodeHybridTransitionPLCToFloat32 captures the integer Hybrid PLC source
// while the float shadow advances it for a Hybrid-to-CELT transition. The
// fixed CELT decoder must still hold the previous Hybrid history here; the
// target CELT packet resets it only after this capture completes.
func (d *streamState) decodeHybridTransitionPLCToFloat32(frameSize int, out []float32) error {
	if !d.fixedTransitionArmed {
		return d.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, d.lastPacketStereo, out)
	}
	d.beginFixedHybridPLCCapture(frameSize)
	err := d.decodeHybridPLCChunkToFloat32(frameSize, out)
	d.endFixedHybridPLCCapture()
	if err != nil {
		return err
	}
	return d.captureFixedHybridTransitionPLC(frameSize)
}

// captureFixedHybridTransitionPLC composes the captured integer SILK lowband
// with the previous Hybrid CELT decoder's loss output and stores it as the
// inner-gain transition source.
func (d *streamState) captureFixedHybridTransitionPLC(frameSize int) error {
	if !d.fixedTransitionArmed {
		return nil
	}
	if d.concealmentMode() != streamModeHybrid {
		return ErrInvalidPacket
	}
	res, err := d.decodeLostFixed(frameSize, nil)
	if err != nil {
		return err
	}
	needed := frameSize * int(d.channels)
	if len(res) < needed {
		return ErrInvalidPacket
	}
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	} else {
		d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	}
	copy(d.fixedTransitionRes, res[:needed])
	if d.fixedTransitionGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(d.fixedTransitionRes, fixedpoint.DecodeGainQ16(int(d.fixedTransitionGainQ8)))
	}
	d.fixedTransitionHasMain = false
	d.fixedTransitionReady = true
	return nil
}

func fixedCELTCodedChannels(packetStereo bool) int {
	if packetStereo {
		return 2
	}
	return 1
}

// applyChannelMappingRes routes per-stream opus_res samples to output channels,
// mirroring the copy_channel_out routing in opus_multistream_decode_native:
// each stream channel feeds the output channel(s) selected by the mapping, and
// muted channels (mapping value 255) stay zero.
func applyChannelMappingRes(streamRes [][]int32, mapping []byte, coupledStreams, frameSize, outputChannels int) []int32 {
	return applyChannelMappingResInto(nil, streamRes, mapping, coupledStreams, frameSize, outputChannels)
}

func applyChannelMappingResInto(out []int32, streamRes [][]int32, mapping []byte, coupledStreams, frameSize, outputChannels int) []int32 {
	needed := frameSize * outputChannels
	if cap(out) < needed {
		out = make([]int32, needed)
	} else {
		out = out[:needed]
		clear(out)
	}
	for outCh := 0; outCh < outputChannels; outCh++ {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue
		}
		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= len(streamRes) {
			continue
		}
		src := streamRes[streamIdx]
		srcChannels := streamChannels(streamIdx, coupledStreams)
		for s := 0; s < frameSize; s++ {
			srcIdx := s*srcChannels + chanInStream
			if srcIdx < len(src) {
				out[s*outputChannels+outCh] = src[srcIdx]
			}
		}
	}
	return out
}

// fixedHandleableStreamPacket reports whether the integer multistream decode
// can reproduce a stream packet bit-exactly: a received frame that is
// CELT-only (decoded by the integer CELT decoder), SILK-only (integer-exact
// through the lossless float->int16 round-trip), or Hybrid (integer SILK
// opus_res lowband plus integer CELT highband, start band 17, celt_accum).
// CELT code-3 packets decode each frame sequentially, and SILK packets are
// integer-exact through their float32/int16 round-trip. Multi-frame Hybrid
// packets compose integer child outputs. Degenerate (DTX/PLC) frames retain
// the preceding mode's integer concealment state.
func fixedHandleableStreamPacket(data []byte, scratch *packetScratch) bool {
	if len(data) == 0 {
		return false
	}
	toc := parseStreamTOC(data[0])
	if toc.mode != streamModeCELT && toc.mode != streamModeSILK && toc.mode != streamModeHybrid {
		return false
	}
	parsed, err := parseOpusPacketInto(scratch, data, false)
	if err != nil || len(parsed.frames) == 0 {
		return false
	}
	return true
}

// decodePacketToResFixed decodes one elementary-stream packet to interleaved
// opus_res samples (stride = stream channel count). The packet must already be
// classified handleable by fixedHandleableStreamPacket.
//
// It runs the float decode first to advance the float cross-frame state (so a
// following float Decode or PLC frame is unaffected), then captures
// integer-exact opus_res:
//
//   - CELT-only: the fixed-point integer CELT decoder selected by the build.
//   - SILK-only: opus_res = INT16TORES(int16) where the int16 is the lossless
//     float->int16 of the SILK output, matching libopus' FIXED_POINT SILK
//     opus_res (the SILK output is integer-native and round-trips through
//     float32 without loss).
//   - Hybrid: integer SILK lowband and CELT highband are added before gain.
func (d *streamState) decodePacketToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	channels := int(d.channels)

	toc := parseStreamTOC(data[0])
	// opus_decode_native runs each SILK or Hybrid child through
	// opus_decode_frame. Its redundancy and transition fades apply per child,
	// before the next child advances the shared decoder state.
	packetCode := data[0] & 3
	if toc.mode != streamModeCELT && packetCode != 0 &&
		(packetCode != 3 || len(data) < 2 || int(data[1]&0x3f) != 1) {
		return d.decodeMultiframeToResFixed(data, frameSize)
	}
	parsed, err := parseOpusPacketInto(&d.packetParser, data, false)
	if err != nil || len(parsed.frames) == 0 || frameSize%len(parsed.frames) != 0 {
		return nil, false, nil
	}
	for _, frame := range parsed.frames {
		if len(frame) <= 1 {
			if len(parsed.frames) == 1 {
				return d.decodeDegenerateToResFixed(data, frameSize)
			}
			return d.decodeMultiframeToResFixed(data, frameSize)
		}
	}
	prevMode := int(d.lastMode)
	resetFixedCELT := d.haveDecoded && prevMode != toc.mode && !d.prevRedundancy
	deferFixedCELTPrepare := toc.mode == streamModeCELT && prevMode == streamModeHybrid && resetFixedCELT
	if deferFixedCELTPrepare && !d.canCaptureFixedHybridTransition() {
		return nil, false, nil
	}
	switch toc.mode {
	case streamModeCELT:
		if !deferFixedCELTPrepare {
			if err := d.prepareFixedCELTFrame(streamModeCELT, parsed, toc, false); err != nil {
				return nil, false, err
			}
		}
	case streamModeSILK:
		if err := d.prepareFixedSILKRedundancy(toc); err != nil {
			return nil, false, err
		}
	}
	d.beginFixedCELTTransition(toc.mode, d.decodeGainQ8)
	defer d.endFixedCELTTransition()

	// A Hybrid frame must arm the integer highband hook on the stream's hybrid
	// decoder before the float decode runs, so the float hybrid decode also drives
	// the integer CELT highband (start band 17, celt_accum) onto the integer SILK
	// opus_res lowband. The hook stashes the combined opus_res output in
	// fixedHybridRes; the CELT-only / SILK paths capture their integer output after
	// the float decode instead.
	hybridArmed := false
	if toc.mode == streamModeHybrid {
		d.prepareFixedHybridQEXTPayload(parsed)
		var err error
		hybridArmed, err = d.prepareFixedHybridStream(toc)
		if err != nil {
			return nil, false, err
		}
	}

	needed := frameSize * channels
	if cap(d.fixedRes) < needed {
		d.fixedRes = make([]int32, needed)
	}
	res := d.fixedRes[:needed]
	d.fixedSILKCaptureActive = toc.mode == streamModeSILK
	d.fixedSILKCaptureValid = d.fixedSILKCaptureActive
	d.fixedSILKCaptureCursor = 0
	defer func() {
		d.fixedSILKCaptureActive = false
		d.fixedSILKCaptureValid = false
		d.fixedSILKCaptureCursor = 0
	}()
	floatOut, err := d.decodePacketToFloat32Unscaled(data, frameSize)
	if hybridArmed {
		d.finishFixedHybridStream()
	}
	if err != nil {
		return nil, false, err
	}
	if deferFixedCELTPrepare {
		if err := d.prepareFixedCELTFrame(streamModeCELT, parsed, toc, true); err != nil {
			return nil, false, err
		}
	}

	var handled bool
	switch toc.mode {
	case streamModeSILK:
		if !d.fixedSILKCaptureValid || d.fixedSILKCaptureCursor != needed {
			floatToRes(res, floatOut)
		}
		handled = true
		if d.fixedHybridRedundant {
			handled = d.finishFixedRedundancy(res, frameSize)
		}
		if handled {
			d.applyFixedCELTTransition(res, frameSize)
		}
	case streamModeCELT:
		handled = d.celtFixedRes(parsed, frameSize, toc, res)
		if handled {
			d.applyFixedCELTTransition(res, frameSize)
		}
	case streamModeHybrid:
		if hybridArmed && d.fixedHybridHandled && len(d.fixedHybridRes) >= needed {
			copy(res, d.fixedHybridRes[:needed])
			handled = true
			if d.fixedHybridRedundant && !d.finishFixedRedundancy(res, frameSize) {
				handled = false
			}
			if handled {
				d.applyFixedCELTTransition(res, frameSize)
			}
		}
	}
	if !handled {
		return nil, false, nil
	}
	if d.decodeGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(res, fixedpoint.DecodeGainQ16(int(d.decodeGainQ8)))
	}
	return res, true, nil
}

// decodePacketToFloat32Unscaled advances the shared float decoder state for a
// fixed-domain public decode without applying its final output gain. The fixed
// opus_res path applies that gain with the source integer MULT32_32_Q16 and
// saturation semantics after each stream is decoded.
func (d *streamState) decodePacketToFloat32Unscaled(data []byte, frameSize int) ([]float32, error) {
	gain := d.decodeGainQ8
	d.decodeGainQ8 = 0
	out, err := d.decodePacketToFloat32(data, frameSize)
	d.decodeGainQ8 = gain
	return out, err
}

// finishFixedHybridStream disarms the integer Hybrid highband hook after the
// float Hybrid decode completes.
func (d *streamState) finishFixedHybridStream() {
	d.hybridDec.SetFixedHighband(nil)
}

// streamFixedHybridHook implements hybrid.FixedHybridHighband for one elementary
// stream, building the integer Hybrid frame's opus_res output exactly as the
// single-stream gopus.Decoder.DecodeHybridHighband does.
type streamFixedHybridHook struct {
	st *streamState
}

// DecodeHybridHighband builds the opus_res SILK lowband (INT16TORES: int16 <<
// RES_SHIFT) from the resampled int16 SILK output, then accumulates the integer
// CELT highband (start band 17) onto it from a clone of the shared range
// decoder, matching libopus celt_decode_with_ec with celt_accum=1. The combined
// opus_res output is stashed in the stream's fixedHybridRes for
// decodePacketToResFixed.
//
// rd is supplied already positioned at the CELT start band: the float Hybrid
// afterSilk callback has consumed the Opus-layer redundancy flag and shrunk the
// storage by any trailing redundancy bytes (per the FixedHybridHighband
// contract), so the highband reads from the correct bit position without
// re-parsing the flag. A redundant frame (recorded by afterSilk in
// fixedHybridRedundant) drives a distinct decode and crossfade the integer
// path reconstructs with decodeFixedRedundantCELT and finishFixedRedundancy.
func (h *streamFixedHybridHook) DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, dataLen, frameSizeAPI, frameSize48 int, packetStereo bool) {
	d := h.st
	channels := int(d.channels)
	needed := frameSizeAPI * channels

	if d.fixedHybridRedundantToSilk && !d.decodeFixedRedundantCELT(false) {
		d.fixedHybridHandled = false
		return
	}

	d.prepareFixedHybridHighband(frameSizeAPI)

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

	downsample := d.fixedCELTDownsample()
	coreFrameSize := frameSizeAPI * downsample

	rdClone := &d.fixedHybridRangeDecoder
	*rdClone = *rd
	handled := d.decodeFixedHybridAccum(rdClone, dataLen, coreFrameSize, packetStereo, res)
	*rdClone = rangecoding.Decoder{}
	if !handled {
		d.fixedHybridHandled = false
		return
	}

	d.fixedHybridRes = res
	d.fixedHybridHandled = true
}

// floatToRes converts float32 PCM to opus_res via the lossless int16
// round-trip: opus_res = INT16TORES(int16) = int16 << RES_SHIFT (RES_SHIFT=8).
// This matches libopus' FIXED_POINT opus_res for integer-native SILK output and
// SILK PLC, whose samples already have integer precision.
func floatToRes(res []int32, samples []float32) {
	n := len(res)
	if len(samples) < n {
		n = len(samples)
	}
	for i := 0; i < n; i++ {
		res[i] = int32(opusmath.Float32ToInt16(samples[i])) << 8
	}
	for i := n; i < len(res); i++ {
		res[i] = 0
	}
}
