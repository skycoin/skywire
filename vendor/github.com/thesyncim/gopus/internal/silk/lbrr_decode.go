// Package silk implements LBRR (Low Bitrate Redundancy) decoding for FEC.
// LBRR provides forward error correction by including redundant data
// for the previous frame at a lower quality in the current packet.
//
// Reference: libopus silk/decode_frame.c, silk/dec_API.c

package silk

import (
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// preparePacketRangeDecoder initializes a range decoder over a SILK packet and
// resolves the packet's frame layout (frames per packet, subframes per frame)
// from the requested API-rate frame size. Shared setup for the FEC entry points.
func preparePacketRangeDecoder(data []byte, frameSizeSamples, sampleRate int) (rangecoding.Decoder, int, int, error) {
	var rd rangecoding.Decoder
	framesPerPacket, nbSubfr, err := preparePacketRangeDecoderInto(&rd, data, frameSizeSamples, sampleRate)
	return rd, framesPerPacket, nbSubfr, err
}

func preparePacketRangeDecoderInto(rd *rangecoding.Decoder, data []byte, frameSizeSamples, sampleRate int) (int, int, error) {
	rd.Init(data)
	duration := FrameDurationFromSamples(frameSizeSamples, sampleRate)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, 0, err
	}
	return framesPerPacket, nbSubfr, nil
}

// DecodeFEC decodes LBRR (Low Bitrate Redundancy) frames for Forward Error Correction.
// This function decodes the FEC data from a packet to recover a lost frame.
//
// Parameters:
//   - data: The Opus packet data containing LBRR
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - frameSizeSamples: Expected frame size in samples at the decoder API rate
//   - stereo: Whether the packet contains stereo data
//   - outputChannels: Number of output channels (1 or 2)
//
// Returns decoded samples at the decoder API rate, or an error if no LBRR data available.
//
// Reference: libopus silk/dec_API.c silk_Decode with lostFlag=FLAG_DECODE_LBRR
func (d *Decoder) DecodeFEC(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	stereo bool,
	stereoToMono bool,
	outputChannels int,
) ([]float32, error) {
	if !d.validFECOutputSize(frameSizeSamples, outputChannels) {
		return nil, ErrDecodeFailed
	}
	output := make([]float32, frameSizeSamples*outputChannels)
	n, err := d.DecodeFECInto(data, bandwidth, frameSizeSamples, stereo, stereoToMono, outputChannels, output)
	if err != nil {
		return nil, err
	}
	return output[:n], nil
}

