//go:build gopus_fixed_point

package silk

// encode_stereo_fixedpoint.go wires the bit-exact FIXED_POINT integer stereo
// front-end (silkStereoLRToMS, the port of silk/stereo_LR_to_MS.c) into the
// public stereo SILK encode path so STEREO SILK packets are byte-exact versus
// the libopus FIXED_POINT encoder.
//
// The default (float) build uses StereoLRToMSWithRates which performs the same
// integer analysis but emits float mid/side samples; under gopus_fixed_point the
// per-channel encode is driven by the integer silk_encode_frame_FIX chain, so
// the int16 mid/side from silkStereoLRToMS are threaded through verbatim (no
// float round-trip) to guarantee the inputBuf the encode body consumes equals
// what libopus enc_API.c produces.

// resetStereoSideFixedState clears the integer side-channel encode state when
// stereo side coding resumes after one or more mid-only frames, mirroring
// libopus enc_API.c (silk/enc_API.c lines 453-463): sShape (harm/tilt smoothers
// and LastGainIndex), sNSQ, prev_NLSFq_Q15, sLP.In_LP_State, prevLag,
// sNSQ.lagPrev, prevSignalType, sNSQ.prev_gain_Q16 and first_frame_after_reset.
// frameCounter and VAD state are preserved, as in libopus.
func (e *Encoder) resetStereoSideFixedState() {
	st := e.fixed
	if st == nil || !st.initialized {
		return
	}
	st.nsq = NSQState{}
	st.nsq.prevGainQ16 = 1 << 16
	st.nsq.lagPrev = 100
	st.prevNLSFqQ15 = [maxLPCOrder]int16{}
	st.prevLag = 100
	st.lastGainIndex = 10
	st.prevSignalType = typeNoVoiceActivity
	st.harmShapeGainSmthQ16 = 0
	st.tiltSmthQ16 = 0
	st.firstFrameAfterReset = true
}

// stereoFixedFrontEnd runs the validated integer silk_stereo_LR_to_MS on one
// 20 ms (or 10 ms) stereo block, updating the encoder stereo state in place and
// returning the int16 mid/side samples the per-channel encode body must consume
// (mid[1..frameLength] and side[1..frameLength] in libopus index terms), the
// quantized predictor indices, the mid-only flag and the per-channel rates.
//
// left/right are the channel inputs (length >= frameLength). They are converted
// to int16 with float32ToInt16 (FLOAT2INT16), matching the int16 inputBuf
// libopus feeds to silk_stereo_LR_to_MS.
func (e *Encoder) stereoFixedFrontEnd(
	left, right []float32,
	frameLength, fsKHz int,
	totalRateBps int,
	prevSpeechActQ8 int32,
	toMono bool,
) (midI16, sideI16 []int16, ix StereoQuantIndices, midOnly bool, midRate, sideRate int, widthQ14 int16) {
	if frameLength <= 0 || len(left) < frameLength || len(right) < frameLength {
		return nil, nil, StereoQuantIndices{}, false, 0, 0, 0
	}

	// mid holds x1 (left) at [2..frameLength+1]; mid[0..1] are overwritten with
	// the saved history inside silkStereoLRToMS. side is scratch of the same
	// length; x2 holds the right channel current frame (frameLength samples).
	mid := ensureInt16Slice(&e.scratchStereoFixedMid, frameLength+2)
	side := ensureInt16Slice(&e.scratchStereoFixedSide, frameLength+2)
	x2 := ensureInt16Slice(&e.scratchStereoFixedX2, frameLength)
	for n := 0; n < frameLength; n++ {
		mid[n+2] = float32ToInt16(left[n])
		x2[n] = float32ToInt16(right[n])
	}

	rawIx, midOnlyFlag, rates := silkStereoLRToMS(
		&e.stereo, mid, side, x2,
		int32(totalRateBps), prevSpeechActQ8, toMono, fsKHz, frameLength,
	)

	// Pack the [2][3]int8 indices into the public StereoQuantIndices layout used
	// by stereoEncodePred / EncodeStereoIndices.
	ix = stereoIndicesFromArray(rawIx)
	midOnly = midOnlyFlag != 0
	midRate = int(rates[0])
	sideRate = int(rates[1])
	widthQ14 = e.stereo.widthPrevQ14

	// mid encode input = mid[1..frameLength]; side encode input =
	// side[1..frameLength] (= libopus x2[n-1] for n in [0,frameLength)).
	midI16 = ensureInt16Slice(&e.scratchStereoFixedMidOut, frameLength)
	sideI16 = ensureInt16Slice(&e.scratchStereoFixedSideOut, frameLength)
	copy(midI16, mid[1:frameLength+1])
	copy(sideI16, side[1:frameLength+1])

	return midI16, sideI16, ix, midOnly, midRate, sideRate, widthQ14
}

