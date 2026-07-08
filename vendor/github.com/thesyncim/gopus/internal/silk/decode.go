package silk

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeFrame decodes a single SILK mono frame from the bitstream.
// Returns decoded samples at native SILK sample rate (8/12/16kHz).
//
// The output includes libopus-compatible delay compensation:
// - 1 sample from sMid history buffer prepended before resampler
// - N samples of resampler input delay (varies by sample rate)
// This matches libopus's exact output timing for test vector compliance.
//
// Parameters:
//   - rd: Range decoder initialized with the SILK bitstream
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - duration: Frame duration (10/20/40/60ms)
//   - vadFlag: Voice Activity Detection flag from header
//
// For 40/60ms frames, the frame is decoded as multiple 20ms sub-blocks.
func (d *Decoder) DecodeFrame(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]float32, error) {
	_ = vadFlag
	st, framesPerPacket, fsKHz, err := d.prepareMonoFramePacket(rd, bandwidth, duration)
	if err != nil {
		return nil, err
	}

	frameLength := int(st.frameLength)
	totalLen := framesPerPacket * frameLength
	outInt16 := d.int16OutputBuffer(totalLen)

	for i := range framesPerPacket {
		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		frameIndex := int(st.nFramesDecoded)
		ctrl := d.decodeFrameCoreInto(st, rd, frameOut, frameCondCoding(frameIndex), st.VADFlags[frameIndex] != 0)
		d.finalizeDecodedChannelFrame(0, st, &ctrl, frameOut, false)
	}

	// Apply libopus-compatible mono delay compensation.
	// This matches the delay introduced by:
	// 1. sMid[1] prepended before resampler input (1 sample)
	// 2. Resampler input delay from delay_matrix_dec (varies by rate)
	delayedInt16 := d.applyMonoDelay(outInt16, fsKHz)

	output := d.float32OutputBuffer(len(delayedInt16))
	for i, v := range delayedInt16 {
		output[i] = float32(v) / 32768.0
	}

	// Mono decode resets mid-only tracking (libopus sets decode_only_middle=0).
	d.prevDecodeOnlyMiddle = 0
	d.haveDecoded = true
	return output, nil
}

// DecodeFrameRaw decodes a single SILK mono frame from the bitstream.
// Returns decoded samples at native SILK sample rate (8/12/16kHz) as float32.
//
// Unlike DecodeFrame, this does NOT apply libopus-compatible delay compensation.
// The caller is responsible for handling sMid buffering before resampling.
// This is used by Decode, DecodeWithDecoder, and the Hybrid decoder.
//
// Parameters:
//   - rd: Range decoder initialized with the SILK bitstream
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - duration: Frame duration (10/20/40/60ms)
//   - vadFlag: Voice Activity Detection flag from header
//
// For 40/60ms frames, the frame is decoded as multiple 20ms sub-blocks.
func (d *Decoder) DecodeFrameRaw(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]float32, error) {
	outInt16, err := d.decodeFrameRawInt16(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Convert to float32 WITHOUT delay compensation.
	// The caller handles sMid buffering via BuildMonoResamplerInput.
	var output []float32
	if d.scratchOutput != nil && len(d.scratchOutput) >= len(outInt16) {
		output = d.scratchOutput[:len(outInt16)]
	} else {
		output = make([]float32, len(outInt16))
	}
	for i, v := range outInt16 {
		output[i] = float32(v) / 32768.0
	}
	return output, nil
}

// DecodeFrameRawInt16 decodes a single SILK mono frame at native SILK sample rate as int16.
// This is an int16-native variant used by hot paths that resample immediately.
func (d *Decoder) DecodeFrameRawInt16(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]int16, error) {
	return d.decodeFrameRawInt16(rd, bandwidth, duration, vadFlag)
}

// decodeFrameRawInt16 is the int16-native DecodeFrameRaw path used by decoder hot paths.
// It performs the same decode steps as DecodeFrameRaw and returns native-rate int16 samples.
func (d *Decoder) decodeFrameRawInt16(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]int16, error) {
	_ = vadFlag
	st, framesPerPacket, fsKHz, err := d.prepareMonoFramePacket(rd, bandwidth, duration)
	if err != nil {
		return nil, err
	}

	frameLength := int(st.frameLength)
	totalLen := framesPerPacket * frameLength
	outInt16 := d.int16OutputBuffer(totalLen)

	for i := range framesPerPacket {
		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		frameIndex := int(st.nFramesDecoded)
		ctrl := d.decodeFrameCoreInto(st, rd, frameOut, frameCondCoding(frameIndex), st.VADFlags[frameIndex] != 0)
		d.finalizeDecodedChannelFrame(0, st, &ctrl, frameOut, true)
	}

	// Mono decode resets mid-only tracking (libopus sets decode_only_middle=0).
	d.prevDecodeOnlyMiddle = 0
	d.haveDecoded = true

	// Record native-rate length/fs so decoder-side post-processing (e.g. the
	// optional OSCE BWE forward pass) can read the pre-resample lowband signal
	// without performing a second decode pass. Mirrors libopus where BWE
	// consumes samplesOut1_tmp directly.
	if nativeLowbandCaptureEnabled {
		d.lastNativeMonoLen = int32(totalLen)
		d.lastNativeMonoFsKHz = int32(fsKHz)
		d.lastNativeStereoLen = 0
		d.lastNativeStereoFsKHz = 0
		d.lastNativeMidLen = 0
		d.lastNativeMidFsKHz = 0
	}
	return outInt16, nil
}

