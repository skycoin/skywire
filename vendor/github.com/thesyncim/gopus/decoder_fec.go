package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/silk"
)

func (d *Decoder) decodePLCForFEC(pcm []float32, frameSize int) (int, error) {
	packetFrameSize := int(d.lastFrameSize)
	if packetFrameSize <= 0 {
		packetFrameSize = frameSize
	}
	mode := d.prevMode
	if d.prevRedundancy {
		// libopus opus_decode_frame selects CELT PLC when the preceding Hybrid
		// frame ends with CELT redundancy, even though prev_mode remains Hybrid.
		mode = ModeCELT
	}
	return d.decodePLCForFECWithState(pcm, frameSize, packetFrameSize, mode, d.lastBandwidth, d.prevPacketStereo)
}

func (d *Decoder) decodePLCForFECWithState(
	pcm []float32,
	frameSize int,
	packetFrameSize int,
	mode Mode,
	bandwidth Bandwidth,
	packetStereo bool,
) (int, error) {
	channels := int(d.channels)
	if packetFrameSize <= 0 {
		packetFrameSize = frameSize
	}
	state := plcDecodeState{
		packetFrameSize:    packetFrameSize,
		mode:               mode,
		bandwidth:          bandwidth,
		packetStereo:       packetStereo,
		useDecoderPLCState: false,
	}
	if extsupport.DREDRuntime && (mode == ModeSILK || mode == ModeHybrid) {
		if d.beginDREDRawMonoFrameCapture(mode) {
			defer d.endDREDRawMonoFrameCapture()
		}
	}
	usedNeuralConcealment := false
	var n int
	var err error
	n, usedNeuralConcealment, err = d.decodeNeuralPLCInto(pcm, frameSize, state, true)
	if err != nil {
		return 0, err
	}
	// FEC fallback has no DRED sidecar. Public loss uses the model-gated PLC
	// path, and cached DRED features are consumed only by explicit DRED decode.
	if !usedNeuralConcealment {
		n, err = d.decodePLCChunksInto(pcm, frameSize, state)
	}
	if err != nil {
		return 0, err
	}
	frameSize = n
	d.applyOutputGain(pcm[:frameSize*channels])
	d.lastFrameSize = int32(packetFrameSize)
	d.lastPacketDuration = int32(frameSize)
	d.lastDataLen = 0
	if extsupport.DREDRuntime && !usedNeuralConcealment && d.dredGoodPacketMarkerActive() {
		d.markDREDConcealed()
	}
	return frameSize, nil
}

// decodeNeuralPLCInto selects the libopus loss path for the current mode. FEC
// recursion can also enter SILK deep PLC below complexity 5 when queued FEC
// features remain; ordinary public loss uses only the complexity gate.
func (d *Decoder) decodeNeuralPLCInto(pcm []float32, frameSize int, state plcDecodeState, allowQueuedFEC bool) (int, bool, error) {
	if !extsupport.DREDRuntime || d == nil || d.channels < 1 || d.channels > 2 ||
		!d.dredNeuralConcealmentAvailable() {
		return 0, false, nil
	}
	deepPLCEnabled := d.complexity >= 5
	if allowQueuedFEC && (state.mode == ModeSILK || state.mode == ModeHybrid) && d.dredFECFeaturesQueued() {
		deepPLCEnabled = true
	}
	if !deepPLCEnabled {
		return 0, false, nil
	}
	switch state.mode {
	case ModeSILK, ModeHybrid:
		return d.decodeSILKNeuralPLCInto(pcm, frameSize, state)
	case ModeCELT:
		if d.complexity >= 5 {
			return d.decodeCELTNeuralPLCInto(pcm, frameSize, state)
		}
	}
	return 0, false, nil
}

