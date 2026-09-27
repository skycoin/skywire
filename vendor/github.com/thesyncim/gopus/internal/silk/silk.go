package silk

import (
	"errors"

	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Errors for SILK decoding
var (
	ErrInvalidBandwidth = errors.New("silk: invalid bandwidth for SILK mode")
	ErrDecodeFailed     = errors.New("silk: frame decode failed")
)

// finalizeSuccessfulDecode resets the shared PLC fade state after a good packet
// and records the frame size and channel count so a subsequent lost packet
// conceals at the right geometry. Called at the end of every successful public
// decode.
func (d *Decoder) finalizeSuccessfulDecode(frameSizeSamples, channels int) {
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, channels)
}

// Decode decodes one SILK mono packet and returns PCM at the decoder API rate.
// If data is nil it performs Packet Loss Concealment (PLC) for a lost packet
// instead of decoding. Mirrors the mono path of libopus silk/dec_API.c
// silk_Decode followed by the SILK resampler.
//
// The function never panics on malformed or truncated data: the range decoder
// keeps producing symbols past the end of the buffer, and the resulting
// out-of-range indices are bounded inside the SILK decoder. Valid input is
// bit-exact with libopus.
//
// Parameters:
//   - data: raw SILK frame data (without TOC byte), or nil for PLC
//   - bandwidth: NB, MB, or WB (from TOC)
//   - frameSizeSamples: frame size in samples at the decoder API rate
//   - vadFlag: voice activity flag (from bitstream header)
//
// Returns float32 samples in range [-1, 1] at the decoder API rate.
func (d *Decoder) Decode(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Validate bandwidth is SILK-compatible (NB, MB, WB only)
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode frame at native rate (without delay compensation, since we'll handle
	// sMid buffering in BuildMonoResamplerInput before resampling to the API rate)
	nativeSamples, err := d.DecodeFrameRaw(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	// This is necessary because sMid contains samples at the previous bandwidth's rate.
	d.HandleBandwidthChange(bandwidth)

	// Apply libopus-style sMid buffering per 20ms frame, then resample.
	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		// Fallback to slice-based length if something is off.
		frameLength = len(nativeSamples) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	output := make([]float32, 0, frameSizeSamples)
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]

		resamplerInput := d.BuildMonoResamplerInput(frame)
		output = append(output, resampler.Process(resamplerInput)...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeStereo decodes one SILK stereo packet and returns PCM at the decoder
// API rate. If data is nil it performs Packet Loss Concealment (PLC) for a lost
// packet instead of decoding. Mirrors the stereo path of libopus
// silk/dec_API.c silk_Decode (mid/side decode plus silk/stereo_MS_to_LR.c)
// followed by the SILK resampler. Like Decode it never panics on malformed
// input and is bit-exact with libopus on valid input.
//
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] at the decoder API rate.
func (d *Decoder) DecodeStereo(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Validate bandwidth is SILK-compatible
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLCStereo(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode stereo at native rate
	leftNative, rightNative, err := d.DecodeStereoFrame(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Mirror the int16-path bookkeeping so callers can read the native-rate
	// SILK lowband via LatestNativeStereo. Multistream stream decoders use
	// DecodeStereo (this function) and still need the pre-resample buffers
	// available for optional decoder-side post-processing (OSCE BWE /
	// LACE / NoLACE). The libopus-style int16 view is re-derived here from
	// the float32 native output rather than threading a second buffer
	// through DecodeStereoFrame.
	if nativeLowbandCaptureEnabled {
		d.recordNativeStereoFromFloat32(leftNative, rightNative, bandwidth)
	}

	// Resample to the decoder API rate using the libopus-compatible resampler.
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	left := leftResampler.Process(leftNative)
	right := rightResampler.Process(rightNative)

	// Interleave samples [L0, R0, L1, R1, ...]
	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return output, nil
}

// recordNativeStereoFromFloat32 copies the float32 native SILK left/right
// channels into the int16 scratch slots that back LatestNativeStereo. Used by
// DecodeStereo so multistream stream decoders (which take this path) can
// expose the pre-resample SILK lowband to optional post-processing such as
// OSCE BWE / LACE.
func (d *Decoder) recordNativeStereoFromFloat32(leftNative, rightNative []float32, bandwidth Bandwidth) {
	if !nativeLowbandCaptureEnabled {
		return
	}
	n := min(min(len(leftNative), len(d.stereoLeftNative)), len(rightNative))
	if n > len(d.stereoRightNative) {
		n = len(d.stereoRightNative)
	}
	for i := 0; i < n; i++ {
		l := leftNative[i] * 32768.0
		if l > 32767.0 {
			l = 32767.0
		} else if l < -32768.0 {
			l = -32768.0
		}
		r := rightNative[i] * 32768.0
		if r > 32767.0 {
			r = 32767.0
		} else if r < -32768.0 {
			r = -32768.0
		}
		d.stereoLeftNative[i] = int16(l)
		d.stereoRightNative[i] = int16(r)
	}
	d.lastNativeStereoLen = int32(n)
	d.lastNativeStereoFsKHz = int32(GetBandwidthConfig(bandwidth).SampleRate / 1000)
	d.lastNativeMonoLen = 0
	d.lastNativeMonoFsKHz = 0
	d.lastNativeMidLen = 0
	d.lastNativeMidFsKHz = 0
}

// DecodeStereoToMono decodes a SILK stereo frame and returns mono PCM at the decoder API rate.
// This matches libopus behavior when the decoder is configured for mono output.
func (d *Decoder) DecodeStereoToMono(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Validate bandwidth is SILK-compatible
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode mid channel at native rate (side channel decoded for state)
	midNative, frameLength, err := d.decodeStereoMidNative(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Resample to the decoder API rate using libopus-compatible resampler and sMid buffering.
	framesPerPacket := 0
	if frameLength > 0 {
		framesPerPacket = len(midNative) / frameLength
	}
	resampler := d.GetResamplerForChannel(bandwidth, 0)
	output := make([]float32, 0, frameSizeSamples)
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(midNative) || frameLength == 0 {
			break
		}
		frame := midNative[start:end]

		resamplerInput := make([]float32, frameLength)
		resamplerInput[0] = float32(d.stereo.sMid[1]) / 32768.0
		if frameLength > 1 {
			for i := 0; i < frameLength-1; i++ {
				resamplerInput[i+1] = float32(frame[i]) / 32768.0
			}
		}
		d.updateMonoHistoryFromInt16(frame)

		output = append(output, resampler.Process(resamplerInput)...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeMonoToStereo decodes a mono SILK frame and returns stereo PCM at the decoder API rate.
// When stereoToMono is true (stereo -> mono transition), the right channel is
// resampled using its own resampler state instead of simple duplication to
// match libopus behavior.
func (d *Decoder) DecodeMonoToStereo(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	stereoToMono bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	if data == nil {
		if !useStereoHistory {
			mono, err := d.decodePLC(bandwidth, frameSizeSamples)
			if err != nil {
				return nil, err
			}
			out := make([]float32, len(mono)*2)
			duplicateMonoFloat32ToStereo(out, mono, len(mono))
			return out, nil
		}
		return d.decodePLCStereo(bandwidth, frameSizeSamples)
	}

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler)
	nativeSamples, err := d.DecodeFrameRaw(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)

	leftOut := make([]float32, 0, frameSizeSamples)
	var rightOut []float32
	if useStereoHistory {
		rightOut = make([]float32, 0, frameSizeSamples)
	}

	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		left := leftResampler.Process(resamplerInput)
		leftOut = append(leftOut, left...)
		if useStereoHistory {
			right := rightResampler.Process(resamplerInput)
			rightOut = append(rightOut, right...)
		}
	}

	out := make([]float32, len(leftOut)*2)
	for i := range leftOut {
		out[i*2] = leftOut[i]
		if useStereoHistory {
			if i < len(rightOut) {
				out[i*2+1] = rightOut[i]
			} else {
				out[i*2+1] = leftOut[i]
			}
		} else {
			out[i*2+1] = leftOut[i]
		}
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return out, nil
}

// DecodeWithDecoder decodes a SILK mono frame using a pre-initialized range decoder.
// This mirrors Decode() but avoids re-initializing the range decoder.
func (d *Decoder) DecodeWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler)
	nativeSamples, err := d.DecodeFrameRaw(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	output := make([]float32, 0, frameSizeSamples)
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		processOutput := resampler.Process(resamplerInput)
		output = append(output, processOutput...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeWithDecoderInto decodes a SILK mono frame into a caller-provided buffer.
// This is the zero-allocation version of DecodeWithDecoder.
// Returns the number of samples written to the output buffer.
func (d *Decoder) DecodeWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	output []float32,
) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler).
	// Use int16-native path to avoid float32->int16 reconversion before resampling.
	nativeSamples, err := d.decodeFrameRawInt16(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return 0, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	outputOffset := 0
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInputInt16(frame)
		n := resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
		outputOffset += n
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return outputOffset, nil
}

// DecodeStereoWithDecoderInto decodes a SILK stereo frame into a caller-owned
// interleaved stereo buffer.
func (d *Decoder) DecodeStereoWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	output []float32,
) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}
	if len(output) < frameSizeSamples*2 {
		return 0, ErrDecodeFailed
	}

	d.handleBandwidthChange(bandwidth)

	duration := d.frameDurationFromAPISamples(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, err
	}
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := framesPerPacket * nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	leftNative, rightNative, ok := d.GetStereoInt16Scratch(nativeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	nativeSamples, err = d.DecodeStereoFrameInt16Into(rd, bandwidth, duration, vadFlag, leftNative, rightNative)
	if err != nil {
		return 0, err
	}
	// Record native-rate length / fs for optional decoder-side post-
	// processing (OSCE BWE). LatestNativeStereo reads back from
	// stereoLeftNative / stereoRightNative which are aliased by the
	// `leftNative` / `rightNative` slices returned by GetStereoInt16Scratch
	// above, so the pre-resample SILK lowband is available to the caller
	// without performing a second decode pass. Mirrors LatestNativeMono.
	if nativeLowbandCaptureEnabled {
		d.lastNativeStereoLen = int32(nativeSamples)
		d.lastNativeStereoFsKHz = int32(config.SampleRate / 1000)
	}
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	nLeft := leftResampler.ProcessInt16Into(leftNative[:nativeSamples], leftScratch)
	nRight := rightResampler.ProcessInt16Into(rightNative[:nativeSamples], rightScratch)
	n := min(nRight, nLeft)
	if n < 0 || n*2 > len(output) {
		return 0, ErrDecodeFailed
	}
	for i := range n {
		output[i*2] = leftScratch[i]
		output[i*2+1] = rightScratch[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)
	return n, nil
}

// DecodeStereoWithDecoder decodes a SILK stereo frame using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	leftNative, rightNative, err := d.DecodeStereoFrame(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	left := leftResampler.Process(leftNative)
	right := rightResampler.Process(rightNative)

	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return output, nil
}

// DecodeStereoToMonoWithDecoder decodes a SILK stereo frame to mono using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoToMonoWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	midNative, frameLength, err := d.decodeStereoMidNative(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	framesPerPacket := 0
	if frameLength > 0 {
		framesPerPacket = len(midNative) / frameLength
	}
	resampler := d.GetResamplerForChannel(bandwidth, 0)
	output := make([]float32, 0, frameSizeSamples)
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(midNative) || frameLength == 0 {
			break
		}
		frame := midNative[start:end]

		resamplerInput := make([]float32, frameLength)
		resamplerInput[0] = float32(d.stereo.sMid[1]) / 32768.0
		if frameLength > 1 {
			for i := 0; i < frameLength-1; i++ {
				resamplerInput[i+1] = float32(frame[i]) / 32768.0
			}
		}
		d.updateMonoHistoryFromInt16(frame)

		output = append(output, resampler.Process(resamplerInput)...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeMonoToStereoWithDecoder decodes a mono SILK frame to stereo using a pre-initialized range decoder.
// stereoToMono mirrors libopus behavior for stereo->mono transitions.
func (d *Decoder) DecodeMonoToStereoWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	stereoToMono bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler)
	nativeSamples, err := d.DecodeFrameRaw(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)

	leftOut := make([]float32, 0, frameSizeSamples)
	var rightOut []float32
	if useStereoHistory {
		rightOut = make([]float32, 0, frameSizeSamples)
	}

	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		left := leftResampler.Process(resamplerInput)
		leftOut = append(leftOut, left...)
		if useStereoHistory {
			right := rightResampler.Process(resamplerInput)
			rightOut = append(rightOut, right...)
		}
	}

	out := make([]float32, len(leftOut)*2)
	for i := range leftOut {
		out[i*2] = leftOut[i]
		if useStereoHistory {
			if i < len(rightOut) {
				out[i*2+1] = rightOut[i]
			} else {
				out[i*2+1] = leftOut[i]
			}
		} else {
			out[i*2+1] = leftOut[i]
		}
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return out, nil
}

// DecodeMonoToStereoWithDecoderInto decodes a mono SILK frame into a
// caller-owned interleaved stereo buffer.
func (d *Decoder) DecodeMonoToStereoWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	stereoToMono bool,
	output []float32,
) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}
	if len(output) < frameSizeSamples*2 {
		return 0, ErrDecodeFailed
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)

	d.handleBandwidthChange(bandwidth)

	duration := d.frameDurationFromAPISamples(frameSizeSamples)
	nativeSamples, err := d.decodeFrameRawInt16(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return 0, err
	}

	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}
	if frameLength <= 0 {
		return 0, ErrDecodeFailed
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	outputOffset := 0
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) {
			return 0, ErrDecodeFailed
		}
		resamplerInput := d.BuildMonoResamplerInputInt16(nativeSamples[start:end])
		nLeft := leftResampler.ProcessInt16Into(resamplerInput, leftScratch)
		n := nLeft
		if useStereoHistory {
			nRight := rightResampler.ProcessInt16Into(resamplerInput, rightScratch)
			if nRight < n {
				n = nRight
			}
		}
		if n < 0 || (outputOffset+n)*2 > len(output) {
			return 0, ErrDecodeFailed
		}
		if useStereoHistory {
			for i := 0; i < n; i++ {
				left := leftScratch[i]
				output[(outputOffset+i)*2] = left
				output[(outputOffset+i)*2+1] = rightScratch[i]
			}
		} else {
			duplicateMonoFloat32ToStereo(output[outputOffset*2:], leftScratch, n)
		}
		outputOffset += n
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)
	return outputOffset, nil
}

