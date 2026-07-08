package gopus

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/silk"
)

// Decode decodes an Opus packet into float32 PCM samples.
//
// data: Opus packet data, or nil for Packet Loss Concealment (PLC).
// pcm: Output buffer for decoded samples. Must be large enough to hold
// frameSize * frameCount * channels samples, where frameSize and frameCount
// are determined from the packet TOC and frame code.
//
// Returns the number of samples per channel decoded, or an error.
//
// When data is nil, the decoder performs packet loss concealment using
// the last successfully decoded frame parameters. Before the first packet has
// been decoded, cold PLC returns zeroed audio and a nil error.
//
// Buffer sizing: For 60ms frames at 48kHz stereo, pcm must have at least
// 2880 * 2 = 5760 elements. For multi-frame packets (code 1/2/3), the buffer
// must be large enough for all frames combined.
//
// Multi-frame packets (RFC 6716 Section 3.2):
//   - Code 0: 1 frame (most common)
//   - Code 1: 2 equal-sized frames
//   - Code 2: 2 different-sized frames
//   - Code 3: Arbitrary number of frames (1-48)
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error) {
	if d.is96kHz() {
		return d.decode96kFloat32(data, pcm)
	}
	return d.decodeFloat32(data, pcm, true)
}

func (d *Decoder) decodeFloat32(data []byte, pcm []float32, clearSoftClipOnPacket bool) (int, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	dredPossible := false
	if extsupport.DREDRuntime {
		dredPossible = d.dredDecodeSidecarPossible()
		if len(data) > 0 && dredPossible && d.dredCachedPayloadActive() {
			d.invalidateDREDPayloadState()
		}
	}

	if len(data) == 0 {
		frameSize, err := d.plcOutputFrameSize(len(pcm))
		if err != nil {
			return 0, err
		}
		// libopus opus_demo (src/opus_demo.c, lost branch ~L1142) always drives
		// PLC with OPUS_GET_LAST_PACKET_DURATION as frame_size, never the
		// maximum decode buffer. gopus derives the requested PLC duration from
		// the output buffer length, so a caller that hands over a full
		// maxPacketSamples buffer (the conventional "size unknown, give me
		// room" sentinel documented on Decode) would otherwise conceal the
		// whole buffer instead of one packet. When the buffer is exactly the
		// max-packet size and a real packet has already been decoded, fall back
		// to the cached last-packet duration to match opus_demo. Deliberately
		// sized requests -- including overlong ones larger than the max buffer
		// -- are still honored verbatim (see the API-rate overlong PLC tests).
		if frameSize == d.maxPacketSamples && int(d.lastPacketDuration) > 0 {
			frameSize = int(d.lastPacketDuration)
		}
		packetFrameSize := int(d.lastFrameSize)
		if packetFrameSize <= 0 {
			packetFrameSize = frameSize
		}
		neuralReady := dredPossible && d.dredNeuralConcealmentAvailable()
		n := frameSize
		usedNeuralConcealment := false
		if neuralReady && d.prevMode == ModeSILK && channels >= 1 && channels <= 2 {
			n, usedNeuralConcealment, err = d.decodeSILKNeuralPLCInto(pcm, frameSize, plcDecodeState{
				packetFrameSize:    packetFrameSize,
				mode:               d.prevMode,
				bandwidth:          d.lastBandwidth,
				packetStereo:       d.prevPacketStereo,
				useDecoderPLCState: true,
			})
		} else if d.dredNeuralConcealmentAvailable() && sampleRate == 16000 && (d.prevMode == ModeCELT || d.prevMode == ModeHybrid) && channels >= 1 && channels <= 2 {
			// libopus opus_decode(NULL) runs FRAME_PLC_NEURAL (pure LPCNet
			// concealment, no DRED) for lost CELT/Hybrid frames whenever the DNN
			// model is loaded -- it does NOT fall back to the classical
			// pitch/noise PLC, and it does not depend on any queued DRED sidecar.
			// Mirror that without queuing cached DRED features (dred==NULL means
			// the FRAME_DRED branch is not taken). The 16 kHz API gate matches
			// the established gopus neural-CELT-PLC scope (and the zero-alloc
			// 48 kHz core-model contract): only the 16 kHz API rate exercises
			// this path in the parity matrix.
			n, usedNeuralConcealment, err = d.decodeCELTNeuralPLCInto(pcm, frameSize, plcDecodeState{
				packetFrameSize:    packetFrameSize,
				mode:               d.prevMode,
				bandwidth:          d.lastBandwidth,
				packetStereo:       d.prevPacketStereo,
				useDecoderPLCState: true,
			})
		}
		if err != nil {
			return 0, err
		}
		// libopus opus_decode(NULL,...) passes dred==NULL, so the cached-DRED
		// FEC-feature feed gated on `dred != NULL && process_stage == 2`
		// (opus_decoder.c:736) is skipped and a public packet-loss decode runs
		// PLAIN PLC, consuming no cached DRED. Mirror that here for CELT/hybrid
		// (and the 16 kHz CELT neural path): do NOT auto-apply cached DRED on a
		// public Decode(nil). DRED is only applied through the explicit
		// DRED-decode path (decodeExplicitDREDFloat). This matches the SILK
		// public-loss reconciliation done in cc04ecf0.
		if !usedNeuralConcealment {
			n, err = d.decodePLCChunksInto(pcm, frameSize, plcDecodeState{
				packetFrameSize:    packetFrameSize,
				mode:               d.prevMode,
				bandwidth:          d.lastBandwidth,
				packetStereo:       d.prevPacketStereo,
				useDecoderPLCState: true,
			})
		}
		if err != nil {
			return 0, err
		}
		frameSize = n
		// libopus enables OSCE_MODE_SILK_BBWE during PLC whenever the
		// internal sample rate is 16 kHz and the API sample rate is 48 kHz
		// (`data == NULL` branch in opus_decoder.c). The gopus equivalent
		// gate uses the previous packet's mode/bandwidth as the BWE
		// eligibility signal: only SILK WB carries the 16 kHz internal SR
		// that BWE expects. Stereo and DRED neural concealment paths are
		// intentionally excluded so the BWE never overwrites richer
		// concealment output.
		//
		// LACE/NoLACE does not enhance packet-loss frames in libopus:
		// `silk_decode_frame` calls `osce_reset` on the lost branch.
		// Keep that state transition here before optional BWE runs on the
		// concealed SILK lowband.
		if extsupport.OSCERuntime {
			packetStereoLocal := d.prevPacketStereo
			if d.lastPacketMode == ModeSILK &&
				d.lastBandwidth == BandwidthWideband &&
				sampleRate == 48000 && d.osceLACEActive() {
				d.resetOSCELACEPostfilterState(packetStereoLocal)
			}
			if !usedNeuralConcealment && d.lastPacketMode == ModeSILK &&
				d.lastBandwidth == BandwidthWideband &&
				sampleRate == 48000 && d.osceBWEActive() {
				d.maybeApplyOSCEBWEPostSilk(pcm[:frameSize*channels], frameSize, ModeSILK, silk.BandwidthWideband, packetStereoLocal)
			}
		}
		d.applyOutputGain(pcm[:frameSize*channels])

		d.lastFrameSize = int32(packetFrameSize)
		d.lastPacketDuration = int32(frameSize)
		d.lastDataLen = 0
		if dredPossible && !usedNeuralConcealment && d.dredGoodPacketMarkerActive() {
			d.markDREDConcealed()
		}
		return frameSize, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	tocValue, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	toc := &tocValue
	frameCode := data[0] & 0x03
	frameSize := toc.FrameSize
	if toc.Mode == ModeSILK || toc.Mode == ModeCELT || toc.Mode == ModeHybrid {
		frameSize = packetTOCSamplesPerFrameAtRate(data[0], sampleRate)
	}
	totalSamples := frameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	needed := totalSamples * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if dredPossible {
		if endRawDREDCapture := d.beginDREDRawMonoGoodFrameCapture(toc.Mode); endRawDREDCapture != nil {
			defer endRawDREDCapture()
		}
	}

	if frameCode == 0 {
		// libopus opus_packet_parse_impl (src/opus.c): non-self-delimited last
		// frame must not exceed 1275 bytes ("last_size > 1275 → OPUS_INVALID_PACKET").
		// For code-0 the only frame fills all of data[1:].
		if len(data)-1 > maxOpusFrameBytes {
			return 0, ErrInvalidPacket
		}
		_, err := d.decodeOpusFrameIntoWithQEXT(
			pcm,
			data[1:],
			frameSize,
			frameSize,
			toc.Mode,
			toc.Bandwidth,
			toc.Stereo,
			nil,
		)
		if err != nil {
			return 0, err
		}
		d.prevPacketStereo = toc.Stereo
	} else {
		_, err := d.decodeMultiFrameFloat32(pcm, data, toc, frameCode, frameSize)
		if err != nil {
			return 0, err
		}
	}

	// OSCE BWE transition bookkeeping: when the current packet does not
	// satisfy OSCE_MODE_SILK_BBWE (Hybrid or mono SILK NB/MB), clear the
	// previous-BWE-active flag so the next SILK WB packet does not
	// erroneously fade in. The SILK-only post-decode hook handles the
	// SILK -> SILK cross-fade itself; this catches Hybrid and CELT
	// transitions where the SILK helper is not invoked.
	if extsupport.OSCERuntime {
		d.osceBWEMarkInactiveIfModeIneligible(toc.Mode, toc.Bandwidth, pcm[:totalSamples*channels], totalSamples, toc.Stereo)
	}

	// OSCE LACE/NoLACE transition bookkeeping: clear the previous-LACE-
	// active flag when the current packet bypasses the postfilter (Hybrid
	// or CELT). Mirrors libopus `osce_reset` which gets called whenever
	// `osce_enhance_frame` exits early (e.g. fs_kHz != 16), priming the
	// reset counter so the next LACE-active frame runs the cross-fade.
	if extsupport.OSCERuntime {
		d.osceLACEMarkInactiveIfModeIneligible(toc.Mode, toc.Bandwidth)
	}

	d.lastFrameSize = int32(frameSize)
	d.lastPacketDuration = int32(totalSamples)
	d.lastBandwidth = toc.Bandwidth
	d.bandwidthKnown = true
	d.lastPacketMode = toc.Mode
	d.lastDataLen = int32(len(data))

	d.clearFECState()

	if dredPossible {
		if d.dredPayloadScannerActive() {
			d.maybeCacheDREDPayload(data)
		}
		if d.dredGoodPacketMarkerActive() {
			if r := d.dredRecoveryState(); r != nil && d.dredNeuralModelsLoaded() {
				r.dredRecovery = 0
			}
			d.markDREDUpdatedPCM(pcm[:totalSamples*channels], totalSamples, toc.Mode)
		}
	}
	d.applyOutputGain(pcm[:totalSamples*channels])
	if clearSoftClipOnPacket {
		d.clearSoftClipMem()
	}
	return totalSamples, nil
}

func (d *Decoder) decodeMultiFrameFloat32(pcm []float32, data []byte, toc *TOC, frameCode byte, frameSize int) (int, error) {
	channels := int(d.channels)
	offsetSamples := 0
	var qextPayloads decoderQEXTPayloads
	decodeFrame := func(frameIndex int, frameData []byte) error {
		var qextPayload []byte
		if extsupport.QEXT && !d.ignoreExtensions {
			qextPayload = qextPayloads.frame(frameIndex)
		}
		n, err := d.decodeOpusFrameIntoWithQEXT(
			pcm[offsetSamples*channels:],
			frameData,
			frameSize,
			frameSize,
			toc.Mode,
			toc.Bandwidth,
			toc.Stereo,
			qextPayload,
		)
		if err != nil {
			return err
		}
		offsetSamples += n
		d.prevPacketStereo = toc.Stereo
		return nil
	}

	switch frameCode {
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return 0, ErrInvalidPacket
		}
		frameLen := frameDataLen / 2
		// libopus opus_packet_parse_impl (src/opus.c): the implicit (CBR) per-frame
		// size is not coded, so it can exceed 1275; reject when last_size = len/2
		// > 1275 ("last_size > 1275 → OPUS_INVALID_PACKET").
		if frameLen > maxOpusFrameBytes {
			return 0, ErrInvalidPacket
		}
		offset := 1
		for i := range 2 {
			if offset+frameLen > len(data) {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(i, data[offset:offset+frameLen]); err != nil {
				return 0, err
			}
			offset += frameLen
		}
	case 2:
		if len(data) < 2 {
			return 0, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return 0, err
		}
		headerLen := 1 + bytesRead
		frame2Len := len(data) - headerLen - frame1Len
		if frame2Len < 0 {
			return 0, ErrInvalidPacket
		}
		if headerLen+frame1Len > len(data) {
			return 0, ErrInvalidPacket
		}
		// libopus opus_packet_parse_impl (src/opus.c): non-self-delimited last
		// frame must not exceed 1275 bytes ("last_size > 1275 → OPUS_INVALID_PACKET").
		// For code-2 the last (second) frame is frame2Len.
		if frame2Len > maxOpusFrameBytes {
			return 0, ErrInvalidPacket
		}
		if err := decodeFrame(0, data[headerLen:headerLen+frame1Len]); err != nil {
			return 0, err
		}
		offset := headerLen + frame1Len
		if offset+frame2Len > len(data) {
			return 0, ErrInvalidPacket
		}
		if err := decodeFrame(1, data[offset:offset+frame2Len]); err != nil {
			return 0, err
		}
	case 3:
		if len(data) < 2 {
			return 0, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)
		if m == 0 || m > 48 {
			return 0, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0

		if hasPadding {
			for {
				if offset >= len(data) {
					return 0, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
				}
				if padByte < 255 {
					break
				}
			}
			if extsupport.QEXT && !d.ignoreExtensions && toc.Mode != ModeSILK {
				if padding > len(data) {
					return 0, ErrInvalidPacket
				}
				qextPayloads.collect(data[len(data)-padding:], m, qextPacketExtensionID)
			}
		}

		if vbr {
			var frameLens [48]int
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, err
				}
				offset += bytesRead
				frameLens[i] = frameLen
			}
			frameDataOffset := offset
			for i := 0; i < m-1; i++ {
				frameLen := frameLens[i]
				if frameDataOffset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(i, data[frameDataOffset:frameDataOffset+frameLen]); err != nil {
					return 0, err
				}
				frameDataOffset += frameLen
			}
			lastFrameLen := len(data) - frameDataOffset - padding
			if lastFrameLen < 0 {
				return 0, ErrInvalidPacket
			}
			if frameDataOffset+lastFrameLen > len(data)-padding {
				return 0, ErrInvalidPacket
			}
			// libopus opus_packet_parse_impl (src/opus.c): the VBR last frame size
			// is implicit (last_size), so it can exceed 1275; reject when
			// last_size > 1275 ("last_size > 1275 → OPUS_INVALID_PACKET").
			if lastFrameLen > maxOpusFrameBytes {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(m-1, data[frameDataOffset:frameDataOffset+lastFrameLen]); err != nil {
				return 0, err
			}
		} else {
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return 0, ErrInvalidPacket
			}
			if frameDataLen%m != 0 {
				return 0, ErrInvalidPacket
			}
			frameLen := frameDataLen / m
			// libopus opus_packet_parse_impl (src/opus.c): for the implicit CBR
			// framing the per-frame size (applied to all frames) is not coded, so
			// it can exceed 1275; reject when last_size = len/count > 1275.
			if frameLen > maxOpusFrameBytes {
				return 0, ErrInvalidPacket
			}
			for i := range m {
				if offset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(i, data[offset:offset+frameLen]); err != nil {
					return 0, err
				}
				offset += frameLen
			}
		}
	}

	return offsetSamples, nil
}