// stereoFixedSideVADFlag runs the integer VAD (silk_VAD_GetSA_Q8 + the
// silk_encode_do_VAD_FIX speech-activity/DTX decision) on the int16 side frame
// against a COPY of the side encoder's VAD state, returning the VAD flag the
// side channel will report. libopus codes the stereo mid-only flag only when the
// side VAD flag is 0, so this must be known before the stereo header is emitted.
// The real per-channel encode runs the VAD once more for effect, advancing the
// live state exactly once (as in libopus, where silk_encode_do_VAD_Fxx runs a
// single time per side frame).
func (e *Encoder) stereoFixedSideVADFlag(sideEnc *Encoder, sideI16 []int16, frameLength, fsKHz int, opusVADActive bool) bool {
	st := sideEnc.ensureFixedState()
	vadCopy := st.vad
	opusActivity := 1
	if !opusVADActive {
		opusActivity = vadNoActivity
	}
	probe := &silkEncodeFrameFIXState{
		fsKHz:           fsKHz,
		frameLength:     frameLength,
		vadInput:        sideI16,
		vad:             vadCopy,
		opusVADActivity: opusActivity,
		noSpeechCounter: st.noSpeechCounter,
		inDTX:           st.inDTX,
	}
	return sideEnc.silkEncodeDoVADFIX(probe) != 0
}

// stereoFrontEnd runs the integer silk_stereo_LR_to_MS front-end and returns
// both float mid/side (for any VAD analyzers the caller passes) and the exact
// int16 mid/side that the per-channel integer encode body must consume. The
// int16 values are staged into the encode body via stageStereoInt16 just before
// each EncodeFrame call. The float outputs are an exact representation of the
// int16 samples (int16/32768) so analyzers see the same waveform; the encode
// body uses the staged int16 directly, avoiding any float round-trip on the LSB.
func (e *Encoder) stereoFrontEnd(
	left, right []float32,
	frameLength, fsKHz int,
	totalRateBps int,
	prevSpeechActQ8 int32,
	toMono bool,
) (midOut, sideOut []float32, midI16, sideI16 []int16, ix StereoQuantIndices, midOnly bool, midRate, sideRate int, widthQ14 int16) {
	midI16, sideI16, ix, midOnly, midRate, sideRate, widthQ14 = e.stereoFixedFrontEnd(
		left, right, frameLength, fsKHz, totalRateBps, prevSpeechActQ8, toMono,
	)
	midOut = ensureFloat32Slice(&e.scratchStereoMidOut, frameLength)
	sideOut = ensureFloat32Slice(&e.scratchStereoSideOut, frameLength)
	for n := 0; n < frameLength; n++ {
		midOut[n] = float32(midI16[n]) / 32768.0
		sideOut[n] = float32(sideI16[n]) / 32768.0
	}
	return midOut, sideOut, midI16, sideI16, ix, midOnly, midRate, sideRate, widthQ14
}

// stageStereoInt16 stages the int16 frame the next EncodeFrame call's integer
// encode body must consume verbatim (post stereo_LR_to_MS, pre LP cutoff).
func (e *Encoder) stageStereoInt16(frame []int16) {
	e.fixedStereoInt16In = frame
}

// stereoSideVADFixed computes the side-channel VAD flag with the integer VAD on
// a copy of the side state, returning (active, true). ok is always true under
// the gopus_fixed_point build.
func (e *Encoder) stereoSideVADFixed(sideEnc *Encoder, sideI16 []int16, frameLength, fsKHz int, opusVADActive bool) (active, ok bool) {
	return e.stereoFixedSideVADFlag(sideEnc, sideI16, frameLength, fsKHz, opusVADActive), true
}

// stereoIndicesFromArray converts the [2][3]int8 predictor index layout produced
// by silkStereoQuantPred into the public StereoQuantIndices struct consumed by
// stereoEncodePred.
func stereoIndicesFromArray(a [2][3]int8) StereoQuantIndices {
	var ix StereoQuantIndices
	for n := 0; n < 2; n++ {
		ix.Ix[n][0] = a[n][0]
		ix.Ix[n][1] = a[n][1]
		ix.Ix[n][2] = a[n][2]
	}
	return ix
}
