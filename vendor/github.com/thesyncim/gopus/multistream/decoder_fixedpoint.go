//go:build gopus_fixed_point

package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeToResFixed decodes a multistream packet and returns the libopus
// FIXED_POINT per-output-channel opus_res samples, interleaved by output
// channel ([ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]). It mirrors
// opus_multistream_decode_native built FIXED_POINT: each elementary stream is
// decoded to opus_res, then the surround channel mapping
// (copy_channel_out_short / copy_channel_out_int24) routes each stream channel
// to its output channel(s) in the integer domain.
//
// The second return value reports whether every stream frame was produced by
// the integer path (CELT-only) or is integer-exact through the SILK round-trip.
// When it is false the packet contains a frame the integer path does not cover
// (Hybrid, multi-frame edge cases, projection, decode gain, or a concealment
// frame); the caller must fall back to the float conversion for that packet.
//
// The output opus_res values feed RES2INT16 (int16) or RES2INT24==identity
// (int24) per the libopus copy_channel_out routines.
func (d *Decoder) DecodeToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	if data == nil || len(data) == 0 {
		return nil, false, nil
	}
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return nil, false, nil
	}
	if extsupport.QEXT && !d.ignoreExtensions {
		return nil, false, nil
	}
	for _, dec := range d.decoders {
		if st, ok := dec.(*streamState); ok && st.decodeGainQ8 != 0 {
			return nil, false, nil
		}
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		return nil, false, nil
	}

	packets, err := parseMultistreamPacket(data, d.streams)
	if err != nil {
		return nil, false, err
	}
	duration, err := validateStreamDurationsAtRate(packets, int(d.sampleRate))
	if err != nil {
		return nil, false, err
	}
	if duration > frameSize {
		return nil, false, ErrBufferTooSmall
	}
	decodeFrameSize := duration

	// Classify every stream up front without decoding so a packet containing a
	// frame the integer path does not cover (Hybrid, multi-frame, DTX/PLC) is
	// declined before any decode runs. This avoids double-decoding (which would
	// corrupt the shared float cross-frame state) when the caller falls back to
	// the float conversion.
	for i := 0; i < d.streams; i++ {
		if _, ok := d.decoders[i].(*streamState); !ok {
			return nil, false, nil
		}
		if !fixedHandleableStreamPacket(packets[i], int(d.sampleRate)) {
			return nil, false, nil
		}
	}

	// Every stream passed the pre-check, so each decode advances the shared
	// float state exactly once and yields bit-exact opus_res. A post-check
	// decline would mean state was advanced but the result is unusable, so it is
	// surfaced as an error rather than silently re-decoded by the caller.
	streamRes := make([][]int32, d.streams)
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

	out := applyChannelMappingRes(streamRes, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return out, true, nil
}