// DecodeWithFEC decodes an Opus packet, optionally recovering a lost frame using FEC.
//
// This mirrors libopus decode_fec semantics: when fec is true, the decoder
// uses in-band LBRR data if present and otherwise falls back to packet loss
// concealment instead of returning a missing-FEC error.
func (d *Decoder) DecodeWithFEC(data []byte, pcm []float32, fec bool) (int, error) {
	if !fec {
		return d.Decode(data, pcm)
	}
	// At 96 kHz, FEC uses the same routing as regular decode (PLC path).
	// SILK/Hybrid FEC is not supported at 96 kHz (no SILK resampler path).
	if d.is96kHz() {
		return d.decode96kFloat32(nil, pcm)
	}
	sampleRate := int(d.sampleRate)

	if len(data) > 0 {
		// libopus opus_decode runs opus_packet_parse_impl on the packet before the
		// decode_fec branch (src/opus_decoder.c:781) and returns its error on a
		// malformed packet, exactly as the plain decode path does. Validate the
		// full frame structure here so DecodeWithFEC rejects the same packets as
		// Decode (e.g. an odd-length code-1 packet) rather than running FEC/PLC on
		// a structurally invalid bitstream.
		if _, err := ParsePacket(data); err != nil {
			return 0, err
		}
		toc, frameCount, err := packetFrameCount(data)
		if err != nil {
			return 0, err
		}
		requestedFrameSize, err := d.requestedOutputFrameSize(len(pcm))
		if err != nil {
			return 0, err
		}
		frameSize := toc.FrameSize
		if toc.Mode == ModeSILK || toc.Mode == ModeCELT || toc.Mode == ModeHybrid {
			frameSize = packetTOCSamplesPerFrameAtRate(data[0], sampleRate)
		}
		if frameSize <= 0 {
			frameSize = int(d.lastFrameSize)
		}
		if frameSize <= 0 {
			frameSize = sampleRate / 50
		}

		prevPacketMode := d.lastPacketMode
		if requestedFrameSize < frameSize || toc.Mode == ModeCELT || prevPacketMode == ModeCELT {
			d.clearFECState()
			plcSize, err := d.plcOutputFrameSize(len(pcm))
			if err != nil {
				return 0, err
			}
			return d.decodePLCForFEC(pcm, plcSize)
		}
		d.lastPacketMode = toc.Mode

		if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
			firstFrameData, err := extractFirstFramePayload(data, toc)
			if err != nil {
				return 0, err
			}
			if !packetHasLBRR(firstFrameData, toc) {
				d.clearFECState()
				if extsupport.DREDRuntime && d.dredCachedPayloadActive() {
					return d.decodePLCForFECWithState(pcm, requestedFrameSize, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
				}
				return d.decodeNoLBRRFECFallback(pcm, requestedFrameSize, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
			}
			d.storeFECData(firstFrameData, toc, frameCount, frameSize)
			if n, err := d.decodeFECFrame(pcm, requestedFrameSize); err == nil {
				return n, nil
			}
			d.clearFECState()
		}
		return d.decodePLCForFECWithState(pcm, requestedFrameSize, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
	}

	d.clearFECState()
	frameSize, err := d.plcOutputFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	return d.decodePLCForFEC(pcm, frameSize)
}

// DecodeInt16 decodes an Opus packet into int16 PCM samples.
func (d *Decoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	if d.is96kHz() {
		return d.decodeInt1696k(data, pcm)
	}
	d.beginFixedPacket()
	defer d.endFixedPacket()
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if len(data) == 0 {
		frameSize, err := d.plcOutputFrameSize(len(pcm))
		if err != nil {
			return 0, err
		}

		needed := frameSize * channels
		d.ensureScratchPCM(needed)
		n, err := d.decodeFloat32(data, d.scratchPCM, false)
		if err != nil {
			return 0, err
		}
		// CELT-only packet loss is concealed bit-exact by the integer
		// FIXED_POINT path under -tags gopus_fixed_point; otherwise the float PLC
		// output is quantized without soft clip (concealed audio is never
		// clipped), matching the default build.
		d.fixedApplyDecodeGain(n * channels)
		if !d.fixedInt16PLCOutput(pcm, n, channels) {
			float32ToInt16NoSoftClip(pcm, d.scratchPCM, n, channels)
		}
		return n, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	if len(pcm) >= d.maxPacketSamples*channels {
		d.ensureScratchPCM(d.maxPacketSamples * channels)
		n, err := d.decodeFloat32(data, d.scratchPCM, false)
		if err != nil {
			return 0, err
		}
		d.fixedApplyDecodeGain(n * channels)
		d.finishInt16Output(pcm, d.scratchPCM, n, channels)
		return n, nil
	}

	toc, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	frameSize := toc.FrameSize
	if toc.Mode == ModeSILK || toc.Mode == ModeCELT || toc.Mode == ModeHybrid {
		frameSize = packetTOCSamplesPerFrameAtRate(data[0], sampleRate)
	}
	totalSamples := frameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}
	needed := totalSamples * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	d.ensureScratchPCM(needed)
	n, err := d.decodeFloat32(data, d.scratchPCM, false)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	d.finishInt16Output(pcm, d.scratchPCM, n, channels)
	return n, nil
}

// DecodeInt24 decodes an Opus packet into 24-bit PCM samples stored in int32.
//
// data: Opus packet data, or nil for Packet Loss Concealment (PLC).
// pcm: Output buffer for decoded samples. Each element carries a signed
// 24-bit value in the range [-8388608, 8388607] (= ±2^23), right-justified
// in int32 — the same convention as libopus opus_decode24().
//
// Returns the number of samples per channel decoded, or an error.
func (d *Decoder) DecodeInt24(data []byte, pcm []int32) (int, error) {
	if d.is96kHz() {
		return d.decodeInt2496k(data, pcm)
	}
	d.beginFixedPacket()
	defer d.endFixedPacket()
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if len(data) == 0 {
		frameSize, err := d.plcOutputFrameSize(len(pcm))
		if err != nil {
			return 0, err
		}

		needed := frameSize * channels
		d.ensureScratchPCM(needed)
		n, err := d.decodeFloat32(data, d.scratchPCM, false)
		if err != nil {
			return 0, err
		}
		d.fixedApplyDecodeGain(n * channels)
		if !d.fixedInt24PLCOutput(pcm, n, channels) {
			float32ToInt24Slice(pcm, d.scratchPCM, n, channels)
		}
		return n, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	if len(pcm) >= d.maxPacketSamples*channels {
		d.ensureScratchPCM(d.maxPacketSamples * channels)
		n, err := d.decodeFloat32(data, d.scratchPCM, false)
		if err != nil {
			return 0, err
		}
		d.fixedApplyDecodeGain(n * channels)
		d.finishInt24Output(pcm, d.scratchPCM, n, channels)
		return n, nil
	}

	toc, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	frameSize := toc.FrameSize
	if toc.Mode == ModeSILK || toc.Mode == ModeCELT || toc.Mode == ModeHybrid {
		frameSize = packetTOCSamplesPerFrameAtRate(data[0], sampleRate)
	}
	totalSamples := frameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}
	needed := totalSamples * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	d.ensureScratchPCM(needed)
	n, err := d.decodeFloat32(data, d.scratchPCM, false)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	d.finishInt24Output(pcm, d.scratchPCM, n, channels)
	return n, nil
}

// DecodeInt24Slice decodes an Opus packet into 24-bit PCM samples and returns a
// new int32 slice. Each element carries a right-justified signed 24-bit value.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use DecodeInt24 with a pre-allocated buffer.
func (d *Decoder) DecodeInt24Slice(data []byte) ([]int32, error) {
	channels := int(d.channels)
	var frameSize int
	if len(data) == 0 {
		frameSize = d.maxPacketSamples
		if int(d.lastPacketDuration) > 0 {
			frameSize = int(d.lastPacketDuration)
		}
	} else {
		sampleRate := int(d.sampleRate)
		toc, frameCount, err := packetFrameCount(data)
		if err != nil {
			return nil, err
		}
		fs := toc.FrameSize
		if toc.Mode == ModeSILK || toc.Mode == ModeCELT || toc.Mode == ModeHybrid {
			fs = packetTOCSamplesPerFrameAtRate(data[0], sampleRate)
		}
		frameSize = fs * frameCount
	}
	pcm := make([]int32, frameSize*channels)
	n, err := d.DecodeInt24(data, pcm)
	if err != nil {
		return nil, err
	}
	return pcm[:n*channels], nil
}

// float32ToInt24Slice converts n*channels float32 samples to int32 using the
// libopus 24-bit PCM conversion (RES2INT24 in arch.h for the float build).
func float32ToInt24Slice(dst []int32, src []float32, n, channels int) {
	if channels < 1 || n < 1 || len(src) == 0 || len(dst) == 0 {
		return
	}
	total := min(n*channels, len(src))
	if total > len(dst) {
		total = len(dst)
	}
	if total <= 0 {
		return
	}
	for i := 0; i < total; i++ {
		dst[i] = float32ToInt24(src[i])
	}
}

func (d *Decoder) ensureScratchPCM(needed int) {
	if cap(d.scratchPCM) < needed {
		d.scratchPCM = make([]float32, needed)
		return
	}
	d.scratchPCM = d.scratchPCM[:needed]
}