// DecodeFECInto writes the SILK LBRR output into caller-owned interleaved PCM.
// Its returned count is the number of written samples across all channels.
func (d *Decoder) DecodeFECInto(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	stereo bool,
	stereoToMono bool,
	outputChannels int,
	output []float32,
) (int, error) {
	if len(data) == 0 {
		return 0, ErrDecodeFailed
	}
	if !d.validFECOutputSize(frameSizeSamples, outputChannels) || len(output) < frameSizeSamples*outputChannels {
		return 0, ErrDecodeFailed
	}

	// Keep SILK bandwidth/resampler transition cadence aligned with normal decode.
	d.NotifyBandwidthChange(bandwidth)

	rd := &d.fecRangeDecoder
	framesPerPacket, nbSubfr, err := preparePacketRangeDecoderInto(rd, data, frameSizeSamples, d.outputSampleRate())
	if err != nil {
		return 0, err
	}
	d.SetRangeDecoder(rd)

	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000

	// Set up decoder state for FEC decoding
	stMid := &d.state[0]
	initFrameDecodeState(stMid, fsKHz, framesPerPacket, nbSubfr)

	if stereo {
		stSide := &d.state[1]
		initFrameDecodeState(stSide, fsKHz, framesPerPacket, nbSubfr)
		// libopus order: both channels' VAD + LBRR-present flags, then both
		// channels' per-frame LBRR flags symbol (see decodeVADFlagsAndLBRRFlag).
		decodeVADFlagsAndLBRRFlag(rd, stMid, framesPerPacket)
		decodeVADFlagsAndLBRRFlag(rd, stSide, framesPerPacket)
		decodeLBRRFlagsSymbol(rd, stMid, framesPerPacket)
		decodeLBRRFlagsSymbol(rd, stSide, framesPerPacket)
		return d.decodeStereoFECFrames(rd, stMid, stSide, bandwidth, framesPerPacket, frameSizeSamples, outputChannels, output)
	}

	// Decode VAD and LBRR flags
	decodeVADAndLBRRFlags(rd, stMid, framesPerPacket)

	// Decode FEC/LBRR frames. Match libopus decode_fec cadence:
	// if a packet frame has no LBRR, decode that frame as loss concealment.
	frameLength := int(stMid.frameLength)
	totalLen := framesPerPacket * frameLength

	// The FEC output accumulator must not alias scratchOutInt16: concealed
	// sub-frames (LBRR_flags[i]==0) run recordPLCLossForState, which writes its
	// int16 concealment + per-frame state updates through scratchOutInt16. If the
	// accumulator shared that buffer, concealing a later sub-frame would clobber
	// the already-decoded LBRR output of earlier sub-frames in the same packet.
	outInt16 := d.fecOutputBuffer(totalLen)
	clear(outInt16)
	lastFrameLost := false

	// Decode each frame using LBRR when present, otherwise run PLC.
	for i := range framesPerPacket {
		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		if stMid.LBRRFlags[i] == 0 {
			d.decodeFECLostFrameInto(0, stMid, frameOut)
			d.syncLegacyPLCState(stMid, frameOut)
			stMid.nFramesDecoded++
			lastFrameLost = true
			continue
		}

		d.decodeLBRRFrameInto(0, stMid, rd, i, frameOut, true)
		d.syncLegacyPLCState(stMid, frameOut)
		lastFrameLost = false
	}

	// Resample from native rate to 48kHz using the same int16 path as normal decode.
	resampler := d.GetResampler(bandwidth)
	outputOffset := 0
	if outputChannels == 2 && stereoToMono {
		// libopus silk/dec_API.c resamples the mono frame through channel 1's
		// retained state when stereo_to_mono is set. This preserves the right
		// channel's filter history from the preceding coded stereo frames.
		rightResampler := d.GetResamplerForChannel(bandwidth, 1)
		leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
		if !ok {
			return 0, ErrDecodeFailed
		}
		for f := range framesPerPacket {
			start := f * frameLength
			end := min(start+frameLength, len(outInt16))
			frameNative := outInt16[start:end]
			resamplerInput := d.BuildMonoResamplerInputInt16(frameNative)
			nLeft := resampler.ProcessInt16Into(resamplerInput, leftScratch)
			n := nLeft
			if f == 0 {
				// opus_decode_frame calls silk_Decode once per 20 ms SILK chunk.
				// Its first transition call sees nChannelsInternal==2; the call
				// then stores 1, so later chunks duplicate the left result.
				nRight := rightResampler.ProcessInt16Into(resamplerInput, rightScratch)
				if nRight < n {
					n = nRight
				}
			}
			if n < 0 || (outputOffset+n)*2 > len(output) {
				return 0, ErrDecodeFailed
			}
			for i := range n {
				output[(outputOffset+i)*2] = leftScratch[i]
				right := leftScratch[i]
				if f == 0 {
					right = rightScratch[i]
				}
				output[(outputOffset+i)*2+1] = right
			}
			outputOffset += n
		}
		outputOffset *= 2
	} else {
		for f := range framesPerPacket {
			start := f * frameLength
			end := min(start+frameLength, len(outInt16))
			frameNative := outInt16[start:end]

			// Apply sMid buffering before resampling
			resamplerInput := d.BuildMonoResamplerInputInt16(frameNative)
			n := resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
			outputOffset += n
		}
	}
	// Expand mono output when C reuses the left-channel resampler state.
	if outputChannels == 2 && !stereo && !stereoToMono {
		// Expand backward so the caller buffer is safe to reuse in place.
		for i := outputOffset - 1; i >= 0; i-- {
			s := output[i]
			output[i*2] = s
			output[i*2+1] = s
		}
		outputOffset *= 2
	}

	// Match libopus decode_fec cadence:
	// - if the recovered frame decoded from LBRR, clear PLC loss accumulator
	// - if no LBRR was present, keep loss cadence from concealment path
	if d.plcState != nil {
		if !lastFrameLost {
			d.plcState.Reset()
			d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, outputChannels)
		}
	}

	d.haveDecoded = true
	return outputOffset, nil
}

func (d *Decoder) validFECOutputSize(frameSizeSamples, outputChannels int) bool {
	if outputChannels < 1 || outputChannels > 2 || frameSizeSamples <= 0 ||
		frameSizeSamples > int(^uint(0)>>1)/outputChannels {
		return false
	}
	rate := d.outputSampleRate()
	return frameSizeSamples == rate/100 || frameSizeSamples == rate/50 ||
		frameSizeSamples == rate/25 || frameSizeSamples == rate*3/50
}

