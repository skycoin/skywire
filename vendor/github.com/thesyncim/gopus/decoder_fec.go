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
	return d.decodePLCForFECWithState(pcm, frameSize, packetFrameSize, d.prevMode, d.lastBandwidth, d.prevPacketStereo)
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
	neuralReady := extsupport.DREDRuntime && d.dredNeuralConcealmentAvailable()
	usedNeuralConcealment := false
	var n int
	var err error
	if neuralReady && mode == ModeSILK && channels >= 1 && channels <= 2 {
		n, usedNeuralConcealment, err = d.decodeSILKNeuralPLCInto(pcm, frameSize, plcDecodeState{
			packetFrameSize:    packetFrameSize,
			mode:               mode,
			bandwidth:          bandwidth,
			packetStereo:       packetStereo,
			useDecoderPLCState: false,
		})
		if err != nil {
			return 0, err
		}
	}
	// libopus opus_decode(NULL,...) / decode_fec PLC fallback passes dred==NULL,
	// so the cached-DRED FEC-feature feed gated on
	// `dred != NULL && process_stage == 2` (opus_decoder.c:736) is skipped and
	// a public packet-loss decode runs PLAIN PLC, consuming no cached DRED.
	// Mirror that here for CELT/hybrid: do NOT auto-apply cached DRED on the
	// public FEC-fallback PLC path. DRED is only applied through the explicit
	// DRED-decode path. Matches the SILK reconciliation in cc04ecf0.
	if !usedNeuralConcealment {
		n, err = d.decodePLCChunksInto(pcm, frameSize, plcDecodeState{
			packetFrameSize:    packetFrameSize,
			mode:               mode,
			bandwidth:          bandwidth,
			packetStereo:       packetStereo,
			useDecoderPLCState: false,
		})
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

func (d *Decoder) decodeNoLBRRFECFallback(
	pcm []float32,
	requestedFrameSize int,
	packetFrameSize int,
	mode Mode,
	bandwidth Bandwidth,
	packetStereo bool,
) (int, error) {
	if packetFrameSize <= 0 {
		packetFrameSize = int(d.lastFrameSize)
	}
	if packetFrameSize <= 0 {
		packetFrameSize = int(d.sampleRate) / 50
	}
	if requestedFrameSize <= packetFrameSize {
		return d.decodePLCForFECWithState(pcm, requestedFrameSize, packetFrameSize, mode, bandwidth, packetStereo)
	}
	channels := int(d.channels)
	needed := requestedFrameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	prefixSize := requestedFrameSize - packetFrameSize
	prefixPacketFrameSize := int(d.lastFrameSize)
	if prefixPacketFrameSize <= 0 {
		prefixPacketFrameSize = packetFrameSize
	}
	n, err := d.decodePLCChunksInto(pcm, prefixSize, plcDecodeState{
		packetFrameSize:    prefixPacketFrameSize,
		mode:               d.prevMode,
		bandwidth:          d.lastBandwidth,
		packetStereo:       d.prevPacketStereo,
		useDecoderPLCState: true,
	})
	if err != nil {
		return 0, err
	}
	if n != prefixSize {
		return 0, ErrInvalidFrameSize
	}
	if d.decodeGainQ8 != 0 {
		d.applyOutputGain(pcm[:prefixSize*channels])
	}

	suffix := pcm[prefixSize*channels : requestedFrameSize*channels]
	n, err = d.decodePLCForFECWithState(suffix, packetFrameSize, packetFrameSize, mode, bandwidth, packetStereo)
	if err != nil {
		return 0, err
	}
	if n != packetFrameSize {
		return 0, ErrInvalidFrameSize
	}
	d.lastPacketDuration = int32(requestedFrameSize)
	return requestedFrameSize, nil
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

// PacketHasLBRR reports whether an Opus packet carries in-band LBRR data for
// FEC recovery. It mirrors libopus opus_packet_has_lbrr().
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
	if !d.hasFEC || len(d.fecData) == 0 {
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
	if prefixSize > 0 {
		prefixPacketFrameSize := int(d.lastFrameSize)
		if prefixPacketFrameSize <= 0 {
			prefixPacketFrameSize = packetFrameSize
		}
		n, err := d.decodePLCChunksInto(pcm, prefixSize, plcDecodeState{
			packetFrameSize:    prefixPacketFrameSize,
			mode:               d.prevMode,
			bandwidth:          d.lastBandwidth,
			packetStereo:       d.prevPacketStereo,
			useDecoderPLCState: true,
		})
		if err != nil {
			return 0, err
		}
		if n != prefixSize {
			return 0, ErrInvalidFrameSize
		}
	}

	fecPCM := pcm[prefixSize*channels:]
	if extsupport.DREDRuntime {
		if endRawDREDCapture := d.beginDREDRawMonoGoodFrameCapture(d.fecMode); endRawDREDCapture != nil {
			defer endRawDREDCapture()
		}
	}

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

	d.prevMode = d.fecMode
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

	fecSamples, err := d.silkDecoder.DecodeFEC(d.fecData, silkBW, frameSize, d.fecStereo, int(d.channels))
	if err != nil {
		return 0, err
	}

	needed := len(fecSamples)
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	copy(pcm[:needed], fecSamples)

	return needed, nil
}

// decodeSILKFEC decodes SILK LBRR data for FEC recovery.
func (d *Decoder) decodeSILKFEC(pcm []float32, frameSize int) (int, error) {
	if _, err := d.decodeFECViaSILK(pcm, frameSize); err != nil {
		return 0, err
	}
	d.mainDecodeRng = d.silkDecoder.FinalRange()
	d.redundantRng = 0
	return frameSize, nil
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
	celtFrameSize := d.frameSize48FromAPI(frameSize)
	celtSamples, err := d.celtDecoder.DecodeHybridFECPLC(min(celtFrameSize, 48000/50))
	if err != nil {
		return 0, err
	}

	neededAPI := frameSize * channels
	if len(d.scratchRedundant) < neededAPI {
		d.scratchRedundant = make([]float32, neededAPI)
	}
	celtAPI := d.scratchRedundant[:neededAPI]
	if d.sampleRate == 48000 {
		for i := range celtAPI {
			if i < len(celtSamples) {
				celtAPI[i] = celtSamples[i]
			} else {
				celtAPI[i] = 0
			}
		}
	} else {
		needed48 := celtFrameSize * channels
		if len(d.scratchFrame48) < needed48 {
			return 0, ErrBufferTooSmall
		}
		celt48 := d.scratchFrame48[:needed48]
		for i := range celt48 {
			if i < len(celtSamples) {
				celt48[i] = celtSamples[i]
			} else {
				celt48[i] = 0
			}
		}
		d.downsampleFrame48ToAPI(celtAPI, celt48, frameSize)
	}
	limit := min(needed, frameSize*channels)
	for i := 0; i < limit; i++ {
		pcm[i] += celtAPI[i]
	}
	d.mainDecodeRng = d.celtDecoder.FinalRange()
	d.redundantRng = 0

	return frameSize, nil
}
