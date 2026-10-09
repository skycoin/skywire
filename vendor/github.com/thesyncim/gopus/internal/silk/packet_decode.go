package silk

import (
	"errors"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// frameParams maps a SILK frame duration to the number of 20 ms SILK frames in
// the packet and the number of 5 ms subframes per frame. A 10 ms frame has one
// frame of two subframes; 20/40/60 ms have one/two/three frames of four
// subframes each. Mirrors the nb_subfr / nFramesPerPacket setup in libopus
// silk/dec_API.c silk_Decode.
func frameParams(duration FrameDuration) (framesPerPacket int, nbSubfr int, err error) {
	switch duration {
	case Frame10ms:
		framesPerPacket = 1
		nbSubfr = maxNbSubfr / 2
	case Frame20ms:
		framesPerPacket = 1
		nbSubfr = maxNbSubfr
	case Frame40ms:
		framesPerPacket = 2
		nbSubfr = maxNbSubfr
	case Frame60ms:
		framesPerPacket = 3
		nbSubfr = maxNbSubfr
	default:
		return 0, 0, ErrDecodeFailed
	}
	if framesPerPacket > maxFramesPerPacket {
		return 0, 0, ErrDecodeFailed
	}
	return framesPerPacket, nbSubfr, nil
}

// roundUpShellFrame rounds a frame length up to a whole number of 16-sample
// shell-coder blocks, the granularity at which pulses are decoded.
func roundUpShellFrame(length int) int {
	return (length + shellCodecFrameLength - 1) & ^(shellCodecFrameLength - 1)
}

// decodeVADFlagsAndLBRRFlag decodes one channel's VAD flags and its single
// LBRR-present flag. This is libopus dec_API.c's first flag-decode loop. The
// per-frame LBRR_flags symbol is NOT decoded here: libopus decodes every
// channel's (VAD + LBRR flag) for the whole packet before decoding any LBRR
// symbol, so stereo callers must run this phase for both channels first, then
// decodeLBRRFlagsSymbol for both. Decoding them together would interleave the
// side channel's flags with the mid channel's LBRR symbol and desync the range
// decoder on stereo LBRR packets.
func decodeVADFlagsAndLBRRFlag(rd *rangecoding.Decoder, st *decoderState, framesPerPacket int) {
	for i := range st.VADFlags {
		st.VADFlags[i] = 0
	}
	for i := range st.LBRRFlags {
		st.LBRRFlags[i] = 0
	}
	for i := range framesPerPacket {
		st.VADFlags[i] = int32(rd.DecodeBit(1))
	}
	st.LBRRFlag = int32(rd.DecodeBit(1))
}

// decodeLBRRFlagsSymbol decodes the per-frame LBRR flags symbol for one channel,
// libopus dec_API.c's second flag-decode loop. It must be called after
// decodeVADFlagsAndLBRRFlag has run for every channel in the packet.
func decodeLBRRFlagsSymbol(rd *rangecoding.Decoder, st *decoderState, framesPerPacket int) {
	if st.LBRRFlag == 0 {
		return
	}
	if framesPerPacket == 1 {
		st.LBRRFlags[0] = 1
		return
	}
	symbol := rd.DecodeICDF8Unchecked(silk_LBRR_flags_iCDF_ptr[framesPerPacket-2]) + 1
	for i := range framesPerPacket {
		st.LBRRFlags[i] = int32((symbol >> i) & 1)
	}
}

// decodeVADAndLBRRFlags decodes one channel's VAD flags, LBRR flag, and LBRR
// flags symbol in a single pass. This is correct only for mono (a single
// channel): the two flag-decode phases are adjacent there, so combining them
// matches libopus. Stereo must use the split phases above.
func decodeVADAndLBRRFlags(rd *rangecoding.Decoder, st *decoderState, framesPerPacket int) {
	decodeVADFlagsAndLBRRFlag(rd, st, framesPerPacket)
	decodeLBRRFlagsSymbol(rd, st, framesPerPacket)
}

// resetSideChannelState re-initializes the stereo side-channel decoder state
// when the side channel resumes after one or more mid-only frames, clearing the
// output and LPC history and restoring the default lag/gain/signal-type. Mirrors
// the side-channel reset in libopus silk/dec_API.c silk_Decode (the
// decode_only_middle transition).
func resetSideChannelState(st *decoderState) {
	for i := range st.outBuf {
		st.outBuf[i] = 0
	}
	for i := range st.sLPCQ14Buf {
		st.sLPCQ14Buf[i] = 0
	}
	st.lagPrev = 100
	st.lastGainIndex = 10
	st.prevSignalType = typeNoVoiceActivity
	st.firstFrameAfterReset = true
}

// ErrInvalidPacket indicates the packet data is malformed.
var ErrInvalidPacket = errors.New("silk: invalid packet")

// DecodeStereoEncoded decodes a range-coded SILK stereo packet back to
// separate left/right channel float32 slices at 48 kHz.
//
// The input must be a complete SILK stereo bitstream, as PacketEncoder.Encode
// or the libopus stereo encoder produces it.
// The packet contains range-coded VAD/LBRR header bits, stereo prediction
// indices, mid and side channel frame data.
//
// Returns left and right channels (each 48 kHz, length = frameSizeSamples).
func DecodeStereoEncoded(encoded []byte, bandwidth Bandwidth) (left, right []float32, err error) {
	if len(encoded) < 2 {
		return nil, nil, ErrInvalidPacket
	}

	// Compute expected 48 kHz frame size from bandwidth (20 ms frame).
	config := GetBandwidthConfig(bandwidth)
	frameSamples := config.SampleRate * 20 / 1000
	frameSizeSamples48 := frameSamples * 48000 / config.SampleRate

	// Use the proper stereo decoder which handles range-coded SILK stereo
	// packets (VAD/LBRR header, stereo prediction, mid/side frames, MS-to-LR).
	decoder := NewDecoder()
	interleaved, err := decoder.DecodeStereo(encoded, bandwidth, frameSizeSamples48, true)
	if err != nil {
		return nil, nil, err
	}

	// De-interleave [L0, R0, L1, R1, ...] into separate left/right slices.
	n := len(interleaved) / 2
	left = make([]float32, n)
	right = make([]float32, n)
	for i := range n {
		left[i] = interleaved[i*2]
		right[i] = interleaved[i*2+1]
	}

	return left, right, nil
}