func duplicateMonoFloat32ToStereo(dst, src []float32, n int) {
	if n <= 0 {
		return
	}
	dst = dst[: n*2 : n*2]
	src = src[:n:n]
	i := 0
	j := 0
	for ; i+3 < n; i += 4 {
		v0 := src[i]
		v1 := src[i+1]
		v2 := src[i+2]
		v3 := src[i+3]
		dst[j] = v0
		dst[j+1] = v0
		dst[j+2] = v1
		dst[j+3] = v1
		dst[j+4] = v2
		dst[j+5] = v2
		dst[j+6] = v3
		dst[j+7] = v3
		j += 8
	}
	for ; i < n; i++ {
		v := src[i]
		dst[j] = v
		dst[j+1] = v
		j += 2
	}
}

func (d *Decoder) stereoFloat32Scratch(frameSizeSamples int) (left, right []float32, ok bool) {
	if frameSizeSamples <= 0 {
		return nil, nil, false
	}
	needed := frameSizeSamples * 2
	if cap(d.upsampleScratch) < needed {
		return nil, nil, false
	}
	scratch := d.upsampleScratch[:needed]
	return scratch[:frameSizeSamples], scratch[frameSizeSamples:needed], true
}

// DecodeToInt16 decodes and converts to int16 PCM.
// This is a convenience wrapper for common audio output formats.
func (d *Decoder) DecodeToInt16(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]int16, error) {
	samples, err := d.Decode(data, bandwidth, frameSizeSamples, vadFlag)
	if err != nil {
		return nil, err
	}

	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = float32ToInt16(s)
	}

	return output, nil
}