// decodeStereoFECFrames recovers a stereo packet's frames from LBRR data: per
// frame it decodes the stereo prediction (when present), decodes each channel's
// LBRR frame or runs concealment when that channel has no LBRR, unmixes mid/side
// to left/right, and resamples to the API rate. Mirrors the stereo LBRR path of
// libopus silk/dec_API.c silk_Decode (lostFlag = FLAG_DECODE_LBRR).
func (d *Decoder) decodeStereoFECFrames(
	rd *rangecoding.Decoder,
	stMid, stSide *decoderState,
	bandwidth Bandwidth,
	framesPerPacket, frameSizeSamples, outputChannels int,
	output []float32,
) (int, error) {
	if rd == nil || stMid == nil || stSide == nil || framesPerPacket <= 0 {
		return 0, ErrDecodeFailed
	}
	frameLength := int(stMid.frameLength)
	totalLen := framesPerPacket * frameLength
	if frameLength <= 0 || totalLen <= 0 {
		return 0, ErrDecodeFailed
	}

	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	if stMid.fsKHz > 0 {
		fsKHz = int(stMid.fsKHz)
	}

	leftNative, rightNative, ok := d.GetStereoInt16Scratch(totalLen)
	if !ok {
		return 0, ErrDecodeFailed
	}
	lastFrameLost := false

	for i := range framesPerPacket {
		frameIndex := int(stMid.nFramesDecoded)
		if frameIndex < 0 || frameIndex >= maxFramesPerPacket {
			return 0, ErrDecodeFailed
		}

		var predQ13 [2]int32
		decodeOnlyMiddle := 0
		if stMid.LBRRFlags[frameIndex] != 0 {
			silkStereoDecodePred(rd, predQ13[:])
			if stSide.LBRRFlags[frameIndex] == 0 {
				decodeOnlyMiddle = silkStereoDecodeMidOnly(rd)
			}
		} else {
			predQ13 = [2]int32{int32(d.stereo.predPrevQ13[0]), int32(d.stereo.predPrevQ13[1])}
		}
		d.maybeResetStereoSideChannel(decodeOnlyMiddle, stSide)

		hasSide := d.prevDecodeOnlyMiddle == 0 || stSide.LBRRFlags[frameIndex] != 0
		midFrame, sideFrame, ok := d.stereoFrameScratch(frameLength)
		if !ok {
			return 0, ErrDecodeFailed
		}
		clear(midFrame)
		clear(sideFrame)
		midOut := midFrame[2:]
		sideOut := sideFrame[2:]

		midRecovered := stMid.LBRRFlags[frameIndex] != 0
		if midRecovered {
			d.decodeLBRRFrameInto(0, stMid, rd, frameIndex, midOut, true)
		} else {
			d.decodeFECLostFrameInto(0, stMid, midOut)
			d.syncLegacyPLCState(stMid, midOut)
			stMid.nFramesDecoded++
		}

		if hasSide {
			sideFrameIndex := int(stSide.nFramesDecoded)
			if sideFrameIndex < 0 || sideFrameIndex >= maxFramesPerPacket {
				return 0, ErrDecodeFailed
			}
			if stSide.LBRRFlags[sideFrameIndex] != 0 {
				d.decodeLBRRFrameInto(1, stSide, rd, sideFrameIndex, sideOut, true)
			} else {
				d.decodeFECLostFrameInto(1, stSide, sideOut)
				stSide.nFramesDecoded++
			}
		} else {
			clear(sideOut)
			stSide.nFramesDecoded++
		}

		start := i * frameLength
		if outputChannels == 2 {
			silkStereoMSToLR(&d.stereo, midFrame, sideFrame, predQ13[:], fsKHz, frameLength)
			copy(leftNative[start:start+frameLength], midFrame[1:frameLength+1])
			copy(rightNative[start:start+frameLength], sideFrame[1:frameLength+1])
		} else {
			copy(leftNative[start:start+frameLength], midOut[:frameLength])
		}
		d.prevDecodeOnlyMiddle = int32(decodeOnlyMiddle)
		lastFrameLost = !midRecovered
	}

	outputLen := 0
	if outputChannels == 2 {
		leftResampler := d.GetResamplerForChannel(bandwidth, 0)
		rightResampler := d.GetResamplerForChannel(bandwidth, 1)
		leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
		if !ok {
			return 0, ErrDecodeFailed
		}
		outputSamples := 0
		for f := range framesPerPacket {
			start := f * frameLength
			end := start + frameLength
			if frameLength <= 0 || end > totalLen {
				return 0, ErrDecodeFailed
			}
			nLeft := leftResampler.ProcessInt16Into(leftNative[start:end], leftScratch[outputSamples:])
			nRight := rightResampler.ProcessInt16Into(rightNative[start:end], rightScratch[outputSamples:])
			n := min(nRight, nLeft)
			if n < 0 || (outputSamples+n)*2 > len(output) {
				return 0, ErrDecodeFailed
			}
			interleaveStereoFloat32(output[outputSamples*2:(outputSamples+n)*2], leftScratch[outputSamples:outputSamples+n], rightScratch[outputSamples:outputSamples+n])
			outputSamples += n
		}
		outputLen = outputSamples * 2
	} else {
		resampler := d.GetResampler(bandwidth)
		outputOffset := 0
		for f := range framesPerPacket {
			start := f * frameLength
			end := start + frameLength
			resamplerInput := d.BuildMonoResamplerInputInt16(leftNative[start:end])
			outputOffset += resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
		}
		outputLen = outputOffset
	}

	if d.plcState != nil && !lastFrameLost {
		d.plcState.Reset()
		d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, outputChannels)
	}

	d.haveDecoded = true
	return outputLen, nil
}