// DecodeStereoFrameToMono decodes a stereo SILK frame and returns the mid channel
// at native sample rate. The side channel is still decoded to keep the bitstream aligned.
func (d *Decoder) DecodeStereoFrameToMono(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]float32, error) {
	midNative, _, err := d.decodeStereoMidNative(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}
	mid := make([]float32, len(midNative))
	for i, v := range midNative {
		mid[i] = float32(v) / 32768.0
	}
	return mid, nil
}

// DecodeStereoFrame decodes a SILK stereo frame from the bitstream.
// Returns left and right channel samples at native sample rate.
//
// Stereo SILK uses mid-side coding with prediction.
// The mid channel is decoded first, then the side channel,
// and finally they are unmixed to left and right.
func (d *Decoder) DecodeStereoFrame(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) (left, right []float32, err error) {
	_ = vadFlag
	stMid, stSide, framesPerPacket, frameLength, fsKHz, err := d.prepareStereoFramePacket(rd, bandwidth, duration)
	if err != nil {
		return nil, nil, err
	}

	leftNative := make([]int16, framesPerPacket*frameLength)
	rightNative := make([]int16, framesPerPacket*frameLength)
	var predQ13 [2]int32
	decodeOnlyMiddle := 0

	for i := range framesPerPacket {
		frameIndex := int(stMid.nFramesDecoded)
		silkStereoDecodePred(rd, predQ13[:])
		if stSide.VADFlags[frameIndex] == 0 {
			decodeOnlyMiddle = silkStereoDecodeMidOnly(rd)
		} else {
			decodeOnlyMiddle = 0
		}
		d.maybeResetStereoSideChannel(decodeOnlyMiddle, stSide)

		hasSide := decodeOnlyMiddle == 0
		midFrame := make([]int16, frameLength+2)
		sideFrame := make([]int16, frameLength+2)
		midOut := midFrame[2:]
		sideOut := sideFrame[2:]

		ctrlMid := d.decodeFrameCoreInto(stMid, rd, midOut, frameCondCoding(frameIndex), stMid.VADFlags[frameIndex] != 0)
		d.finalizeDecodedChannelFrame(0, stMid, &ctrlMid, midOut, false)
		if nativeLowbandCaptureEnabled && len(d.stereoMidNative) >= (i+1)*frameLength {
			copy(d.stereoMidNative[i*frameLength:(i+1)*frameLength], midOut[:frameLength])
		}

		if hasSide {
			sideFrameIndex := int(stSide.nFramesDecoded)
			ctrlSide := d.decodeFrameCoreInto(stSide, rd, sideOut, sideFrameCondCoding(frameIndex, d.prevDecodeOnlyMiddle), stSide.VADFlags[sideFrameIndex] != 0)
			d.finalizeDecodedChannelFrame(1, stSide, &ctrlSide, sideOut, false)
		} else {
			clear(sideOut)
			stSide.nFramesDecoded++
		}

		silkStereoMSToLR(&d.stereo, midFrame, sideFrame, predQ13[:], fsKHz, frameLength)
		copy(leftNative[i*frameLength:(i+1)*frameLength], midFrame[1:frameLength+1])
		copy(rightNative[i*frameLength:(i+1)*frameLength], sideFrame[1:frameLength+1])

		// Track mid-only flag per frame (used for side-channel conditioning).
		d.prevDecodeOnlyMiddle = int32(decodeOnlyMiddle)
	}

	left = make([]float32, len(leftNative))
	right = make([]float32, len(rightNative))
	for i, v := range leftNative {
		left[i] = float32(v) / 32768.0
	}
	for i, v := range rightNative {
		right[i] = float32(v) / 32768.0
	}

	d.haveDecoded = true
	return left, right, nil
}