// extractFirstFramePayload extracts the first Opus frame payload bytes from
// a packet. This excludes packet-level TOC and framing headers.
func extractFirstFramePayload(data []byte, toc TOC) ([]byte, error) {
	if len(data) < 1 {
		return nil, ErrPacketTooShort
	}
	// A 1-byte code-0 packet is a valid empty (DTX/silence) frame: its first frame
	// payload is the empty byte slice, not an error. libopus opus_packet_has_lbrr
	// reports no LBRR for it, and decode_fec falls through to PLC. The other framing
	// codes need at least their frame-count / length bytes, handled below.
	if len(data) == 1 {
		if toc.FrameCode == 0 {
			return data[1:], nil
		}
		return nil, ErrPacketTooShort
	}

	switch toc.FrameCode {
	case 0:
		return data[1:], nil
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return nil, ErrInvalidPacket
		}
		frameLen := frameDataLen / 2
		if frameLen <= 0 || 1+frameLen > len(data) {
			return nil, ErrInvalidPacket
		}
		return data[1 : 1+frameLen], nil
	case 2:
		if len(data) < 2 {
			return nil, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return nil, err
		}
		headerLen := 1 + bytesRead
		if frame1Len <= 0 || headerLen+frame1Len > len(data) {
			return nil, ErrInvalidPacket
		}
		return data[headerLen : headerLen+frame1Len], nil
	case 3:
		if len(data) < 2 {
			return nil, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)
		if m == 0 || m > 48 {
			return nil, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(data) {
					return nil, ErrPacketTooShort
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
		}

		if vbr {
			frameDataEnd := len(data) - padding
			if frameDataEnd < offset {
				return nil, ErrInvalidPacket
			}

			frameLen := frameDataEnd - offset
			if m > 1 {
				var bytesRead int
				parsedFrameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return nil, err
				}
				frameLen = parsedFrameLen
				offset += bytesRead
				for i := 1; i < m-1; i++ {
					_, readN, err := parseFrameLength(data, offset)
					if err != nil {
						return nil, err
					}
					offset += readN
				}
			}
			if frameLen <= 0 || offset+frameLen > frameDataEnd {
				return nil, ErrInvalidPacket
			}
			return data[offset : offset+frameLen], nil
		}

		frameDataLen := len(data) - offset - padding
		if frameDataLen < 0 || frameDataLen%m != 0 {
			return nil, ErrInvalidPacket
		}
		frameLen := frameDataLen / m
		if frameLen <= 0 || offset+frameLen > len(data)-padding {
			return nil, ErrInvalidPacket
		}
		return data[offset : offset+frameLen], nil
	default:
		return nil, ErrInvalidPacket
	}
}

// PacketHasLBRR reports whether the first SILK or Hybrid frame in data carries
// the LBRR flag for in-band FEC. It returns false for CELT and when the first
// frame cannot be extracted. This is a payload probe, not full packet framing
// validation; use ParsePacket to validate the complete packet.
func PacketHasLBRR(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	toc, _, err := packetFrameCount(data)
	if err != nil {
		return false
	}
	firstFrameData, err := extractFirstFramePayload(data, toc)
	if err != nil {
		return false
	}
	return packetHasLBRR(firstFrameData, toc)
}

// packetHasLBRR mirrors libopus opus_packet_has_lbrr() semantics for Opus
// frame payload bytes (first frame only).
func packetHasLBRR(firstFrameData []byte, toc TOC) bool {
	if toc.Mode == ModeCELT || len(firstFrameData) == 0 {
		return false
	}

	nbFrames := 1
	if toc.FrameSize > 960 {
		nbFrames = toc.FrameSize / 960
	}

	monoBit := 7 - nbFrames
	if monoBit < 0 {
		return false
	}
	lbrr := (firstFrameData[0] >> uint(monoBit)) & 0x1

	if toc.Stereo {
		stereoBit := 6 - 2*nbFrames
		if stereoBit >= 0 {
			lbrr |= (firstFrameData[0] >> uint(stereoBit)) & 0x1
		}
	}

	return lbrr != 0
}

