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
	rd.Init(data)

	duration := FrameDurationFromSamples(frameSizeSamples, sampleRate)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return rangecoding.Decoder{}, 0, 0, err
	}

	return rd, framesPerPacket, nbSubfr, nil
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
	outputChannels int,
) ([]float32, error) {
	if len(data) == 0 {
		return nil, ErrDecodeFailed
	}

	// Keep SILK bandwidth/resampler transition cadence aligned with normal decode.
	d.NotifyBandwidthChange(bandwidth)

	rd, framesPerPacket, nbSubfr, err := preparePacketRangeDecoder(data, frameSizeSamples, d.outputSampleRate())
	if err != nil {
		return nil, err
	}
	d.SetRangeDecoder(&rd)

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
		decodeVADFlagsAndLBRRFlag(&rd, stMid, framesPerPacket)
		decodeVADFlagsAndLBRRFlag(&rd, stSide, framesPerPacket)
		decodeLBRRFlagsSymbol(&rd, stMid, framesPerPacket)
		decodeLBRRFlagsSymbol(&rd, stSide, framesPerPacket)
		return d.decodeStereoFECFrames(&rd, stMid, stSide, bandwidth, framesPerPacket, frameSizeSamples, outputChannels)
	}

	// Decode VAD and LBRR flags
	decodeVADAndLBRRFlags(&rd, stMid, framesPerPacket)

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

		d.decodeLBRRFrameInto(0, stMid, &rd, i, frameOut, true)
		d.syncLegacyPLCState(stMid, frameOut)
		lastFrameLost = false
	}

	// Resample from native rate to 48kHz using the same int16 path as normal decode.
	resampler := d.GetResampler(bandwidth)
	output := make([]float32, frameSizeSamples*outputChannels)
	outputOffset := 0

	for f := range framesPerPacket {
		start := f * frameLength
		end := min(start+frameLength, len(outInt16))
		frameNative := outInt16[start:end]

		// Apply sMid buffering before resampling
		resamplerInput := d.BuildMonoResamplerInputInt16(frameNative)
		n := resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
		outputOffset += n
	}
	output = output[:outputOffset]

	// Handle channel expansion/reduction
	if outputChannels == 2 && !stereo {
		// Mono to stereo: duplicate samples
		stereoOutput := make([]float32, len(output)*2)
		for i, s := range output {
			stereoOutput[i*2] = s
			stereoOutput[i*2+1] = s
		}
		output = stereoOutput
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
	return output, nil
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
) ([]float32, error) {
	if rd == nil || stMid == nil || stSide == nil || framesPerPacket <= 0 {
		return nil, ErrDecodeFailed
	}
	frameLength := int(stMid.frameLength)
	totalLen := framesPerPacket * frameLength
	if frameLength <= 0 || totalLen <= 0 {
		return nil, ErrDecodeFailed
	}

	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	if stMid.fsKHz > 0 {
		fsKHz = int(stMid.fsKHz)
	}

	leftNative, rightNative, ok := d.GetStereoInt16Scratch(totalLen)
	if !ok {
		return nil, ErrDecodeFailed
	}
	lastFrameLost := false

	for i := range framesPerPacket {
		frameIndex := int(stMid.nFramesDecoded)
		if frameIndex < 0 || frameIndex >= maxFramesPerPacket {
			return nil, ErrDecodeFailed
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
			return nil, ErrDecodeFailed
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
				return nil, ErrDecodeFailed
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

	var output []float32
	if outputChannels == 2 {
		leftResampler := d.GetResamplerForChannel(bandwidth, 0)
		rightResampler := d.GetResamplerForChannel(bandwidth, 1)
		leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
		if !ok {
			return nil, ErrDecodeFailed
		}
		nLeft := leftResampler.ProcessInt16Into(leftNative[:totalLen], leftScratch)
		nRight := rightResampler.ProcessInt16Into(rightNative[:totalLen], rightScratch)
		n := min(nRight, nLeft)
		if n < 0 {
			return nil, ErrDecodeFailed
		}
		output = make([]float32, n*2)
		for i := range n {
			output[i*2] = leftScratch[i]
			output[i*2+1] = rightScratch[i]
		}
	} else {
		resampler := d.GetResampler(bandwidth)
		output = make([]float32, frameSizeSamples)
		outputOffset := 0
		for f := range framesPerPacket {
			start := f * frameLength
			end := start + frameLength
			resamplerInput := d.BuildMonoResamplerInputInt16(leftNative[start:end])
			outputOffset += resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
		}
		output = output[:outputOffset]
	}

	if d.plcState != nil && !lastFrameLost {
		d.plcState.Reset()
		d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, outputChannels)
	}

	d.haveDecoded = true
	return output, nil
}

// decodeFECLostFrameInto fills frameOut with packet-loss concealment for a frame
// that has no LBRR data inside an FEC packet, keeping the decoder's loss cadence
// aligned with libopus decode_fec (it runs the normal PLC concealment for that
// sub-frame instead of decoding redundant data).
func (d *Decoder) decodeFECLostFrameInto(channel int, st *decoderState, frameOut []int16) {
	if st == nil || len(frameOut) == 0 {
		return
	}

	frameLength := len(frameOut)
	fadeFactor := float32(1.0)
	if d.plcState != nil {
		fadeFactor = d.plcState.RecordLoss()
	}

	lossCnt := st.lossCnt
	var concealed []float32
	if d.scratchOutput != nil && len(d.scratchOutput) >= frameLength {
		concealed = d.scratchOutput[:frameLength]
		clear(concealed)
	} else {
		concealed = make([]float32, frameLength)
	}

	if state := d.ensureSILKPLCState(channel); state != nil && st.nbSubfr > 0 {
		view := d.plcDecoderView(channel)
		if view == nil {
			return
		}
		concealedQ0 := plc.ConcealSILKWithLTP(view, state, int(lossCnt), frameLength)
		const scale = float32(1.0 / 32768.0)
		n := min(len(concealedQ0), frameLength)
		for i := range n {
			concealed[i] = float32(concealedQ0[i]) * scale
		}
		if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
			st.lagPrev = int32(lag)
		}
	} else {
		plcOut := plc.ConcealSILK(d, frameLength, fadeFactor)
		copy(concealed, plcOut)
		if len(plcOut) < frameLength {
			clear(concealed[len(plcOut):])
		}
	}

	d.recordPLCLossForState(st, concealed)
	for i := range frameOut {
		frameOut[i] = float32ToInt16(concealed[i])
	}
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