// DecodeStereoFrameInt16Into decodes a SILK stereo frame at native rate into
// caller-provided planar int16 buffers.
func (d *Decoder) DecodeStereoFrameInt16Into(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
	leftNative, rightNative []int16,
) (int, error) {
	_ = vadFlag
	stMid, stSide, framesPerPacket, frameLength, fsKHz, err := d.prepareStereoFramePacket(rd, bandwidth, duration)
	if err != nil {
		return 0, err
	}
	totalLen := framesPerPacket * frameLength
	if frameLength <= 0 || frameLength > maxFrameLength || len(leftNative) < totalLen || len(rightNative) < totalLen {
		return 0, ErrDecodeFailed
	}

	var predQ13 [2]int32
	decodeOnlyMiddle := 0

	for i := range framesPerPacket {
		frameIndex := int(stMid.nFramesDecoded)
		silkStereoDecodePred(rd, predQ13[:])
		if stSide.VADFlags[frameIndex] == 0 {
			decodeOnlyMiddle = silkStereoDecodeMidOnly(rd)
		} else {
			decodeOnlyMiddle = 0
		}
		d.maybeResetStereoSideChannel(decodeOnlyMiddle, stSide)

		hasSide := decodeOnlyMiddle == 0
		midFrame, sideFrame, ok := d.stereoFrameScratch(frameLength)
		if !ok {
			return 0, ErrDecodeFailed
		}
		midOut := midFrame[2:]
		sideOut := sideFrame[2:]

		ctrlMid := d.decodeFrameCoreInto(stMid, rd, midOut, frameCondCoding(frameIndex), stMid.VADFlags[frameIndex] != 0)
		d.finalizeDecodedChannelFrame(0, stMid, &ctrlMid, midOut, false)
		if nativeLowbandCaptureEnabled && len(d.stereoMidNative) >= (i+1)*frameLength {
			copy(d.stereoMidNative[i*frameLength:(i+1)*frameLength], midOut[:frameLength])
		}

		if hasSide {
			sideFrameIndex := int(stSide.nFramesDecoded)
			ctrlSide := d.decodeFrameCoreInto(stSide, rd, sideOut, sideFrameCondCoding(frameIndex, d.prevDecodeOnlyMiddle), stSide.VADFlags[sideFrameIndex] != 0)
			d.finalizeDecodedChannelFrame(1, stSide, &ctrlSide, sideOut, false)
		} else {
			clear(sideOut)
			stSide.nFramesDecoded++
		}

		silkStereoMSToLR(&d.stereo, midFrame, sideFrame, predQ13[:], fsKHz, frameLength)
		copy(leftNative[i*frameLength:(i+1)*frameLength], midFrame[1:frameLength+1])
		copy(rightNative[i*frameLength:(i+1)*frameLength], sideFrame[1:frameLength+1])

		d.prevDecodeOnlyMiddle = int32(decodeOnlyMiddle)
	}

	d.haveDecoded = true
	if nativeLowbandCaptureEnabled {
		d.lastNativeMonoLen = 0
		d.lastNativeMonoFsKHz = 0
		if len(d.stereoMidNative) >= totalLen {
			d.lastNativeMidLen = int32(totalLen)
			d.lastNativeMidFsKHz = int32(fsKHz)
		}
	}
	return totalLen, nil
}

// decodeStereoMidNative decodes a stereo SILK frame but returns only the mid channel
// at native sample rate. It still decodes the side channel to keep the bitstream aligned.
func (d *Decoder) decodeStereoMidNative(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) (mid []int16, frameLength int, err error) {
	_ = vadFlag
	stMid, stSide, framesPerPacket, frameLength, _, err := d.prepareStereoFramePacket(rd, bandwidth, duration)
	if err != nil {
		return nil, 0, err
	}
	midNative := make([]int16, framesPerPacket*frameLength)
	var predQ13 [2]int32
	decodeOnlyMiddle := 0

	for i := range framesPerPacket {
		frameIndex := int(stMid.nFramesDecoded)
		silkStereoDecodePred(rd, predQ13[:])
		if stSide.VADFlags[frameIndex] == 0 {
			decodeOnlyMiddle = silkStereoDecodeMidOnly(rd)
		} else {
			decodeOnlyMiddle = 0
		}
		d.maybeResetStereoSideChannel(decodeOnlyMiddle, stSide)

		hasSide := decodeOnlyMiddle == 0
		midOut := midNative[i*frameLength : (i+1)*frameLength]

		ctrlMid := d.decodeFrameCoreInto(stMid, rd, midOut, frameCondCoding(frameIndex), stMid.VADFlags[frameIndex] != 0)
		d.finalizeDecodedChannelFrame(0, stMid, &ctrlMid, midOut, false)

		if hasSide {
			sideOut := make([]int16, frameLength)
			sideFrameIndex := int(stSide.nFramesDecoded)
			ctrlSide := d.decodeFrameCoreInto(stSide, rd, sideOut, sideFrameCondCoding(frameIndex, d.prevDecodeOnlyMiddle), stSide.VADFlags[sideFrameIndex] != 0)
			d.finalizeDecodedChannelFrame(1, stSide, &ctrlSide, sideOut, false)
		} else {
			stSide.nFramesDecoded++
		}

		// Track mid-only flag per frame (used for side-channel conditioning).
		d.prevDecodeOnlyMiddle = int32(decodeOnlyMiddle)
	}

	d.haveDecoded = true
	return midNative, frameLength, nil
}
