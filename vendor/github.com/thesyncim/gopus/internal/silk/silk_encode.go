package silk

import "errors"

// ErrInvalidPacket indicates the packet data is malformed.
var ErrInvalidPacket = errors.New("silk: invalid packet")

// Encode encodes mono PCM audio to SILK frame.
// pcm: Input samples at encoder's configured sample rate
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK frame bytes
func Encode(pcm []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	enc := NewEncoder(bandwidth)
	return enc.EncodeFrame(pcm, nil, vadFlag), nil
}

// EncodeStereo encodes stereo PCM audio to SILK frame.
// left, right: Input samples for each channel
// bandwidth: Target bandwidth
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK frame bytes for combined mid/side channels
//
// Note: This function allocates new encoders per call.
// For zero-allocation encoding, use EncodeStereoWithEncoder.
func EncodeStereo(left, right []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	enc := NewEncoder(bandwidth)
	sideEnc := NewEncoder(bandwidth)
	return EncodeStereoWithEncoder(enc, sideEnc, left, right, bandwidth, vadFlag)
}

// EncodeStereoWithEncoder encodes stereo PCM audio using pre-existing encoders
// into a single SILK stereo bitstream that matches the libopus format.
//
// The output is a valid SILK stereo packet containing:
//   - VAD/LBRR header bits for both channels (patched at end)
//   - LBRR data for both channels
//   - Stereo prediction indices (range coded)
//   - Mid-only flag (when side VAD is inactive)
//   - Mid channel frame data (range coded)
//   - Side channel frame data (range coded, if not mid-only)
//
// This matches libopus enc_API.c silk_Encode() for nChannelsInternal == 2.
//
// enc: Pre-allocated encoder for mid channel
// sideEnc: Pre-allocated encoder for side channel
// left, right: Input samples for each channel at SILK sample rate
// bandwidth: Target bandwidth
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK stereo frame bytes
func EncodeStereoWithEncoder(enc, sideEnc *Encoder, left, right []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	return EncodeStereoWithEncoderVADFlags(enc, sideEnc, left, right, bandwidth, []bool{vadFlag})
}

// EncodeStereoWithEncoderVADFlags is the multi-frame variant of
// EncodeStereoWithEncoder. It accepts per-20ms VAD flags for 40/60ms packets.
func EncodeStereoWithEncoderVADFlags(enc, sideEnc *Encoder, left, right []float32, bandwidth Bandwidth, vadFlags []bool) ([]byte, error) {
	return EncodeStereoWithEncoderVADFlagsWithSide(enc, sideEnc, left, right, bandwidth, vadFlags, nil)
}

// EncodeStereoWithEncoderVADFlagsWithSide is like EncodeStereoWithEncoderVADFlags,
// but accepts optional side-channel VAD flags.
// When sideVADFlags is nil, side VAD defaults to mid VAD gating for backward compatibility.
func EncodeStereoWithEncoderVADFlagsWithSide(enc, sideEnc *Encoder, left, right []float32, bandwidth Bandwidth, vadFlags []bool, sideVADFlags []bool) ([]byte, error) {
	return EncodeStereoWithEncoderVADFlagsAndStatesWithSide(
		enc, sideEnc, left, right, bandwidth, vadFlags, nil, sideVADFlags, nil,
	)
}

// EncodeStereoWithEncoderVADFlagsAndStatesWithSide is like
// EncodeStereoWithEncoderVADFlagsWithSide, but also applies optional per-frame
// VAD-derived state for mid/side encoders.
func EncodeStereoWithEncoderVADFlagsAndStatesWithSide(
	enc, sideEnc *Encoder,
	left, right []float32,
	bandwidth Bandwidth,
	vadFlags []bool,
	vadStates []VADFrameState,
	sideVADFlags []bool,
	sideVADStates []VADFrameState,
) ([]byte, error) {
	return EncodeStereoWithEncoderVADAnalyzersWithSide(
		enc,
		sideEnc,
		left,
		right,
		bandwidth,
		vadFlags,
		vadStates,
		nil,
		sideVADFlags,
		sideVADStates,
		nil,
	)
}