// DecodeStereoToInt16 decodes stereo and converts to int16 PCM.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] as int16.
func (d *Decoder) DecodeStereoToInt16(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]int16, error) {
	samples, err := d.DecodeStereo(data, bandwidth, frameSizeSamples, vadFlag)
	if err != nil {
		return nil, err
	}

	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = float32ToInt16(s)
	}

	return output, nil
}

// BandwidthFromOpus converts Opus bandwidth to SILK bandwidth.
// SILK only supports NB, MB, WB. SWB and FB use Hybrid mode.
//
// Returns the SILK bandwidth and true if valid, or (0, false) for SWB/FB.
func BandwidthFromOpus(opusBandwidth int) (Bandwidth, bool) {
	switch opusBandwidth {
	case 0: // Narrowband
		return BandwidthNarrowband, true
	case 1: // Mediumband
		return BandwidthMediumband, true
	case 2: // Wideband
		return BandwidthWideband, true
	default:
		return 0, false // SWB/FB not supported in SILK-only mode
	}
}

// decodePLC generates concealment audio for a lost mono packet.
func (d *Decoder) decodePLC(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	output := make([]float32, frameSizeSamples)
	n, err := d.DecodePLCInto(bandwidth, frameSizeSamples, output)
	if err != nil {
		return nil, err
	}
	return output[:n], nil
}