// storeFECData prepares the current packet's first-frame LBRR payload for one
// provided-packet decode_fec call.
func (d *Decoder) storeFECData(data []byte, toc TOC, frameCount, frameSize int) {
	if !packetHasLBRR(data, toc) {
		d.clearFECState()
		return
	}
	d.storeFECDataForDecode(data, toc, frameCount, frameSize)
}

// opus_decode_frame passes a SILK or Hybrid packet to silk_Decode with
// lostFlag=FLAG_DECODE_LBRR even when every LBRR flag is zero. SILK parses the
// packet header and then conceals the absent LBRR frame, updating its packet
// cadence and resampler state. Keep that packet available for this decode call.
func (d *Decoder) storeFECDataForDecode(data []byte, toc TOC, frameCount, frameSize int) {
	if cap(d.fecData) < len(data) {
		d.fecData = make([]byte, len(data))
	} else {
		d.fecData = d.fecData[:len(data)]
	}
	copy(d.fecData, data)

	d.fecMode = toc.Mode
	d.fecBandwidth = toc.Bandwidth
	d.fecStereo = toc.Stereo
	d.fecFrameSize = frameSize
	d.fecFrameCount = frameCount
	d.hasFEC = true
}

// decodeFECFrame decodes LBRR data from the stored FEC packet.
// This is used to recover a lost frame using forward error correction.
func (d *Decoder) decodeFECFrame(pcm []float32, requestedFrameSize int) (int, error) {
	if !d.hasFEC {
		return 0, errNoFECData
	}
	channels := int(d.channels)

	packetFrameSize := d.fecFrameSize
	if packetFrameSize <= 0 {
		packetFrameSize = int(d.lastFrameSize)
	}
	if packetFrameSize <= 0 {
		packetFrameSize = int(d.sampleRate) / 50
	}
	if packetFrameSize > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	frameSize := requestedFrameSize
	if frameSize <= 0 {
		frameSize = packetFrameSize
	}

	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if frameSize < packetFrameSize {
		d.clearFECState()
		return d.decodePLCForFEC(pcm, frameSize)
	}

	prefixSize := frameSize - packetFrameSize
	captureMode := d.fecMode
	if prefixSize > 0 && (d.prevMode == ModeSILK || d.prevMode == ModeHybrid) {
		captureMode = d.prevMode
	}
	if extsupport.DREDRuntime {
		if d.beginDREDRawMonoFrameCapture(captureMode) {
			defer d.endDREDRawMonoFrameCapture()
		}
	}

	if prefixSize > 0 {
		prefixPacketFrameSize := int(d.lastFrameSize)
		if prefixPacketFrameSize <= 0 {
			prefixPacketFrameSize = packetFrameSize
		}
		prefixState := plcDecodeState{
			packetFrameSize:    prefixPacketFrameSize,
			mode:               d.prevMode,
			bandwidth:          d.lastBandwidth,
			packetStereo:       d.prevPacketStereo,
			useDecoderPLCState: true,
		}
		n, usedNeuralConcealment, err := d.decodeNeuralPLCInto(pcm, prefixSize, prefixState, true)
		if err != nil {
			return 0, err
		}
		if !usedNeuralConcealment {
			n, err = d.decodePLCChunksInto(pcm, prefixSize, prefixState)
		}
		if err != nil {
			return 0, err
		}
		if n != prefixSize {
			return 0, ErrInvalidFrameSize
		}
	}

	fecPCM := pcm[prefixSize*channels:]

	n, err := d.decodeLBRRFrames(fecPCM, packetFrameSize)
	if err != nil {
		return 0, err
	}
	frameSize = prefixSize + n
	if extsupport.DREDRuntime {
		if d.dredGoodPacketMarkerActive() {
			if r := d.dredRecoveryState(); r != nil && d.dredNeuralModelsLoaded() {
				r.dredRecovery = 0
			}
			d.markDREDUpdatedPCM(pcm[:frameSize*channels], frameSize, d.fecMode)
		}
	}
	d.applyOutputGain(pcm[:frameSize*channels])

	// opus_decode_frame treats payloads of at most one byte as PLC, so its
	// previous mode remains the concealment mode even though opus_decode_native
	// selected the packet mode before entering the frame decoder.
	if len(d.fecData) > 1 {
		d.prevMode = d.fecMode
	}
	d.lastPacketMode = d.fecMode
	d.lastBandwidth = d.fecBandwidth
	d.bandwidthKnown = true
	d.prevPacketStereo = d.fecStereo
	d.lastFrameSize = int32(packetFrameSize)
	d.lastPacketDuration = int32(frameSize)
	d.lastDataLen = int32(len(d.fecData))
	d.prevRedundancy = false
	d.haveDecoded = true

	d.clearFECState()

	return frameSize, nil
}