// EncodeStereoWithEncoderVADAnalyzersWithSide is like
// EncodeStereoWithEncoderVADFlagsAndStatesWithSide, but lets callers compute
// VAD from the transformed mid/side frames after stereo_LR_to_MS, matching
// libopus enc_API.c packet control cadence.
func EncodeStereoWithEncoderVADAnalyzersWithSide(
	enc, sideEnc *Encoder,
	left, right []float32,
	bandwidth Bandwidth,
	vadFlags []bool,
	vadStates []VADFrameState,
	midAnalyzer VADFrameAnalyzer,
	sideVADFlags []bool,
	sideVADStates []VADFrameState,
	sideAnalyzer VADFrameAnalyzer,
) ([]byte, error) {
	if sideEnc == nil {
		sideEnc = NewEncoder(bandwidth)
	}
	if len(left) == 0 || len(right) == 0 {
		return nil, ErrInvalidPacket
	}
	if len(right) < len(left) {
		left = left[:len(right)]
	} else if len(left) < len(right) {
		right = right[:len(left)]
	}

	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	frameLength20ms := config.SampleRate * 20 / 1000
	if frameLength20ms <= 0 {
		frameLength20ms = len(left)
	}
	if len(left) < frameLength20ms {
		frameLength20ms = len(left)
	}
	nFrames := max(len(left)/frameLength20ms, 1)
	if nFrames > maxFramesPerPacket {
		nFrames = maxFramesPerPacket
	}

	// Reset packet state for both encoders
	enc.ResetPacketState()
	sideEnc.ResetPacketState()
	enc.nFramesPerPacket = int32(nFrames)
	sideEnc.nFramesPerPacket = int32(nFrames)
	// Preserve base rate-control settings; we override maxBits/useCBR per
	// channel per block to mirror libopus enc_API.c.
	baseMidMaxBits := enc.maxBits
	baseSideMaxBits := sideEnc.maxBits
	baseMidUseCBR := enc.useCBR
	baseSideUseCBR := sideEnc.useCBR
	baseMidBlockUseCBR := enc.blockUseCBR
	baseSideBlockUseCBR := sideEnc.blockUseCBR
	baseMidBitrate := enc.targetRateBps
	baseSideBitrate := sideEnc.targetRateBps
	basePacketMaxBits := baseMidMaxBits
	if basePacketMaxBits <= 0 {
		basePacketMaxBits = baseSideMaxBits
	}
	// libopus uses one packet-level bits-exceeded state for stereo SILK.
	// Keep the side encoder aligned with the shared packet state.
	sideEnc.SetBitsExceeded(enc.BitsExceeded())

	// Match libopus stereo_LR_to_MS: use the previous frame's speech activity
	// and leave it at the reset default before the first encoded frame.
	speechActQ8 := enc.speechActivityQ8

	// Compute total bitrate for stereo rate allocation.
	packetRate := baseMidBitrate
	if packetRate <= 0 {
		packetRate = enc.targetRateBps
	}
	totalRate := stereoAllocationTargetRate(enc, int(packetRate), frameLength20ms*nFrames, 0)
	if totalRate <= 0 {
		totalRate = int(enc.targetRateBps)
	}
	if totalRate <= 0 {
		totalRate = 20000
	}

	// Create shared range encoder for the stereo packet.
	bufSize := max(len(left)+200, maxSilkPacketBytes)
	output := ensureByteSlice(&enc.scratchOutput, bufSize)
	enc.scratchRangeEncoder.Init(output)
	re := &enc.scratchRangeEncoder

	// Reserve header bits for 2 channels: (nFramesPerPacket + 1) * 2 = 4 bits
	// This reserves space for VAD flags + LBRR flags for both channels.
	nChannels := 2
	headerBits := (nFrames + 1) * nChannels
	iCDF := []uint16{
		uint16(256 - (256 >> headerBits)),
		0,
	}
	re.EncodeICDF16(0, iCDF, 8)

	encodeStereoLBRRPacket(re, enc, sideEnc, nFrames, &enc.stereo)

	var midVAD [maxFramesPerPacket]int
	var sideVAD [maxFramesPerPacket]int
	for i := 0; i < nFrames; i++ {
		start := i * frameLength20ms
		end := min(start+frameLength20ms, len(left))
		frameLength := end - start
		if frameLength <= 0 {
			continue
		}

		leftFrame := left[start:end]
		rightFrame := right[start:end]
		// Fold this frame's LBRR header bits into the shared nBitsUsedLBRR moving
		// average before deriving the target rate, matching libopus enc_API.c (the
		// EMA update precedes silk_stereo_LR_to_MS). Only the frame that wrote the
		// packet LBRR header carries non-zero bits; later frames reset it to zero so
		// they do not re-subtract the already-paid LBRR overhead.
		enc.applyLBRRReservoirUpdate()
		frameRate := stereoAllocationTargetRate(enc, int(packetRate), frameLength*nFrames, re.Tell())
		if frameRate > 0 {
			totalRate = frameRate
		}

		// Convert L/R to M/S with stereo prediction, rate allocation, and width decision.
		// This matches libopus silk_stereo_LR_to_MS. Under the gopus_fixed_point
		// build the integer front-end also returns the exact int16 mid/side the
		// per-channel integer encode body consumes (midI16/sideI16); on the float
		// build those are nil.
		midOut, sideOut, midI16, sideI16, ix, midOnly, midRate, sideRate, _ := enc.stereoFrontEnd(
			leftFrame, rightFrame, frameLength, fsKHz,
			totalRate, speechActQ8, false,
		)
		enc.stereo.saveLBRRStereoMeta(i, ix, midOnly)
		prevDecodeOnlyMiddle := enc.stereo.prevDecodeOnlyMiddle
		midFramesEncodedInPacket := enc.nFramesEncoded
		EncodeStereoIndices(re, ix)

		// Match libopus stereo control flow: apply the per-channel
		// mid/side rate split from stereo_LR_to_MS before encoding each
		// channel frame so controlSNR runs at the intended target.
		if midRate > 0 {
			enc.SetBitrate(midRate)
			enc.SetPreAdjustedTargetRateBps(midRate)
		}
		if sideRate > 0 {
			sideEnc.SetBitrate(sideRate)
			sideEnc.SetPreAdjustedTargetRateBps(sideRate)
		}

		midFrameVAD := stereoVADFlagAt(vadFlags, i)
		var midState VADFrameState
		var midFeedbackState VADFrameState
		midFeedbackVAD := midFrameVAD
		if midAnalyzer != nil {
			midFeedbackState, midFeedbackVAD = midAnalyzer(midOut, len(midOut), fsKHz)
		}
		if i < len(vadStates) && vadStates[i].Valid {
			midState = vadStates[i]
		} else if midAnalyzer != nil {
			midState = midFeedbackState
			midFrameVAD = midFeedbackVAD
		}
		if midFrameVAD {
			midVAD[i] = 1
		}

		// Match libopus: side channel is coded only when stereo_LR_to_MS
		// reports mid_only_flag == 0 (sideRate > 0). The earlier
		// forceSideCoding override on 40/60ms packets bloated the stereo
		// SILK packet versus libopus.
		sideFrameVAD := midFrameVAD && !midOnly
		var sideState VADFrameState
		// Under the gopus_fixed_point build the side VAD flag must come from the
		// integer VAD (silk_encode_do_VAD_FIX) run on the int16 side frame, since
		// it gates the mid-only-flag coding exactly as in libopus enc_API.c. We
		// run it on a copy of the side VAD state so the real per-channel encode
		// advances the live state exactly once.
		if !midOnly {
			if active, ok := enc.stereoSideVADFixed(sideEnc, sideI16, frameLength, fsKHz, midFrameVAD); ok {
				sideFrameVAD = active
			} else if sideAnalyzer != nil {
				sideState, sideFrameVAD = sideAnalyzer(sideOut, len(sideOut), fsKHz)
			} else if len(sideVADFlags) > 0 {
				sideFrameVAD = stereoVADFlagAt(sideVADFlags, i)
				if i < len(sideVADStates) && sideVADStates[i].Valid {
					sideState = sideVADStates[i]
				}
			}
		} else {
			sideFrameVAD = false
		}
		if sideFrameVAD {
			sideVAD[i] = 1
		}
		if !sideFrameVAD {
			midOnlyVal := 0
			if midOnly {
				midOnlyVal = 1
			}
			EncodeStereoMidOnly(re, midOnlyVal)
		}

		// Match libopus enc_API.c channel budgeting:
		// 1) scale block maxBits for 40/60 ms packets
		// 2) if stereo_LR_to_MS allocated side rate, mid uses non-CBR and
		//    gives side headroom; side still receives the frame maxBits.
		frameMaxBits := basePacketMaxBits
		switch nFrames {
		case 2:
			if i == 0 {
				frameMaxBits = frameMaxBits * 3 / 5
			}
		case 3:
			if i == 0 {
				frameMaxBits = frameMaxBits * 2 / 5
			} else if i == 1 {
				frameMaxBits = frameMaxBits * 3 / 4
			}
		}
		midMaxBits := frameMaxBits
		sideMaxBits := frameMaxBits
		if frameMaxBits > 0 {
			if sideRate > 0 {
				reserve := basePacketMaxBits / int32(nFrames*2)
				midMaxBits -= reserve
				if midMaxBits < 1 {
					midMaxBits = 1
				}
			}
			enc.maxBits = midMaxBits
		}
		frameUseCBR := baseMidUseCBR && i == nFrames-1
		enc.blockUseCBR = frameUseCBR
		if sideRate > 0 {
			enc.blockUseCBR = false
		}
		sideEnc.blockUseCBR = baseSideUseCBR && i == nFrames-1

		if midState.Valid {
			enc.SetVADState(midState.SpeechActivityQ8, midState.InputTiltQ15, midState.InputQualityBandsQ15)
		} else if !midFrameVAD {
			enc.SetVADState(speechActivityDTXThresholdQ8-1, 0, [4]int32{-1, -1, -1, -1})
		}

		// Set up shared range encoder for mid channel encoding.
		enc.stereoCondMid = enc
		enc.stereoCondMidFramesEncoded = midFramesEncodedInPacket
		enc.stereoChannelIdx = 0
		enc.stereoPrevDecodeOnlyMiddle = int32(prevDecodeOnlyMiddle)
		enc.SetRangeEncoder(re)
		// Feed the integer mid frame (post stereo_LR_to_MS, pre LP cutoff) to the
		// integer encode body verbatim. No-op on the float build.
		enc.stageStereoInt16(midI16)
		_ = enc.EncodeFrame(midOut, nil, midFrameVAD)
		// The VAD header bit must reflect the integer VAD decision computed inside
		// the encode body (libopus enc_API.c VAD_flags), not the Opus-level flag.
		if enc.fixedEncodeActive() {
			if enc.FixedLastVADFlag() {
				midVAD[i] = 1
			} else {
				midVAD[i] = 0
			}
		}

		// Encode side channel if not mid-only.
		// Use the side-channel VAD decision (not the mid flag) so the
		// frame type coding stays consistent with patched side VAD header bits.
		if !midOnly {
			if prevDecodeOnlyMiddle == 1 {
				sideEnc.ResetStereoSideAfterMidOnly()
			}
			if sideState.Valid {
				sideEnc.SetVADState(sideState.SpeechActivityQ8, sideState.InputTiltQ15, sideState.InputQualityBandsQ15)
			} else if !sideFrameVAD {
				sideEnc.SetVADState(speechActivityDTXThresholdQ8-1, 0, [4]int32{-1, -1, -1, -1})
			}
			sideEnc.stereoCondMid = enc
			// libopus enc_API.c selects the side channel's conditional-coding
			// type from state_Fxx[0].nFramesEncoded - n with n = 1. The mid
			// channel (n = 0) has already encoded this 20 ms block and
			// incremented its nFramesEncoded by the time the side channel is
			// coded, so the side channel sees the post-increment mid count.
			sideEnc.stereoCondMidFramesEncoded = enc.nFramesEncoded
			sideEnc.stereoChannelIdx = 1
			sideEnc.stereoPrevDecodeOnlyMiddle = int32(prevDecodeOnlyMiddle)
			if sideMaxBits > 0 {
				sideEnc.maxBits = sideMaxBits
			}
			sideEnc.SetRangeEncoder(re)
			sideEnc.stageStereoInt16(sideI16)
			_ = sideEnc.EncodeFrame(sideOut, nil, sideFrameVAD)
			if sideEnc.fixedEncodeActive() {
				if sideEnc.FixedLastVADFlag() {
					sideVAD[i] = 1
				} else {
					sideVAD[i] = 0
				}
			}
		}
		// libopus enc_API.c increments nFramesEncoded on every channel each 20 ms
		// block, even when the side frame is not coded (mid-only).
		if sideEnc.nFramesEncoded < enc.nFramesEncoded {
			sideEnc.nFramesEncoded = enc.nFramesEncoded
		}

		speechActQ8 = enc.speechActivityQ8
		if midOnly {
			enc.stereo.prevDecodeOnlyMiddle = 1
		} else {
			enc.stereo.prevDecodeOnlyMiddle = 0
		}
	}
	enc.maxBits = baseMidMaxBits
	sideEnc.maxBits = baseSideMaxBits
	enc.useCBR = baseMidUseCBR
	sideEnc.useCBR = baseSideUseCBR
	enc.blockUseCBR = baseMidBlockUseCBR
	sideEnc.blockUseCBR = baseSideBlockUseCBR
	enc.targetRateBps = baseMidBitrate
	sideEnc.targetRateBps = baseSideBitrate

	// Patch header bits: VAD flags + LBRR flags for both channels.
	// Format: [mid_VAD[0..N-1] | mid_LBRR | side_VAD[0..N-1] | side_LBRR]
	flags := uint32(0)
	for i := 0; i < nFrames; i++ {
		flags = (flags << 1) | uint32(midVAD[i]&1)
	}
	flags = (flags << 1) | uint32(enc.lbrrFlag&1)
	for i := 0; i < nFrames; i++ {
		flags = (flags << 1) | uint32(sideVAD[i]&1)
	}
	flags = (flags << 1) | uint32(sideEnc.lbrrFlag&1)
	re.PatchInitialBits(flags, uint(headerBits))

	// Capture final range state.
	enc.lastRng = re.Range()

	// Capture nBytesOut before ec_enc_done, matching libopus. This pre-flush
	// (ec_tell+7)>>3 estimate is the value libopus feeds into the reservoir and
	// can exceed the flushed buffer length; only the returned slice is clamped.
	nBytesOut := max((re.Tell()+7)>>3, 0)

	// Finalize the range encoder.
	raw := re.Done()
	resultLen := min(nBytesOut, len(raw))
	// Return a view into the range encoder's own buffer (enc.scratchOutput),
	// matching the mono finalizeEncodeFrame path. The caller copies the bytes
	// into the packet buffer before the next encode reinitialises this buffer,
	// so no defensive copy is needed.
	result := raw[:resultLen]

	// Match libopus packet-level nBitsExceeded update.
	payloadSizeMs := (nFrames * frameLength20ms * 1000) / config.SampleRate
	packetBitrate := baseMidBitrate
	if packetBitrate <= 0 {
		packetBitrate = int32(totalRate)
	}
	enc.UpdatePacketBitsExceeded(nBytesOut, payloadSizeMs, int(packetBitrate))
	sideEnc.SetBitsExceeded(enc.BitsExceeded())
	enc.finishLBRRPacket()
	sideEnc.finishLBRRPacket()

	// Clean up shared encoder references.
	enc.rangeEncoder = nil
	sideEnc.rangeEncoder = nil

	return result, nil
}