// DecodePLCInto generates mono SILK concealment audio for a lost packet and
// writes the resampled API-rate PCM into output. It is the zero-allocation
// counterpart of decodePLC: the caller owns the destination buffer, which must
// hold at least frameSizeSamples samples. Returns the number of samples written.
func (d *Decoder) DecodePLCInto(bandwidth Bandwidth, frameSizeSamples int, output []float32) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if len(output) < frameSizeSamples {
		return 0, ErrDecodeFailed
	}
	// Get fade factor for this loss
	fadeFactor := d.plcState.RecordLoss()
	// Match libopus silk_PLC_conceal() input cadence: use decoder-state lossCnt.
	lossCnt := d.state[0].lossCnt

	// Get native sample count from the API-rate frame size.
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / d.outputSampleRate()

	// Generate concealment at native rate.
	// Use LTP-aware concealment whenever per-channel SILK PLC state is valid.
	// Fall back to legacy concealment only when required state is unavailable.
	var concealed []float32
	hookLagPrev := 0
	usedDeepPLCHook := false
	if state := d.ensureSILKPLCState(0); state != nil && d.state[0].nbSubfr > 0 {
		// Use the per-channel decoder view so the PLC reads the SILK frame's
		// actual LPC order (10 for NB/MB, 16 for WB) and its exact integer
		// sLPC_Q14_buf history, matching libopus silk_PLC_conceal. The
		// Decoder-level accessor reports a stale order and would force the
		// float-derived LPC fallback, corrupting unvoiced concealment.
		concealedQ0 := plc.ConcealSILKWithLTP(d.plcDecoderView(0), state, int(lossCnt), nativeSamples)
		if d.scratchOutput != nil && len(d.scratchOutput) >= nativeSamples {
			concealed = d.scratchOutput[:nativeSamples]
		} else {
			concealed = make([]float32, nativeSamples)
		}
		// ConcealSILKWithLTP already applies libopus PLC attenuation cadence.
		// Keep only Q0 -> float scaling here (no extra external fade).
		scale := float32(1.0 / 32768.0)
		for i := 0; i < nativeSamples && i < len(concealedQ0); i++ {
			concealed[i] = float32(concealedQ0[i]) * scale
		}
		if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
			hookLagPrev = lag
		}
	} else {
		concealed = plc.ConcealSILK(d, nativeSamples, fadeFactor)
	}
	if dredHooksEnabled && d.hasDeepPLCLossMonoHook() {
		if len(concealed) < nativeSamples {
			concealed = make([]float32, nativeSamples)
		}
		ok, lagPrev := d.fireDeepPLCLossMonoHook(concealed)
		if ok {
			usedDeepPLCHook = true
			if lagPrev > 0 {
				hookLagPrev = lagPrev
			}
		}
	}
	if hookLagPrev > 0 {
		d.state[0].lagPrev = int32(hookLagPrev)
	} else if state := d.ensureSILKPLCState(0); state != nil {
		if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
			d.state[0].lagPrev = int32(lag)
		}
	}

	// Update decoder state for PLC gluing and outBuf cadence.
	if usedDeepPLCHook {
		d.applyDeepPLCHistoryMono(&d.state[0], concealed)
	}
	d.recordPLCLossForState(&d.state[0], concealed)
	if usedDeepPLCHook {
		d.state[0].plcSkipRecoveryGlue = true
	}
	// Match libopus dec_API.c: on FLAG_PACKET_LOST, reset gain index
	// to avoid gain-bounce on subsequent good frames.
	d.state[0].lastGainIndex = 10

	// Expose the native-rate concealed PCM via LatestNativeMono so the optional
	// OSCE BWE forward pass can read the pre-resample lowband during PLC.
	// libopus enables OSCE_MODE_SILK_BBWE on `data == NULL` whenever the
	// internal sample rate is 16 kHz; the gopus equivalent needs access to the
	// native-rate samples for the 16k -> 48k BWE input.
	if nativeLowbandCaptureEnabled {
		if cap(d.scratchOutInt16) >= len(concealed) {
			buf := d.scratchOutInt16[:len(concealed)]
			for i, v := range concealed {
				buf[i] = float32ToInt16(v)
			}
		}
		d.lastNativeMonoLen = int32(len(concealed))
		d.lastNativeMonoFsKHz = int32(config.SampleRate / 1000)
		d.lastNativeStereoLen = 0
		d.lastNativeStereoFsKHz = 0
		d.lastNativeMidLen = 0
		d.lastNativeMidFsKHz = 0
	}

	// Resample to the decoder API rate using the same mono sMid buffering cadence as good frames.
	duration := d.frameDurationFromAPISamples(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil || framesPerPacket <= 0 {
		resampler := d.GetResampler(bandwidth)
		n := resampler.ProcessInto(d.BuildMonoResamplerInput(concealed), output)
		d.captureMonoPLCLowband(output[:n])
		return n, nil
	}
	frameLength := nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	if frameLength <= 0 || frameLength*framesPerPacket != len(concealed) {
		frameLength = len(concealed) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	outputOffset := 0
	captureI16 := d.plcLowbandCaptureArm && len(d.plcLowbandCapture) > 0
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(concealed) || frameLength == 0 {
			break
		}
		if dredHooksEnabled && usedDeepPLCHook && len(d.scratchOutInt16) >= end {
			frameQ0 := d.scratchOutInt16[start:end]
			resamplerInput := d.BuildMonoResamplerInputInt16(frameQ0)
			if captureI16 {
				outputOffset += d.resamplePLCFrameCaptureInt16(resampler, resamplerInput, output[outputOffset:], outputOffset)
			} else {
				outputOffset += resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
			}
			continue
		}
		if captureI16 {
			// Resample the int16-recovered concealment (float concealed = q/32768,
			// so float32ToInt16 recovers q exactly) so the captured lowband is the
			// integer-exact resampler output the FIXED_POINT path needs, while the
			// float output is bit-identical to the float-input resampler path.
			frameI16 := d.plcConcealInt16Scratch(end - start)
			for i := start; i < end; i++ {
				frameI16[i-start] = float32ToInt16(concealed[i])
			}
			resamplerInput := d.BuildMonoResamplerInputInt16(frameI16)
			outputOffset += d.resamplePLCFrameCaptureInt16(resampler, resamplerInput, output[outputOffset:], outputOffset)
			continue
		}
		frame := concealed[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		outputOffset += resampler.ProcessInto(resamplerInput, output[outputOffset:])
	}

	if captureI16 {
		d.plcLowbandCaptured = min(outputOffset, len(d.plcLowbandCapture))
	}
	return outputOffset, nil
}

// plcConcealInt16Scratch returns a reusable int16 scratch buffer of length n for
// converting the mono PLC concealment to int16 before resampling.
func (d *Decoder) plcConcealInt16Scratch(n int) []int16 {
	if cap(d.plcConcealI16) < n {
		d.plcConcealI16 = make([]int16, n)
	}
	d.plcConcealI16 = d.plcConcealI16[:n]
	return d.plcConcealI16
}

// resamplePLCFrameCaptureInt16 resamples one int16 PLC frame, writing the float
// output to out and the native int16 resampler output into the armed
// plcLowbandCapture buffer at outputOffset. It returns the sample count written.
func (d *Decoder) resamplePLCFrameCaptureInt16(resampler *LibopusResampler, resamplerInput []int16, out []float32, outputOffset int) int {
	dst := d.plcLowbandCapture[outputOffset:]
	return resampler.ProcessInt16IntoBoth(resamplerInput, out, dst)
}

// captureMonoPLCLowband captures a fallback mono PLC lowband (the degenerate
// frameParams<=0 path) by re-deriving the int16 from the float output (out =
// int16/32768, so float32ToInt16 recovers it). Only runs when capture is armed.
func (d *Decoder) captureMonoPLCLowband(out []float32) {
	if !d.plcLowbandCaptureArm || len(d.plcLowbandCapture) == 0 {
		return
	}
	n := min(len(out), len(d.plcLowbandCapture))
	for i := range n {
		d.plcLowbandCapture[i] = float32ToInt16(out[i])
	}
	d.plcLowbandCaptured = n
}

// RecordPLCLossMono records a mono SILK PLC loss event for glue-frame tracking.
// This mirrors the state bookkeeping performed by decodePLC.
func (d *Decoder) RecordPLCLossMono(concealed []float32) {
	d.recordPLCLossForState(&d.state[0], concealed)
}

// RecordPLCLossStereo records stereo SILK PLC loss events for glue-frame tracking.
// This mirrors the state bookkeeping performed by decodePLCStereo.
func (d *Decoder) RecordPLCLossStereo(left, right []float32) {
	d.recordPLCLossForState(&d.state[0], left)
	d.recordPLCLossForState(&d.state[1], right)
}

// recordPLCLossForState applies the per-channel state updates for a concealed
// frame: it bumps the loss counter, converts the concealment to int16, updates
// the LTP/output history and outBuf, runs comfort-noise generation and PLC
// frame-energy gluing, and writes the (possibly CNG-modified) result back into
// concealed. Mirrors the lost-frame bookkeeping of libopus silk/decode_frame.c
// (silk_CNG and silk_PLC_glue_frames on a concealed frame).
func (d *Decoder) recordPLCLossForState(st *decoderState, concealed []float32) {
	if st == nil {
		return
	}
	channel := 0
	if st == &d.state[1] {
		channel = 1
	}
	st.lossCnt++
	if len(concealed) == 0 {
		st.plcConcEnergy = 0
		st.plcConcEnergyShift = 0
		st.plcLastFrameLost = true
		return
	}

	if cap(d.scratchOutInt16) < len(concealed) {
		d.scratchOutInt16 = make([]int16, len(concealed))
	}
	tmp := d.scratchOutInt16[:len(concealed)]
	for i, v := range concealed {
		tmp[i] = float32ToInt16(v)
	}

	d.updateHistoryInt16(tmp)
	// Keep decoder outBuf cadence aligned with normal decode path so
	// subsequent PLC rewhitening uses the most recent concealed output.
	silkUpdateOutBuf(st, tmp)

	// Match libopus decode_frame.c cadence on lost frames:
	// CNG is applied after outBuf update, then PLC glue captures concealed energy.
	d.applyCNG(channel, st, nil, tmp)
	silkPLCGlueFrames(st, tmp, len(tmp))

	const scale = float32(1.0 / 32768.0)
	for i := range tmp {
		concealed[i] = float32(tmp[i]) * scale
	}
}

func (d *Decoder) applyDeepPLCHistoryMono(st *decoderState, concealed []float32) {
	if st == nil || len(concealed) == 0 {
		return
	}
	order := int(st.lpcOrder)
	if order <= 0 {
		return
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	prevGainQ16 := st.prevGainQ16
	if plcState := d.ensureSILKPLCState(0); plcState != nil && plcState.PrevGainQ16[1] > 0 {
		prevGainQ16 = plcState.PrevGainQ16[1]
	}
	prevGainQ10 := prevGainQ16 >> 6
	if prevGainQ10 <= 0 {
		return
	}
	var history [maxLPCOrder]int32
	start := max(len(concealed)-order, 0)
	historyIdx := 0
	for i := start; i < len(concealed) && historyIdx < order; i++ {
		sampleQ0 := int32(float32ToInt16(concealed[i]))
		scaled := float32(sampleQ0) * float32(1<<24) / float32(prevGainQ10)
		history[historyIdx] = floorHalfPlusF32ToInt32(scaled)
		historyIdx++
	}
	if historyIdx == 0 {
		return
	}
	setSLPCQ14HistoryQ14(st, history[:historyIdx])
}

// ApplyDeepPLCLossMono records a mono lost frame whose concealment was produced
// externally (the deep/neural PLC), writing concealed into rendered and updating
// the channel-0 SILK loss state, pitch lag and LPC history so a following good
// frame glues correctly. It still advances the standard SILK PLC state to keep
// the pitch lag consistent. Returns the number of samples written. Used by the
// hybrid DRED/DeepPLC path; the standard SILK concealment is decodePLC.
func (d *Decoder) ApplyDeepPLCLossMono(concealed, rendered []float32, lagPrev int) int {
	if d == nil || len(concealed) == 0 || len(rendered) < len(concealed) {
		return 0
	}
	st := &d.state[0]
	var plcLagPrev int
	if plcState := d.ensureSILKPLCState(0); plcState != nil && st.nbSubfr > 0 {
		if view := d.plcDecoderView(0); view != nil {
			_ = plc.ConcealSILKWithLTP(view, plcState, int(max(0, st.lossCnt)), len(concealed))
			plcLagPrev = int((plcState.PitchLQ8 + 128) >> 8)
		}
	}
	tmp := rendered[:len(concealed)]
	copy(tmp, concealed)
	d.recordPLCLossForState(st, tmp)
	switch {
	case plcLagPrev > 0:
		st.lagPrev = int32(plcLagPrev)
	case lagPrev > 0:
		st.lagPrev = int32(lagPrev)
	}
	st.lastGainIndex = 10
	d.applyDeepPLCHistoryMono(st, concealed)
	return len(tmp)
}

// syncLegacyPLCState aligns legacy PLC helper fields from libopus-style decoder state.
// ConcealSILK() still reads these legacy fields, so keep them synchronized after
// successful frame decodes (including LBRR/FEC decodes).
func (d *Decoder) syncLegacyPLCState(st *decoderState, recent []int16) {
	if st == nil {
		return
	}

	if st.lpcOrder > 0 {
		d.lpcOrder = st.lpcOrder
	}
	d.isPreviousFrameVoiced = int(st.indices.signalType) == typeVoiced

	order := int(d.lpcOrder)
	if order <= 0 {
		return
	}
	if order > len(d.prevLPCValues) {
		order = len(d.prevLPCValues)
	}

	scale := float32(1.0 / 32768.0)
	if len(recent) >= order {
		base := len(recent) - order
		for i := 0; i < order; i++ {
			d.prevLPCValues[i] = float32(recent[base+i]) * scale
		}
		return
	}

	historyLen := len(d.outputHistory)
	if historyLen == 0 {
		return
	}
	start := d.historyIndex - order
	for i := 0; i < order; i++ {
		idx := start + i
		for idx < 0 {
			idx += historyLen
		}
		idx %= historyLen
		d.prevLPCValues[i] = d.outputHistory[idx]
	}
}

// decodePLCStereo generates concealment audio for a lost stereo packet.
func (d *Decoder) decodePLCStereo(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	output := make([]float32, frameSizeSamples*2)
	n, err := d.DecodePLCStereoInto(bandwidth, frameSizeSamples, output)
	if err != nil {
		return nil, err
	}
	return output[:n], nil
}

// DecodePLCStereoInto generates stereo SILK concealment audio for a lost packet
// and writes the interleaved [L0,R0,L1,R1,...] API-rate PCM into output. It is
// the zero-allocation counterpart of decodePLCStereo: the caller owns the
// destination buffer, which must hold at least 2*frameSizeSamples samples.
// Returns the number of interleaved samples written.
func (d *Decoder) DecodePLCStereoInto(bandwidth Bandwidth, frameSizeSamples int, output []float32) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if len(output) < frameSizeSamples*2 {
		return 0, ErrDecodeFailed
	}

	// Get native sample count from the API-rate frame size.
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / d.outputSampleRate()
	// A frame size that maps to zero native samples (e.g. frameSizeSamples 0)
	// has nothing to conceal. Return before touching PLC/decoder state: the
	// stereo MS->LR pass (silkStereoMSToLR) assumes frameLength >= the
	// interpolation window and would read past the mid/side history otherwise.
	// Every valid SILK frame size yields nativeSamples >= 80, so this never
	// fires on real input.
	if nativeSamples <= 0 {
		return 0, nil
	}

	// Get fade factor for this loss
	fadeFactor := d.plcState.RecordLoss()
	// Match libopus silk_PLC_conceal() input cadence: use decoder-state lossCnt.
	lossCnt := d.state[0].lossCnt

	// libopus stereo PLC keeps operating in mid/side space and only converts
	// back to left/right through silk_stereo_MS_to_LR before resampling.
	// Our decoder states 0/1 track mid/side, not left/right.
	hasSide := d.prevDecodeOnlyMiddle == 0
	mid := d.plcStereoFloatScratch(&d.plcMidNative, nativeSamples)
	side := d.plcStereoFloatScratch(&d.plcSideNative, nativeSamples)

	midState := d.ensureSILKPLCState(0)
	sideState := d.ensureSILKPLCState(1)
	midView := d.plcDecoderView(0)
	sideView := d.plcDecoderView(1)
	usedDeepPLCHook := false
	hookLagPrev := 0
	if midState != nil && midView != nil && d.state[0].nbSubfr > 0 {
		midQ0 := plc.ConcealSILKWithLTP(midView, midState, int(lossCnt), nativeSamples)
		scale := float32(1.0 / 32768.0)
		for i := 0; i < nativeSamples && i < len(midQ0); i++ {
			mid[i] = float32(midQ0[i]) * scale
		}
		if lag := int((midState.PitchLQ8 + 128) >> 8); lag > 0 {
			hookLagPrev = lag
		}
	} else {
		// Legacy fallback when richer PLC state is unavailable.
		left, right := plc.ConcealSILKStereo(d, nativeSamples, fadeFactor)
		copy(mid, left)
		if hasSide {
			copy(side, right)
		}
	}
	if dredHooksEnabled && d.hasDeepPLCLossMonoHook() {
		ok, lagPrev := d.fireDeepPLCLossMonoHook(mid)
		if ok {
			usedDeepPLCHook = true
			if lagPrev > 0 {
				hookLagPrev = lagPrev
			}
		}
	}
	if usedDeepPLCHook && hookLagPrev > 0 {
		d.state[0].lagPrev = int32(hookLagPrev)
	} else if !usedDeepPLCHook && hookLagPrev > 0 {
		d.state[0].lagPrev = int32(hookLagPrev)
	}
	if hasSide && sideState != nil && sideView != nil && d.state[1].nbSubfr > 0 {
		sideQ0 := plc.ConcealSILKWithLTP(sideView, sideState, int(lossCnt), nativeSamples)
		scale := float32(1.0 / 32768.0)
		for i := 0; i < nativeSamples && i < len(sideQ0); i++ {
			side[i] = float32(sideQ0[i]) * scale
		}
		if lag := int((sideState.PitchLQ8 + 128) >> 8); lag > 0 {
			d.state[1].lagPrev = int32(lag)
		}
	}

	// Update decoder state for the concealed internal channels before MS->LR.
	if usedDeepPLCHook {
		d.applyDeepPLCHistoryMono(&d.state[0], mid)
	}
	d.recordPLCLossForState(&d.state[0], mid)
	if usedDeepPLCHook {
		d.state[0].plcSkipRecoveryGlue = true
	}
	d.state[0].lastGainIndex = 10
	if hasSide {
		d.recordPLCLossForState(&d.state[1], side)
		d.state[1].lastGainIndex = 10
	}

	// Convert concealed mid/side to left/right using the saved stereo predictor.
	midFrame, sideFrame, ok := d.stereoFrameScratch(nativeSamples)
	if !ok {
		midFrame = make([]int16, nativeSamples+2)
		sideFrame = make([]int16, nativeSamples+2)
	}
	clear(midFrame[:nativeSamples+2])
	clear(sideFrame[:nativeSamples+2])
	for i := range nativeSamples {
		midFrame[i+2] = float32ToInt16(mid[i])
		if hasSide {
			sideFrame[i+2] = float32ToInt16(side[i])
		}
	}
	d.plcPredQ13[0] = int32(d.stereo.predPrevQ13[0])
	d.plcPredQ13[1] = int32(d.stereo.predPrevQ13[1])
	silkStereoMSToLR(&d.stereo, midFrame, sideFrame, d.plcPredQ13[:], config.SampleRate/1000, nativeSamples)

	// Resample left/right channels to API rate.
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftUp := d.plcStereoFloatScratch(&d.plcLeftUp, frameSizeSamples)
	rightUp := d.plcStereoFloatScratch(&d.plcRightUp, frameSizeSamples)
	captureI16 := d.plcLowbandCaptureArm && len(d.plcLowbandCapture) > 0
	var leftI16, rightI16 []int16
	var nLeft, nRight int
	if captureI16 {
		leftI16 = d.plcStereoLeftI16Scratch(frameSizeSamples)
		rightI16 = d.plcStereoRightI16Scratch(frameSizeSamples)
		nLeft = leftResampler.ProcessInt16IntoBoth(midFrame[1:nativeSamples+1], leftUp, leftI16)
		nRight = rightResampler.ProcessInt16IntoBoth(sideFrame[1:nativeSamples+1], rightUp, rightI16)
	} else {
		nLeft = leftResampler.ProcessInt16Into(midFrame[1:nativeSamples+1], leftUp)
		nRight = rightResampler.ProcessInt16Into(sideFrame[1:nativeSamples+1], rightUp)
	}
	if nRight < nLeft {
		nLeft = nRight
	}
	if nLeft < 0 {
		nLeft = 0
	}

	for i := 0; i < nLeft; i++ {
		output[i*2] = leftUp[i]
		output[i*2+1] = rightUp[i]
	}
	if captureI16 {
		filled := 0
		for i := 0; i < nLeft && 2*i+1 < len(d.plcLowbandCapture); i++ {
			d.plcLowbandCapture[2*i] = leftI16[i]
			d.plcLowbandCapture[2*i+1] = rightI16[i]
			filled = 2 * (i + 1)
		}
		d.plcLowbandCaptured = filled
	}

	return nLeft * 2, nil
}

// plcStereoFloatScratch returns a zeroed slice of length n backed by *buf,
// growing the backing buffer if necessary. The clear matches the freshly
// allocated make([]float32, n) the PLC path previously used so concealment
// output that only partially fills the buffer keeps the libopus zero tail.
func (d *Decoder) plcStereoFloatScratch(buf *[]float32, n int) []float32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]float32, n)
	}
	s := (*buf)[:n]
	clear(s)
	return s
}

func (d *Decoder) plcStereoLeftI16Scratch(n int) []int16 {
	if cap(d.plcStereoLI16) < n {
		d.plcStereoLI16 = make([]int16, n)
	}
	d.plcStereoLI16 = d.plcStereoLI16[:n]
	return d.plcStereoLI16
}

func (d *Decoder) plcStereoRightI16Scratch(n int) []int16 {
	if cap(d.plcStereoRI16) < n {
		d.plcStereoRI16 = make([]int16, n)
	}
	d.plcStereoRI16 = d.plcStereoRI16[:n]
	return d.plcStereoRI16
}

func float32ToInt16(v float32) int16 {
	return opusmath.Float32ToInt16(v)
}