func (d *Decoder) clearFECState() {
	d.hasFEC = false
	d.fecFrameSize = 0
	d.fecFrameCount = 0
	d.fecData = d.fecData[:0]
	d.fecMode = ModeHybrid
	d.fecBandwidth = BandwidthFullband
	d.fecStereo = false
}

// decodeLBRRFrames decodes LBRR (FEC) data from the stored packet.
func (d *Decoder) decodeLBRRFrames(pcm []float32, frameSize int) (int, error) {
	if len(d.fecData) <= 1 {
		state := plcDecodeState{
			packetFrameSize:    frameSize,
			mode:               d.fecMode,
			bandwidth:          d.fecBandwidth,
			packetStereo:       d.fecStereo,
			useDecoderPLCState: true,
		}
		n, usedNeuralConcealment, err := d.decodeNeuralPLCInto(pcm, frameSize, state, true)
		if err != nil || usedNeuralConcealment {
			return n, err
		}
		return d.decodePLCChunksInto(pcm, frameSize, state)
	}
	switch d.fecMode {
	case ModeSILK:
		return d.decodeSILKFEC(pcm, frameSize)
	case ModeHybrid:
		return d.decodeHybridFEC(pcm, frameSize)
	default:
		return 0, errNoFECData
	}
}

func (d *Decoder) decodeFECViaSILK(pcm []float32, frameSize int) (int, error) {
	silkBW, ok := silk.BandwidthFromOpus(int(d.fecBandwidth))
	if !ok {
		silkBW = silk.BandwidthWideband
	}
	if extsupport.OSCERuntime {
		d.installOSCELACESilkPostfilterHook(d.fecMode, silkBW, d.fecStereo)
		defer d.clearOSCELACESilkPostfilterHook()
	}
	if extsupport.DREDRuntime && d.beginDREDFECLowbandHook() {
		defer d.endHybridDREDLowbandHook()
	}
	// silk_Decode initializes channel_state[1] and resets the stereo
	// predictor/resampler when internal channel count grows from mono to stereo.
	// The regular frame path performs this setup before entering SILK; FEC can
	// arrive after a PLC prefix, so apply the transition here immediately before
	// decoding the redundant stereo frame.
	d.prepareStereoTransition(d.fecStereo, silkBW)
	stereoToMono := d.silkDecoder.ShouldUseStereoToMonoHistory(silkBW, !d.fecStereo && d.prevPacketStereo)

	needed, err := d.silkDecoder.DecodeFECInto(d.fecData, silkBW, frameSize, d.fecStereo, stereoToMono, int(d.channels), pcm)
	if err != nil {
		return 0, err
	}

	return needed, nil
}