func stereoAllocationTargetRate(enc *Encoder, targetRateBps, frameLength, bitsUsedSoFar int) int {
	if enc == nil || targetRateBps <= 0 || enc.sampleRate <= 0 || frameLength <= 0 {
		return 0
	}
	payloadSizeMs := (frameLength * 1000) / int(enc.sampleRate)
	if payloadSizeMs <= 0 {
		return targetRateBps
	}
	nBits := (targetRateBps * payloadSizeMs) / 1000
	nBits -= int(enc.nBitsUsedLBRR)
	if enc.nFramesPerPacket > 0 {
		nBits /= int(enc.nFramesPerPacket)
	}
	targetRate := 0
	if payloadSizeMs == 10 {
		targetRate = nBits * 100
	} else {
		targetRate = nBits * 50
	}
	targetRate -= int((enc.nBitsExceeded * 1000) / 500)
	if enc.nFramesEncoded > 0 {
		bitsBalance := bitsUsedSoFar - int(enc.nBitsUsedLBRR) - nBits*int(enc.nFramesEncoded)
		targetRate -= (bitsBalance * 1000) / 500
	}
	if targetRate > targetRateBps {
		targetRate = targetRateBps
	}
	if targetRate < 5000 {
		targetRate = 5000
	}
	return targetRate
}

func stereoVADFlagAt(vadFlags []bool, frame int) bool {
	if len(vadFlags) == 0 {
		return true
	}
	if frame < len(vadFlags) {
		return vadFlags[frame]
	}
	return vadFlags[len(vadFlags)-1]
}

func stereoFrameWithLookahead(src []float32, start, frameLength int) []float32 {
	end := start + frameLength
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(src) {
		end = len(src)
	}
	return src[start:end]
}

// DecodeStereoEncoded decodes a range-coded SILK stereo packet back to
// separate left/right channel float32 slices at 48 kHz.
//
// The input must be a complete SILK stereo bitstream produced by
// EncodeStereoWithEncoder (or the equivalent libopus stereo encoder).
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

// EncoderState holds encoder state for streaming encoding.
type EncoderState struct {
	enc *Encoder
}

// NewEncoderState creates a new streaming encoder.
func NewEncoderState(bandwidth Bandwidth) *EncoderState {
	return &EncoderState{
		enc: NewEncoder(bandwidth),
	}
}

// EncodeFrame encodes a frame maintaining state across calls.
func (es *EncoderState) EncodeFrame(pcm []float32, vadFlag bool) ([]byte, error) {
	return es.enc.EncodeFrame(pcm, nil, vadFlag), nil
}