// decodeFECLostFrameInto is silk_decode_frame() for a frame of an FEC packet
// whose channel carries no LBRR data (lostFlag == FLAG_DECODE_LBRR with
// LBRR_flags[nFramesDecoded] == 0): silk_PLC(lost=1) conceals the frame, then
// the output buffer, comfort noise, PLC glue and lagPrev updates follow as for
// any concealed frame.
func (d *Decoder) decodeFECLostFrameInto(channel int, st *decoderState, frameOut []int16) {
	if st == nil || len(frameOut) == 0 {
		return
	}
	copy(frameOut, d.concealSILKFrame(channel, st, len(frameOut)))
	usedDeepPLC, deepPLCLagPrev := d.replaceFECLossWithDeepPLC(channel, st, frameOut)
	if !usedDeepPLC {
		d.fireRawMonoLossFrameHook(channel, st, frameOut)
	}
	d.finishLostFrame(channel, st, frameOut)
	if usedDeepPLC {
		st.plcSkipRecoveryGlue = true
	}
	if deepPLCLagPrev > 0 {
		st.lagPrev = int32(deepPLCLagPrev)
	} else {
		st.lagPrev = d.concealLagPrev(channel)
	}
}

// replaceFECLossWithDeepPLC mirrors the ENABLE_DEEP_PLC branch in
// silk/PLC.c:silk_PLC_conceal for a missing LBRR frame. It replaces only the
// mono 16 kHz output before silk_decode_frame runs CNG and PLC glue.
func (d *Decoder) replaceFECLossWithDeepPLC(channel int, st *decoderState, frame []int16) (bool, int) {
	if !dredHooksEnabled || channel != 0 || st == nil || st.fsKHz != 16 ||
		len(frame) == 0 || len(frame) > len(d.scratchOutput) || !d.hasDeepPLCLossMonoHook() {
		return false, 0
	}
	concealed := d.scratchOutput[:len(frame)]
	const scale = float32(1.0 / 32768.0)
	for i, sample := range frame {
		concealed[i] = float32(sample) * scale
	}
	used, lagPrev := d.fireDeepPLCLossMonoHook(concealed)
	if !used {
		return false, 0
	}
	for i, sample := range concealed {
		frame[i] = float32ToInt16(sample)
	}
	d.applyDeepPLCHistoryMono(st, concealed)
	return true, lagPrev
}

// HasLBRR checks if the given packet contains LBRR (FEC) data.
// This can be used to check if FEC recovery is possible before attempting it.
func (d *Decoder) HasLBRR(data []byte, bandwidth Bandwidth, frameSizeSamples int) bool {
	if len(data) == 0 {
		return false
	}

	rd, framesPerPacket, _, err := preparePacketRangeDecoder(data, frameSizeSamples, d.outputSampleRate())
	if err != nil {
		return false
	}

	// Decode VAD and LBRR flags
	st := &decoderState{}
	decodeVADAndLBRRFlags(&rd, st, framesPerPacket)

	return st.LBRRFlag != 0
}