// decodeSILKFEC decodes SILK LBRR data for FEC recovery.
func (d *Decoder) decodeSILKFEC(pcm []float32, frameSize int) (int, error) {
	n, err := d.decodeFECViaSILK(pcm, frameSize)
	if err != nil {
		return 0, err
	}
	if n != frameSize*int(d.channels) || !d.fixedCaptureSILKOutput(pcm[:n]) {
		d.markFixedUnhandled()
	}
	if err := d.applyFECHybridToSILKFade(pcm, frameSize); err != nil {
		return 0, err
	}
	d.mainDecodeRng = d.silkDecoder.FinalRange()
	d.redundantRng = 0
	return frameSize, nil
}

// applyFECHybridToSILKFade mirrors opus_decode_frame's 2.5 ms CELT silence
// accumulation when a SILK FEC frame follows a Hybrid frame. The public FEC
// path decodes SILK directly, so it does not pass through the ordinary frame
// dispatcher that applies this CELT overlap fade.
func (d *Decoder) applyFECHybridToSILKFade(pcm []float32, frameSize int) error {
	if !d.haveDecoded || d.prevMode != ModeHybrid {
		return nil
	}

	fadeSamples := int(d.sampleRate) / 400
	channels := int(d.channels)
	if fadeSamples <= 0 || fadeSamples > frameSize || len(pcm) < fadeSamples*channels {
		return ErrInvalidFrameSize
	}

	// opus_decode_frame sets CELT's end band from the packet bandwidth, its
	// stream channel count from the packet TOC, and start band to zero before
	// decoding the 0xffff silence frame with celt_accum=1.
	celtBW := celt.BandwidthFromOpusConfig(int(d.fecBandwidth))
	if !d.fixedAccumulateHybridToSILKFade(frameSize, fadeSamples, d.fecStereo, celtBW) {
		d.markFixedUnhandled()
	}
	d.celtDecoder.SetBandwidth(celtBW)
	return d.celtDecoder.AccumulateFrameWithPacketStereoAtAPIRate(
		celtSilenceFrame2B[:], fadeSamples, d.fecStereo, pcm[:fadeSamples*channels],
	)
}

// decodeHybridFEC decodes Hybrid mode LBRR data for FEC recovery.
func (d *Decoder) decodeHybridFEC(pcm []float32, frameSize int) (int, error) {
	channels := int(d.channels)
	needed, err := d.decodeFECViaSILK(pcm, frameSize)
	if err != nil {
		return 0, err
	}

	celtBW := celt.BandwidthFromOpusConfig(int(d.fecBandwidth))
	d.celtDecoder.SetBandwidth(celtBW)
	if d.haveDecoded && d.prevMode != ModeHybrid && !d.prevRedundancy {
		d.celtDecoder.Reset()
		d.celtDecoder.SetBandwidth(celtBW)
	}
	d.hybridDecoder.RecordPLCLoss()
	// libopus conceals at most a 20 ms CELT frame and accumulates it onto the
	// SILK LBRR output (celt_decode_with_ec(NULL, celt_accum=1)); samples past
	// 20 ms keep the SILK output alone. libopus src/opus_decoder.c:304,601 uses
	// F20=Fs/50 and passes the active CELT rate to PLC, so native 96 kHz CELT
	// receives a 96 kHz frame size. The fixed-point bridge below continues to
	// receive the equivalent 48 kHz frame size.
	celtFrameSize := min(d.frameSize48FromAPI(frameSize), 48000/50)
	celtAPIFrames := min(frameSize, celtFrameSize*int(d.sampleRate)/48000)
	if !d.fixedDecodeHybridFEC(pcm[:needed], frameSize, celtFrameSize, celtBW) {
		d.markFixedUnhandled()
	}
	celtPLCFrameSize := celtFrameSize
	if d.is96kHz() {
		celtPLCFrameSize = celtAPIFrames
	}
	if err := d.celtDecoder.DecodeHybridFECPLC(celtPLCFrameSize, pcm[:min(needed, celtAPIFrames*channels)]); err != nil {
		return 0, err
	}
	d.mainDecodeRng = d.celtDecoder.FinalRange()
	d.redundantRng = 0

	return frameSize, nil
}