// applyChannelMappingRes routes per-stream opus_res samples to output channels,
// mirroring the copy_channel_out routing in opus_multistream_decode_native:
// each stream channel feeds the output channel(s) selected by the mapping, and
// muted channels (mapping value 255) stay zero.
func applyChannelMappingRes(streamRes [][]int32, mapping []byte, coupledStreams, frameSize, outputChannels int) []int32 {
	out := make([]int32, frameSize*outputChannels)
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
// can reproduce a stream packet bit-exactly: a single received frame that is
// CELT-only (decoded by the integer CELT decoder), SILK-only (integer-exact
// through the lossless float->int16 round-trip), or Hybrid (integer SILK
// opus_res lowband plus integer CELT highband, start band 17, celt_accum).
// Multi-frame packets and degenerate (DTX/PLC) frames are not covered. A Hybrid
// stream at an API rate below 16 kHz is also declined: its wideband SILK lowband
// is produced by the float downsampling resampler, which has no integer int16
// output for INT16TORES, so the integer hybrid path cannot reproduce it (the
// float conversion is bit-exact with the FIXED_POINT reference for those rates).
func fixedHandleableStreamPacket(data []byte, sampleRate int) bool {
	if len(data) <= 1 {
		return false
	}
	toc := parseStreamTOC(data[0])
	if toc.mode != streamModeCELT && toc.mode != streamModeSILK && toc.mode != streamModeHybrid {
		return false
	}
	if toc.mode == streamModeHybrid && sampleRate < 16000 {
		return false
	}
	parsed, err := parseOpusPacket(data, false)
	if err != nil || len(parsed.frames) != 1 {
		return false
	}
	return len(parsed.frames[0]) > 1
}

// decodePacketToResFixed decodes one elementary-stream packet to interleaved
// opus_res samples (stride = stream channel count). The packet must already be
// classified handleable by fixedHandleableStreamPacket.
//
// It runs the float decode first to advance the float cross-frame state (so a
// following float Decode or PLC frame is unaffected), then captures
// integer-exact opus_res:
//
//   - CELT-only: the FIXED_POINT integer CELT decoder
//     (internal/fixedpoint.CELTDecoder), exactly as the single-stream public
//     decoder does.
//   - SILK-only: opus_res = INT16TORES(int16) where the int16 is the lossless
//     float->int16 of the SILK output, matching libopus' FIXED_POINT SILK
//     opus_res (the SILK output is integer-native and round-trips through
//     float32 without loss).
func (d *streamState) decodePacketToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	channels := int(d.channels)

	toc := parseStreamTOC(data[0])

	// A Hybrid frame must arm the integer highband hook on the stream's hybrid
	// decoder before the float decode runs, so the float hybrid decode also drives
	// the integer CELT highband (start band 17, celt_accum) onto the integer SILK
	// opus_res lowband. The hook stashes the combined opus_res output in
	// fixedHybridRes; the CELT-only / SILK paths capture their integer output after
	// the float decode instead.
	hybridArmed := false
	if toc.mode == streamModeHybrid {
		if parsed, perr := parseOpusPacket(data, false); perr == nil && len(parsed.frames) == 1 {
			hybridArmed = d.prepareFixedHybridStream(toc)
		}
	}

	floatOut, err := d.decodePacketToFloat32(data, frameSize)
	if hybridArmed {
		d.finishFixedHybridStream()
	}
	if err != nil {
		return nil, false, err
	}

	needed := frameSize * channels
	if cap(d.fixedRes) < needed {
		d.fixedRes = make([]int32, needed)
	}
	res := d.fixedRes[:needed]

	parsed, perr := parseOpusPacket(data, false)
	if perr != nil || len(parsed.frames) != 1 {
		return nil, false, nil
	}

	switch toc.mode {
	case streamModeSILK:
		floatToRes(res, floatOut)
		return res, true, nil
	case streamModeCELT:
		if d.celtFixedRes(parsed.frames[0], frameSize, toc, res) {
			return res, true, nil
		}
		return nil, false, nil
	case streamModeHybrid:
		if hybridArmed && d.fixedHybridHandled && len(d.fixedHybridRes) >= needed {
			copy(res, d.fixedHybridRes[:needed])
			return res, true, nil
		}
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

// prepareFixedHybridStream arms the integer Hybrid highband hook on the stream's
// hybrid decoder for the in-flight frame, mirroring the single-stream
// prepareFixedHybrid. The integer CELT decoder is created lazily and shared with
// the CELT-only path. It records the CELT end band from the packet bandwidth.
// The multistream float Hybrid decode never resets its CELT decoder mid-session,
// so the integer CELT decoder is likewise never reset here; the two stay in
// lockstep across frames. It returns true once the hook is armed.
func (d *streamState) prepareFixedHybridStream(toc streamTOC) bool {
	// Hybrid SILK is always wideband (16 kHz internal). At an API rate below
	// 16 kHz the SILK lowband is produced by the float downsampling resampler,
	// which has no integer int16 output for INT16TORES, so the integer hybrid
	// highband cannot reproduce the FIXED_POINT lowband. Decline so the stream's
	// float conversion handles the frame (bit-exact with the FIXED_POINT
	// reference for these rates).
	if int(d.sampleRate) < 16000 {
		return false
	}
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	if d.fixedHybridHook == nil {
		d.fixedHybridHook = &streamFixedHybridHook{st: d}
	}
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.fixedHybridRedundant = false
	d.fixedHybridHandled = false
	d.hybridDec.SetFixedHighband(d.fixedHybridHook)
	return true
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
// hybrid path does not reproduce, so it declines.
func (h *streamFixedHybridHook) DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, frameSizeAPI, frameSize48 int, packetStereo bool) {
	d := h.st
	channels := int(d.channels)
	needed := frameSizeAPI * channels

	if d.fixedHybridRedundant {
		d.fixedHybridHandled = false
		return
	}

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

	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSizeAPI * downsample

	rdClone := *rd
	d.fixedCELT.DecodeHybridAccum(&rdClone, coreFrameSize, res)

	d.fixedHybridRes = res
	d.fixedHybridHandled = true
}

// celtFixedRes runs the FIXED_POINT integer CELT decoder for a CELT-only frame
// and writes its opus_res output into res. It mirrors the single-stream
// celtDecodeFixedAPIRate: the integer decoder runs in addition to the float
// decoder (which already advanced the float cross-frame state). It returns true
// when it produced a frame.
func (d *streamState) celtFixedRes(frame []byte, frameSize int, toc streamTOC, res []int32) bool {
	if len(frame) <= 1 {
		return false
	}
	channels := int(d.channels)
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(channels, int(d.sampleRate))
	}

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSize * downsample

	celtBW := celt.BandwidthFromOpusConfig(toc.bandwidth)
	d.fixedCELT.SetBandRange(0, celtBW.EffectiveBands())

	needed := frameSize * channels
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	int16Out := d.fixedCELTPCM[:needed]
	d.fixedCELT.DecodeWithEC(frame, coreFrameSize, int16Out)
	celtRes := d.fixedCELT.LastRes()
	if len(celtRes) < needed {
		return false
	}
	copy(res, celtRes[:needed])
	return true
}

// floatToRes converts float32 PCM to opus_res via the lossless int16
// round-trip: opus_res = INT16TORES(int16) = int16 << RES_SHIFT (RES_SHIFT=8).
// This matches libopus' FIXED_POINT opus_res for integer-native SILK output and
// provides a faithful fallback for declined frames.
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
