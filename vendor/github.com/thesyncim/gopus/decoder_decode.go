package gopus

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/silk"
)

// Decode decodes data into interleaved float32 PCM in pcm. The buffer length is
// measured in samples across all channels; the returned sample count is per
// channel, and the first n*Channels elements contain the output.
//
// An empty data slice performs packet-loss concealment. Its requested duration
// comes from len(pcm)/Channels and must be a positive multiple of 2.5 ms. If pcm
// has exactly DecoderConfig.MaxPacketSamples*Channels elements and a packet has
// already been decoded or concealed, Decode uses the most recent output
// duration as the concealment request. Before the first decode, a valid
// full-buffer request returns zeroed PCM.
//
// Deliberately sized PLC requests larger than MaxPacketSamples are honored;
// MaxPacketSamples and MaxPacketBytes limit coded packets. Decode returns an
// error for malformed packets, packet-limit violations, invalid PLC durations,
// or a short output buffer.
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error) {
	if len(pcm) < int(d.channels) {
		// The public libopus wrappers reject frame_size <= 0 before packet parsing
		// (src/opus_decoder.c: opus_decode, opus_decode24, opus_decode_float).
		// gopus keeps ErrBufferTooSmall as its
		// empty-output facade result and returns before touching decoder state.
		return 0, ErrBufferTooSmall
	}
	if d.is96kHz() {
		return d.decode96kFloat32(data, pcm)
	}
	return d.decodePublicFloat32(data, pcm)
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
		return d.decodeLossFloat32(pcm, dredPossible)
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
		// libopus parses the complete packet before it checks the output
		// capacity. Keep malformed framing ahead of ErrBufferTooSmall while
		// limiting this extra parse to the rejected-buffer path.
		if err := validatePacketFraming(data); err != nil {
			return 0, err
		}
		return 0, ErrBufferTooSmall
	}

	if d.beginDREDRawMonoFrameCapture(toc.Mode) {
		return d.decodePacketFloat32Captured(data, pcm, toc, frameCode, frameSize, totalSamples, dredPossible, clearSoftClipOnPacket)
	}
	return d.decodePacketFloat32(data, pcm, toc, frameCode, frameSize, totalSamples, dredPossible, clearSoftClipOnPacket)
}

// decodePacketFloat32Captured is decodePacketFloat32 inside a DRED raw mono
// frame capture, which it ends once the packet is decoded. The capture's defer
// lives here so decodeFloat32 itself carries no defer.
func (d *Decoder) decodePacketFloat32Captured(data []byte, pcm []float32, toc *TOC, frameCode byte, frameSize, totalSamples int, dredPossible, clearSoftClipOnPacket bool) (int, error) {
	defer d.endDREDRawMonoFrameCapture()
	return d.decodePacketFloat32(data, pcm, toc, frameCode, frameSize, totalSamples, dredPossible, clearSoftClipOnPacket)
}

// decodePacketFloat32 decodes the frames of a validated packet into pcm and
// updates the per-packet decoder state.
func (d *Decoder) decodePacketFloat32(data []byte, pcm []float32, toc *TOC, frameCode byte, frameSize, totalSamples int, dredPossible, clearSoftClipOnPacket bool) (int, error) {
	channels := int(d.channels)
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

// decodeLossFloat32 conceals one lost packet into pcm (decodeFloat32 with no
// data).
func (d *Decoder) decodeLossFloat32(pcm []float32, dredPossible bool) (int, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
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
	if d.prevMode == ModeSILK || d.prevMode == ModeHybrid {
		if d.beginDREDRawMonoFrameCapture(d.prevMode) {
			defer d.endDREDRawMonoFrameCapture()
		}
	}
	// The public loss path passes no DRED feature queue to the codec.
	// libopus selects main-model neural PLC when deep PLC is enabled at
	// complexity 5 or higher; sidecar availability does not enable it.
	state := plcDecodeState{
		packetFrameSize:    packetFrameSize,
		mode:               d.prevMode,
		bandwidth:          d.lastBandwidth,
		packetStereo:       d.prevPacketStereo,
		useDecoderPLCState: true,
	}
	n, usedNeuralConcealment, err := d.decodeNeuralPLCInto(pcm, frameSize, state, false)
	if err != nil {
		return 0, err
	}
	// opus_decode(NULL,...) passes no DRED sidecar. Public loss follows the
	// selected PLC path, including neural concealment when its gates pass.
	// Cached DRED features are consumed only by explicit DRED decode.
	if !usedNeuralConcealment {
		n, err = d.decodePLCChunksInto(pcm, frameSize, state)
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
	// LACE/NoLACE resets at each internal SILK loss boundary before CNG
	// and frame gluing. Optional BWE processes the resulting lowband here.
	if extsupport.OSCERuntime {
		packetStereoLocal := d.prevPacketStereo
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
			explicitTotal := 0
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, err
				}
				offset += bytesRead
				frameLens[i] = frameLen
				explicitTotal += frameLen
			}
			// opus_packet_parse_impl validates every length before opus_decode
			// advances any frame state, including the implicit final length.
			lastFrameLen := len(data) - offset - padding - explicitTotal
			if lastFrameLen < 0 || lastFrameLen > maxOpusFrameBytes {
				return 0, ErrInvalidPacket
			}
			frameLens[m-1] = lastFrameLen
			frameDataOffset := offset
			for i := range m {
				frameLen := frameLens[i]
				if err := decodeFrame(i, data[frameDataOffset:frameDataOffset+frameLen]); err != nil {
					return 0, err
				}
				frameDataOffset += frameLen
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

// DecodeWithFEC decodes data into interleaved float32 PCM and returns samples
// per channel. When fec is false, it behaves like Decode. When fec is true,
// data is the packet received after a loss and len(pcm)/Channels requests the
// missing duration; the request must be a positive multiple of 2.5 ms. The
// decoder recovers in-band FEC when usable LBRR is present and otherwise
// performs packet-loss concealment. Empty data also performs concealment. This
// call does not decode the supplied packet's primary frame: call Decode with
// the same packet afterward.
func (d *Decoder) DecodeWithFEC(data []byte, pcm []float32, fec bool) (int, error) {
	if len(pcm) < int(d.channels) {
		return 0, ErrBufferTooSmall
	}
	if !fec {
		return d.Decode(data, pcm)
	}
	return d.decodeFECPublicFloat32(data, pcm)
}

func (d *Decoder) decodeWithFECFloat32(data []byte, pcm []float32) (int, error) {
	sampleRate := int(d.sampleRate)

	if len(data) > 0 {
		if len(data) > d.maxPacketBytes {
			return 0, ErrPacketTooLarge
		}
		requestedFrameSize, err := d.requestedOutputFrameSize(len(pcm))
		if err != nil {
			return 0, err
		}
		// Match opus_decode_native in libopus src/opus_decoder.c: validate the
		// requested FEC duration before parsing the packet, then reject malformed
		// framing before attempting FEC or falling back to concealment.
		if err := validatePacketFraming(data); err != nil {
			return 0, err
		}
		toc, frameCount, err := packetFrameCount(data)
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
				d.storeFECDataForDecode(firstFrameData, toc, frameCount, frameSize)
				n, err := d.decodeFECFrame(pcm, requestedFrameSize)
				if err != nil {
					d.clearFECState()
					return 0, err
				}
				return n, nil
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

// DecodeInt16 decodes data into interleaved signed 16-bit PCM in pcm. The
// returned sample count is per channel; pcm must hold n*Channels elements, and
// the first n*Channels elements contain the output. An empty data slice performs
// packet-loss concealment, with the requested duration derived from pcm's
// per-channel length.
func (d *Decoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	if len(pcm) < int(d.channels) {
		return 0, ErrBufferTooSmall
	}
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
		if err := validatePacketFraming(data); err != nil {
			return 0, err
		}
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

// DecodeInt24 decodes data into interleaved 24-bit-scale PCM stored in pcm.
// Each int32 holds a right-justified signed value; the nominal 24-bit range is
// [-8388608, 8388607]. The libopus RES2INT24 conversion does not soft-clip or
// clamp to that range: a +1.0 sample maps to 8388608, and output gain can also
// produce values outside the nominal 24-bit interval. The returned sample count
// is per channel; pcm must hold n*Channels elements. An empty data slice
// performs packet-loss concealment.
func (d *Decoder) DecodeInt24(data []byte, pcm []int32) (int, error) {
	if len(pcm) < int(d.channels) {
		return 0, ErrBufferTooSmall
	}
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
		// opus_decode24 disables output soft clipping and clears the int16
		// soft-clip history after a received packet. PLC returns before that
		// state update, so keep its separate path above unchanged.
		n, err := d.decodeFloat32(data, d.scratchPCM, true)
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
		if err := validatePacketFraming(data); err != nil {
			return 0, err
		}
		return 0, ErrBufferTooSmall
	}

	d.ensureScratchPCM(needed)
	n, err := d.decodeFloat32(data, d.scratchPCM, true)
	if err != nil {
		return 0, err
	}
	d.fixedApplyDecodeGain(n * channels)
	d.finishInt24Output(pcm, d.scratchPCM, n, channels)
	return n, nil
}

// DecodeInt24Slice decodes data into a newly allocated, caller-owned
// interleaved PCM slice. Each int32 holds a right-justified signed 24-bit-scale
// value using the same unsaturated conversion as DecodeInt24. The slice contains
// n*Channels elements for n samples per channel. For empty data, it requests the
// most recent output duration, or MaxPacketSamples when LastPacketDuration is zero.
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
	total := min(min(n*channels, len(src)), len(dst))
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
