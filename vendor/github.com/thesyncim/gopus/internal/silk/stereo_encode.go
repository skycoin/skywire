package silk

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/util"
)

// Constants for stereo prediction weight quantization matching libopus
// stereoQuantTabSize and stereoQuantSubSteps are defined in libopus_consts.go

// StereoQuantIndices holds the three quantization indices per predictor
// as used by libopus stereo_quant_pred.c
type StereoQuantIndices struct {
	// ix[n][0]: 0-2 (index within triplet)
	// ix[n][1]: 0-4 (sub-step index)
	// ix[n][2]: 0-4 (triplet index, derived from main index / 3)
	Ix [2][3]int8
}

// encodeStereo converts stereo to mid-side and computes prediction weights.
// Returns mid channel, side channel, and Q13 stereo weights.
// Per RFC 6716 Section 4.2.8.
func (e *Encoder) encodeStereo(left, right []float32) (mid []float32, side []float32, weights [2]int32) {
	n := len(left)
	mid = ensureFloat32Slice(&e.scratchStereoMid, n)
	side = ensureFloat32Slice(&e.scratchStereoSide, n)

	// Compute mid and side channels
	for i := range n {
		mid[i] = (left[i] + right[i]) / 2
		side[i] = (left[i] - right[i]) / 2
	}

	// Compute stereo prediction weights using linear regression
	// side[n] ~= w0 * mid[n] + w1 * mid[n-1]
	// Minimize sum((side[n] - w0*mid[n] - w1*mid[n-1])^2)
	var sumMM, sumMS, sumM1M1, sumM1S, sumMM1 float32

	for i := 1; i < n; i++ {
		m := mid[i]
		m1 := mid[i-1]
		s := side[i]

		sumMM += m * m
		sumMS += m * s
		sumM1M1 += m1 * m1
		sumM1S += m1 * s
		sumMM1 += m * m1
	}

	// Solve 2x2 system for w0, w1
	// [sumMM   sumMM1] [w0]   [sumMS]
	// [sumMM1 sumM1M1] [w1] = [sumM1S]
	det := sumMM*sumM1M1 - sumMM1*sumMM1
	var w0, w1 float32
	if det > 1e-10 || det < -1e-10 {
		w0 = (sumMS*sumM1M1 - sumM1S*sumMM1) / det
		w1 = (sumMM*sumM1S - sumMM1*sumMS) / det
	}

	// Clamp to valid Q13 range [-16384, 16384] (approximately [-2, 2])
	// libopus uses [-1<<14, 1<<14] = [-16384, 16384]
	if w0 > 2.0 {
		w0 = 2.0
	}
	if w0 < -2.0 {
		w0 = -2.0
	}
	if w1 > 2.0 {
		w1 = 2.0
	}
	if w1 < -2.0 {
		w1 = -2.0
	}

	// Convert to Q13
	weights[0] = int32(w0 * 8192)
	weights[1] = int32(w1 * 8192)

	return mid, side, weights
}

// stereoQuantPred quantizes mid/side predictors using libopus 80-level quantization.
// This is a direct port of silk/stereo_quant_pred.c.
// predQ13 contains the predictors to quantize (modified in place).
// Returns the quantization indices.
func stereoQuantPred(predQ13 *[2]int32) StereoQuantIndices {
	var ix StereoQuantIndices

	const maxInt32 = int32(0x7FFFFFFF)

	// Quantize each predictor
	for n := range 2 {
		// Brute-force search over quantization levels
		errMinQ13 := maxInt32
		var quantPredQ13 int32

		for i := range stereoQuantTabSize - 1 {
			lowQ13 := int32(silk_stereo_pred_quant_Q13[i])
			// step_Q13 = (high - low) * 0.5 / STEREO_QUANT_SUB_STEPS
			// In Q16: 0.5/5 = 0.1 = 6554 (approximately)
			highQ13 := int32(silk_stereo_pred_quant_Q13[i+1])
			stepQ13 := smulwb(highQ13-lowQ13, 6554) // 0.5/5 in Q16 = 6554

			for j := range stereoQuantSubSteps {
				// lvl_Q13 = low_Q13 + step_Q13 * (2*j + 1)
				lvlQ13 := lowQ13 + stepQ13*(int32(2*j+1))
				errQ13 := util.Abs(predQ13[n] - lvlQ13)

				if errQ13 < errMinQ13 {
					errMinQ13 = errQ13
					quantPredQ13 = lvlQ13
					ix.Ix[n][0] = int8(i)
					ix.Ix[n][1] = int8(j)
				} else {
					// Error increasing, so we're past the optimum
					goto done
				}
			}
		}
	done:
		// Decompose index: ix[n][2] = ix[n][0] / 3, ix[n][0] = ix[n][0] % 3
		ix.Ix[n][2] = ix.Ix[n][0] / 3
		ix.Ix[n][0] = ix.Ix[n][0] - ix.Ix[n][2]*3
		predQ13[n] = quantPredQ13
	}

	// Subtract second from first predictor (delta coding)
	// This helps when actually applying these weights
	predQ13[0] -= predQ13[1]

	return ix
}

// stereoEncodePred encodes the stereo prediction indices to the bitstream.
// This is a direct port of silk/stereo_encode_pred.c.
func stereoEncodePred(enc *rangecoding.Encoder, ix StereoQuantIndices) {
	// Encode joint index: n = 5 * ix[0][2] + ix[1][2]
	n := 5*int(ix.Ix[0][2]) + int(ix.Ix[1][2])
	// Joint index must be < 25
	if n >= 25 {
		n = 24
	}
	enc.EncodeICDF(n, silk_stereo_pred_joint_iCDF, 8)

	// Encode individual indices for each predictor
	for i := range 2 {
		// Encode ix[n][0] using uniform3 (3 symbols: 0, 1, 2)
		idx0 := min(max(int(ix.Ix[i][0]), 0), 2)
		enc.EncodeICDF(idx0, silk_uniform3_iCDF, 8)

		// Encode ix[n][1] using uniform5 (5 symbols: 0, 1, 2, 3, 4)
		idx1 := min(max(int(ix.Ix[i][1]), 0), 4)
		enc.EncodeICDF(idx1, silk_uniform5_iCDF, 8)
	}
}

// smulwb and absInt32 are defined in other files (resample_libopus.go and gain_encode.go)

// encodeStereoWeights encodes stereo prediction weights to bitstream.
// This implements the libopus 80-level quantization scheme per RFC 6716 Section 4.2.8.3.
func (e *Encoder) encodeStereoWeights(weights [2]int32) {
	// Quantize the predictors
	predQ13 := weights
	ix := stereoQuantPred(&predQ13)

	// Encode to bitstream
	stereoEncodePred(e.rangeEncoder, ix)

	// Store quantized weights for state tracking
	// Note: predQ13[0] now contains the delta (pred0 - pred1)
	// To get the actual values back:
	// quantized_pred1 = predQ13[1]
	// quantized_pred0 = predQ13[0] + predQ13[1]
	e.prevStereoWeights[0] = int16(predQ13[0] + predQ13[1])
	e.prevStereoWeights[1] = int16(predQ13[1])
}

// EncodeStereoMidSide is the public method to convert stereo to mid-side
// and compute prediction weights. Used by hybrid mode encoder.
func (e *Encoder) EncodeStereoMidSide(left, right []float32) (mid []float32, side []float32, weights [2]int32) {
	return e.encodeStereo(left, right)
}

// EncodeStereoWeightsToRange encodes stereo prediction weights to the range encoder.
// Used by hybrid mode encoder.
func (e *Encoder) EncodeStereoWeightsToRange(weights [2]int32) {
	if e.rangeEncoder == nil {
		return
	}
	e.encodeStereoWeights(weights)
}

// GetRangeEncoderPtr returns the current range encoder pointer.
// Used to share range encoder between mid and side channel encoders.
func (e *Encoder) GetRangeEncoderPtr() *rangecoding.Encoder {
	return e.rangeEncoder
}

// QuantizeStereoWeights quantizes stereo prediction weights and returns the indices.
// This is the public interface for external callers who need the indices.
func QuantizeStereoWeights(predQ13 [2]int32) (quantized [2]int32, ix StereoQuantIndices) {
	quantized = predQ13
	ix = stereoQuantPred(&quantized)
	return quantized, ix
}

// EncodeStereoIndices encodes pre-computed stereo quantization indices.
// Use this when you've already called QuantizeStereoWeights.
func EncodeStereoIndices(enc *rangecoding.Encoder, ix StereoQuantIndices) {
	stereoEncodePred(enc, ix)
}

// stereoSelectCondCoding matches libopus enc_API.c condCoding selection, which keys
// off the mid encoder's nFramesEncoded at the start of the current 20 ms block
// (before either channel is encoded in that block).
func stereoSelectCondCoding(midFramesEncodedInPacket, channelIdx int, prevDecodeOnlyMiddle int32) int {
	if midFramesEncodedInPacket-channelIdx <= 0 {
		return codeIndependently
	}
	if channelIdx > 0 && prevDecodeOnlyMiddle != 0 {
		return codeIndependentlyNoLtpScaling
	}
	return codeConditionally
}

func (s *stereoEncState) saveLBRRStereoMeta(frameIdx int, ix StereoQuantIndices, midOnly bool) {
	if s == nil || frameIdx < 0 || frameIdx >= maxFramesPerPacket {
		return
	}
	s.lbrrStereoIx[frameIdx] = ix
	s.lbrrMidOnly[frameIdx] = 0
	if midOnly {
		s.lbrrMidOnly[frameIdx] = 1
	}
}

// EncodeStereoMidOnly encodes the stereo "mid-only" flag.
// This is only encoded when the side channel VAD flag is inactive.
// midOnly=1 means side channel is not coded.
func EncodeStereoMidOnly(enc *rangecoding.Encoder, midOnly int) {
	if enc == nil {
		return
	}
	if midOnly != 0 {
		midOnly = 1
	}
	enc.EncodeICDF(midOnly, silk_stereo_only_code_mid_iCDF, 8)
}

// encodeStereoWithLPFilter converts stereo to mid-side with proper LP/HP filtering
// and applies 8ms predictor interpolation for smooth frame boundary transitions.
// This matches libopus stereo_LR_to_MS.c by applying LP and HP filtering before
// computing the stereo predictors, which improves prediction quality.
//
// The LP filter is a 3-tap FIR [1,2,1]/4 that separates low and high frequency content.
// Separate predictors are computed for LP and HP bands and combined.
//
// The 8ms interpolation smoothly transitions from previous frame's predictor weights
// to the current frame's weights over the first 8ms of the frame, eliminating
// discontinuities at frame boundaries.
//
// Parameters:
//   - left, right: input stereo signals (should have length frameLength+2 for look-ahead)
//   - frameLength: number of samples in the frame (not including look-ahead)
//   - fsKHz: sample rate in kHz (8, 12, or 16)
//
// Returns:
//   - mid, side: converted mid/side signals (length frameLength+2)
//   - predQ13: two prediction coefficients in Q13 format
func (e *Encoder) encodeStereoWithLPFilter(left, right []float32, frameLength, fsKHz int) (mid, side []float32, predQ13 [2]int32) {
	// Ensure we have enough samples (need frameLength + 2 for LP filter)
	inputLen := len(left)
	if inputLen < frameLength+2 {
		// Pad with zeros if needed using scratch buffers
		padLeft := ensureFloat32Slice(&e.scratchStereoPadLeft, frameLength+2)
		padRight := ensureFloat32Slice(&e.scratchStereoPadRight, frameLength+2)
		copy(padLeft, left)
		for i := len(left); i < frameLength+2; i++ {
			padLeft[i] = 0
		}
		copy(padRight, right)
		for i := len(right); i < frameLength+2; i++ {
			padRight[i] = 0
		}
		left = padLeft
		right = padRight
	}

	// Convert L/R to basic M/S with look-ahead samples using scratch buffers
	msLen := frameLength + 2
	mid = ensureFloat32Slice(&e.scratchStereoMid, msLen)
	side = ensureFloat32Slice(&e.scratchStereoSide, msLen)
	stereoConvertLRToMSFloatInto(left, right, mid, side, frameLength)

	// Overwrite beginning of mid/side with history for correct LP filtering
	// (midWithHistory and sideWithHistory were separate buffers, but we can
	// just patch mid/side in place since we only need the first 2 samples)
	mid[0] = float32(e.stereo.sMid[0]) / 32768.0
	mid[1] = float32(e.stereo.sMid[1]) / 32768.0
	side[0] = float32(e.stereo.sSide[0]) / 32768.0
	side[1] = float32(e.stereo.sSide[1]) / 32768.0

	// Update state with last 2 samples for next frame
	e.stereo.sMid[0] = int16(mid[frameLength] * 32768)
	e.stereo.sMid[1] = int16(mid[frameLength+1] * 32768)
	e.stereo.sSide[0] = int16(side[frameLength] * 32768)
	e.stereo.sSide[1] = int16(side[frameLength+1] * 32768)

	// Apply LP/HP filtering using scratch buffers
	lpMid := ensureFloat32Slice(&e.scratchStereoLPMid, frameLength)
	hpMid := ensureFloat32Slice(&e.scratchStereoHPMid, frameLength)
	stereoLPFilterFloatInto(mid, lpMid, hpMid, frameLength)

	lpSide := ensureFloat32Slice(&e.scratchStereoLPSide, frameLength)
	hpSide := ensureFloat32Slice(&e.scratchStereoHPSide, frameLength)
	stereoLPFilterFloatInto(side, lpSide, hpSide, frameLength)

	// Find predictors for LP and HP bands
	predLP := stereoFindPredictorFloat(lpMid, lpSide, frameLength)
	predHP := stereoFindPredictorFloat(hpMid, hpSide, frameLength)

	predQ13[0] = predLP
	predQ13[1] = predHP

	return mid, side, predQ13
}

// StereoEncodeLRToMSWithInterp converts L/R to M/S and applies 8ms predictor interpolation.
// This is the full libopus-compatible stereo encoding function that:
// 1. Converts L/R to M/S with proper buffering
// 2. Computes LP/HP predictors
// 3. Applies 8ms smooth interpolation from previous to current predictors
// 4. Updates state for next frame continuity
//
// The side channel output has the prediction subtracted, matching libopus behavior.
// The interpolation ensures smooth transitions at frame boundaries.
//
// Parameters:
//   - left, right: input stereo signals (length frameLength+2 for look-ahead)
//   - frameLength: number of samples in the frame
//   - fsKHz: sample rate in kHz (8, 12, or 16)
//   - widthQ14: stereo width parameter in Q14 (16384 = full stereo)
//
// Returns:
//   - midOut: mid channel output (length frameLength)
//   - sideOut: side channel with prediction subtracted (length frameLength)
//   - predQ13: computed prediction coefficients [LP, HP]
func (e *Encoder) StereoEncodeLRToMSWithInterp(left, right []float32, frameLength, fsKHz int, widthQ14 int16) (midOut, sideOut []float32, predQ13 [2]int32) {
	// First compute predictors using LP/HP filtering
	mid, side, predQ13 := e.encodeStereoWithLPFilter(left, right, frameLength, fsKHz)

	// Use scratch output buffers
	midOut = ensureFloat32Slice(&e.scratchStereoMidOut, frameLength)
	sideOut = ensureFloat32Slice(&e.scratchStereoSideOut, frameLength)

	// Copy mid channel (offset by 1 to account for the history sample alignment)
	for n := range frameLength {
		midOut[n] = mid[n+1]
	}

	// Apply 8ms predictor interpolation to side channel
	// This matches libopus stereo_LR_to_MS.c lines 199-224
	interpSamples := stereoInterpLenMs * fsKHz

	// Get previous frame's predictor values (negated as in libopus)
	pred0Q13 := -int32(e.stereo.predPrevQ13[0])
	pred1Q13 := -int32(e.stereo.predPrevQ13[1])
	wQ24 := int32(e.stereo.widthPrevQ14) << 10

	// Compute interpolation deltas
	// denomQ16 = 1.0 / (8ms * fsKHz) in Q16
	denomQ16 := int32((1 << 16) / interpSamples)
	delta0Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(e.stereo.predPrevQ13[0]), denomQ16), 16)
	delta1Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(e.stereo.predPrevQ13[1]), denomQ16), 16)
	deltawQ24 := int32(silkSMULWB(int32(widthQ14)-int32(e.stereo.widthPrevQ14), denomQ16)) << 10

	// Process interpolation region (first 8ms)
	for n := 0; n < interpSamples && n < frameLength; n++ {
		pred0Q13 += delta0Q13
		pred1Q13 += delta1Q13
		wQ24 += deltawQ24

		// LP-filtered mid: (mid[n] + 2*mid[n+1] + mid[n+2]) << 9 (Q11)
		// Convert float mid values to int16 scale first, matching libopus int16 arithmetic.
		midN := int32(mid[n] * 32768)
		midN1 := int32(mid[n+1] * 32768)
		midN2 := int32(mid[n+2] * 32768)
		sumQ11 := (midN + midN2 + (midN1 << 1)) << 9

		// side' = width * side + pred0 * LP_mid + pred1 * mid
		// In Q8 precision matching libopus
		sideQ8 := silkSMULWB(wQ24, int32(side[n+1]*32768))
		sideQ8 = silkSMLAWB(sideQ8, sumQ11, pred0Q13)
		sideQ8 = silkSMLAWB(sideQ8, midN1<<11, pred1Q13)

		// Convert from Q8 to float
		sideOut[n] = float32(silkRSHIFT_ROUND(sideQ8, 8)) / 32768.0
	}

	// Process remainder (after 8ms interpolation)
	pred0Q13 = -predQ13[0]
	pred1Q13 = -predQ13[1]
	wQ24 = int32(widthQ14) << 10

	for n := interpSamples; n < frameLength; n++ {
		// LP-filtered mid: (mid[n] + 2*mid[n+1] + mid[n+2]) << 9 (Q11)
		midN := int32(mid[n] * 32768)
		midN1 := int32(mid[n+1] * 32768)
		midN2 := int32(mid[n+2] * 32768)
		sumQ11 := (midN + midN2 + (midN1 << 1)) << 9

		// side' = width * side + pred0 * LP_mid + pred1 * mid
		sideQ8 := silkSMULWB(wQ24, int32(side[n+1]*32768))
		sideQ8 = silkSMLAWB(sideQ8, sumQ11, pred0Q13)
		sideQ8 = silkSMLAWB(sideQ8, midN1<<11, pred1Q13)

		// Convert from Q8 to float
		sideOut[n] = float32(silkRSHIFT_ROUND(sideQ8, 8)) / 32768.0
	}

	// Update state for next frame
	e.stereo.predPrevQ13[0] = int16(predQ13[0])
	e.stereo.predPrevQ13[1] = int16(predQ13[1])
	e.stereo.widthPrevQ14 = widthQ14

	return midOut, sideOut, predQ13
}

// StereoEncodeLRToMSWithInterpQuantized is the libopus-aligned variant that:
// 1) computes LP/HP predictors,
// 2) quantizes predictors to 80 levels,
// 3) applies 8ms interpolation using the quantized predictors, and
// 4) updates encoder stereo state based on quantized predictors.
//
// This ensures the side residual is computed with the same predictors the decoder will use.
//
// Returns mid/side outputs (length frameLength), the quantized predictors, and the quant indices.
func (e *Encoder) StereoEncodeLRToMSWithInterpQuantized(left, right []float32, frameLength, fsKHz int, widthQ14 int16) (midOut, sideOut []float32, predQ13 [2]int32, ix StereoQuantIndices) {
	// Compute predictors from LP/HP filtered mid/side
	mid, side, predQ13 := e.encodeStereoWithLPFilter(left, right, frameLength, fsKHz)

	// Quantize predictors (80-level) and keep indices for encoding
	predQ13, ix = QuantizeStereoWeights(predQ13)

	// Use scratch output buffers
	midOut = ensureFloat32Slice(&e.scratchStereoMidOut, frameLength)
	sideOut = ensureFloat32Slice(&e.scratchStereoSideOut, frameLength)

	// Copy mid channel (offset by 1 to account for history alignment)
	for n := range frameLength {
		midOut[n] = mid[n+1]
	}

	// Apply 8ms predictor interpolation using quantized predictors
	interpSamples := stereoInterpLenMs * fsKHz

	pred0Q13 := -int32(e.stereo.predPrevQ13[0])
	pred1Q13 := -int32(e.stereo.predPrevQ13[1])
	wQ24 := int32(e.stereo.widthPrevQ14) << 10

	denomQ16 := int32((1 << 16) / interpSamples)
	delta0Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(e.stereo.predPrevQ13[0]), denomQ16), 16)
	delta1Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(e.stereo.predPrevQ13[1]), denomQ16), 16)
	deltawQ24 := int32(silkSMULWB(int32(widthQ14)-int32(e.stereo.widthPrevQ14), denomQ16)) << 10

	for n := 0; n < interpSamples && n < frameLength; n++ {
		pred0Q13 += delta0Q13
		pred1Q13 += delta1Q13
		wQ24 += deltawQ24

		// LP-filtered mid: (mid[n] + 2*mid[n+1] + mid[n+2]) << 9 (Q11)
		// Convert float mid values to int16 scale first, matching libopus int16 arithmetic.
		midN := int32(mid[n] * 32768)
		midN1 := int32(mid[n+1] * 32768)
		midN2 := int32(mid[n+2] * 32768)
		sumQ11 := (midN + midN2 + (midN1 << 1)) << 9

		sideQ8 := silkSMULWB(wQ24, int32(side[n+1]*32768))
		sideQ8 = silkSMLAWB(sideQ8, sumQ11, pred0Q13)
		sideQ8 = silkSMLAWB(sideQ8, midN1<<11, pred1Q13)
		sideOut[n] = float32(silkRSHIFT_ROUND(sideQ8, 8)) / 32768.0
	}

	pred0Q13 = -predQ13[0]
	pred1Q13 = -predQ13[1]
	wQ24 = int32(widthQ14) << 10

	for n := interpSamples; n < frameLength; n++ {
		// LP-filtered mid: (mid[n] + 2*mid[n+1] + mid[n+2]) << 9 (Q11)
		midN := int32(mid[n] * 32768)
		midN1 := int32(mid[n+1] * 32768)
		midN2 := int32(mid[n+2] * 32768)
		sumQ11 := (midN + midN2 + (midN1 << 1)) << 9

		sideQ8 := silkSMULWB(wQ24, int32(side[n+1]*32768))
		sideQ8 = silkSMLAWB(sideQ8, sumQ11, pred0Q13)
		sideQ8 = silkSMLAWB(sideQ8, midN1<<11, pred1Q13)
		sideOut[n] = float32(silkRSHIFT_ROUND(sideQ8, 8)) / 32768.0
	}

	// Update state for next frame (quantized predictors)
	e.stereo.predPrevQ13[0] = int16(predQ13[0])
	e.stereo.predPrevQ13[1] = int16(predQ13[1])
	e.stereo.widthPrevQ14 = widthQ14
	e.prevStereoWeights[0] = int16(predQ13[0] + predQ13[1])
	e.prevStereoWeights[1] = int16(predQ13[1])

	return midOut, sideOut, predQ13, ix
}

// StereoLRToMSWithRates is a libopus-aligned stereo front-end that computes
// mid/side signals, predictor indices, mid-only decision, and per-channel rates.
//
// This approximates silk_stereo_LR_to_MS using float-domain analysis while
// preserving the key rate/width decision logic.
func (e *Encoder) StereoLRToMSWithRates(
	left, right []float32,
	frameLength, fsKHz int,
	totalRateBps int,
	prevSpeechActQ8 int32,
	toMono bool,
) (midOut, sideOut []float32, ix StereoQuantIndices, midOnly bool, midRate, sideRate int, widthQ14 int16) {
	if frameLength <= 0 {
		return nil, nil, StereoQuantIndices{}, false, 0, 0, 0
	}

	if len(left) < frameLength || len(right) < frameLength {
		return nil, nil, StereoQuantIndices{}, false, 0, 0, 0
	}
	if len(left) > frameLength {
		left = left[:frameLength]
	}
	if len(right) > frameLength {
		right = right[:frameLength]
	}

	// Convert to mid/side directly in int16 arithmetic, matching libopus
	// stereo_LR_to_MS.c. Building float mid/side first and rounding later can
	// perturb tiny packet-0 predictor values enough to change the coded indices.
	msLen := frameLength + 2
	midQ0 := ensureInt16Slice(&e.scratchStereoMidQ0, msLen)
	sideQ0 := ensureInt16Slice(&e.scratchStereoSideQ0, msLen)
	midQ0[0] = e.stereo.sMid[0]
	midQ0[1] = e.stereo.sMid[1]
	sideQ0[0] = e.stereo.sSide[0]
	sideQ0[1] = e.stereo.sSide[1]
	for n := range frameLength {
		l := int32(float32ToInt16(left[n]))
		r := int32(float32ToInt16(right[n]))
		midQ0[n+2] = int16(silkRSHIFT_ROUND(l+r, 1))
		sideQ0[n+2] = silkSAT16(silkRSHIFT_ROUND(l-r, 1))
	}
	if frameLength >= 2 {
		e.stereo.sMid[0] = midQ0[frameLength]
		e.stereo.sMid[1] = midQ0[frameLength+1]
		e.stereo.sSide[0] = sideQ0[frameLength]
		e.stereo.sSide[1] = sideQ0[frameLength+1]
	} else if frameLength == 1 {
		e.stereo.sMid[0] = e.stereo.sMid[1]
		e.stereo.sSide[0] = e.stereo.sSide[1]
		e.stereo.sMid[1] = midQ0[2]
		e.stereo.sSide[1] = sideQ0[2]
	}

	lpMidQ0 := ensureInt16Slice(&e.scratchStereoLPMidQ0, frameLength)
	hpMidQ0 := ensureInt16Slice(&e.scratchStereoHPMidQ0, frameLength)
	stereoLPFilterInto(midQ0, lpMidQ0, hpMidQ0, frameLength)
	lpSideQ0 := ensureInt16Slice(&e.scratchStereoLPSideQ0, frameLength)
	hpSideQ0 := ensureInt16Slice(&e.scratchStereoHPSideQ0, frameLength)
	stereoLPFilterInto(sideQ0, lpSideQ0, hpSideQ0, frameLength)

	// Predictor smoothing coefficient (Q16), matching stereo_LR_to_MS.c.
	smoothCoefQ16 := int32(silkFixConst(stereoRatioSmoothCoef, 16))
	if frameLength == 10*fsKHz {
		smoothCoefQ16 = int32(silkFixConst(stereoRatioSmoothCoef/2.0, 16))
	}
	smoothCoefQ16 = silkSMULWB(silkSMULBB(prevSpeechActQ8, prevSpeechActQ8), smoothCoefQ16)
	lpMidResQ0 := [2]int32{
		e.stereo.midSideAmpQ0[0],
		e.stereo.midSideAmpQ0[1],
	}
	hpMidResQ0 := [2]int32{
		e.stereo.midSideAmpQ0[2],
		e.stereo.midSideAmpQ0[3],
	}

	predLP, ratioLPQ14 := stereoFindPredictorQ13WithRatioQ14(lpMidQ0, lpSideQ0, frameLength, &lpMidResQ0, smoothCoefQ16)
	predHP, ratioHPQ14 := stereoFindPredictorQ13WithRatioQ14(hpMidQ0, hpSideQ0, frameLength, &hpMidResQ0, smoothCoefQ16)

	e.stereo.midSideAmpQ0[0], e.stereo.midSideAmpQ0[1] = lpMidResQ0[0], lpMidResQ0[1]
	e.stereo.midSideAmpQ0[2], e.stereo.midSideAmpQ0[3] = hpMidResQ0[0], hpMidResQ0[1]

	predQ13 := [2]int32{predLP, predHP}

	// frac = min(1, HP_ratio + 3*LP_ratio), in Q16 for libopus parity.
	// stereoFindPredictorQ13WithRatioQ14 clamps each ratio to [0, 32767], so the
	// sum is non-negative; libopus stereo_LR_to_MS.c applies only the upper clamp.
	fracQ16 := silkSMLABB(ratioHPQ14, ratioLPQ14, 3)
	if fracQ16 > (1 << 16) {
		fracQ16 = 1 << 16
	}
	// Rate split and width decision.
	total := totalRateBps
	// Match libopus stereo_LR_to_MS.c: reserve bits for stereo side-info
	// before computing mid/side allocation.
	if frameLength == 10*fsKHz {
		total -= 1200
	} else {
		total -= 600
	}
	if total < 1 {
		total = 1
	}
	minMidRate := 2000 + fsKHz*600
	frac3Q16 := silkMUL(3, fracQ16)
	midRate = int(silkDiv32VarQ(int32(total), int32(silkFixConst(8+5, 16))+frac3Q16, 16+3))
	if midRate < minMidRate {
		midRate = minMidRate
		sideRate = total - midRate
		widthQ14Raw := min(max(silkDiv32VarQ(
			silkLSHIFT(int32(sideRate), 1)-int32(minMidRate),
			silkSMULWB(int32(silkFixConst(1, 16))+frac3Q16, int32(minMidRate)),
			14+2,
		), 0), int32(silkFixConst(1, 14)))
		widthQ14 = int16(widthQ14Raw)
	} else {
		sideRate = total - midRate
		widthQ14 = 16384
	}

	// Match libopus stereo_LR_to_MS.c smoother and predictor scaling in fixed-point.
	e.stereo.smthWidthQ14 = int16(silkSMLAWB(int32(e.stereo.smthWidthQ14), int32(widthQ14)-int32(e.stereo.smthWidthQ14), smoothCoefQ16))
	scalePredBySmthWidth := func() {
		smthWidthQ14 := int32(e.stereo.smthWidthQ14)
		predQ13[0] = silkRSHIFT(silkSMULBB(smthWidthQ14, predQ13[0]), 14)
		predQ13[1] = silkRSHIFT(silkSMULBB(smthWidthQ14, predQ13[1]), 14)
	}
	fracSmthQ14 := silkSMULWB(fracQ16, int32(e.stereo.smthWidthQ14))

	// Mid-only / width decisions.
	midOnly = false
	if toMono {
		widthQ14 = 0
		predQ13[0], predQ13[1] = 0, 0
	} else if e.stereo.widthPrevQ14 == 0 && (8*total < 13*minMidRate || fracSmthQ14 < int32(silkFixConst(0.05, 14))) {
		scalePredBySmthWidth()
		midOnly = true
		widthQ14 = 0
		midRate = total
		sideRate = 0
	} else if e.stereo.widthPrevQ14 != 0 && (8*total < 11*minMidRate || fracSmthQ14 < int32(silkFixConst(0.02, 14))) {
		scalePredBySmthWidth()
		widthQ14 = 0
	} else if e.stereo.smthWidthQ14 > int16(silkFixConst(0.95, 14)) {
		widthQ14 = 16384
	} else {
		scalePredBySmthWidth()
		widthQ14 = e.stereo.smthWidthQ14
	}

	// Quantize predictors (returns delta-coded predQ13).
	predQ13, ix = QuantizeStereoWeights(predQ13)
	// Handle mid-only persistence.
	if midOnly {
		silentSideLen := int32(e.stereo.silentSideLen) + int32(frameLength-stereoInterpLenMs*fsKHz)
		if silentSideLen < int32(laShapeMs*fsKHz) {
			midOnly = false
		} else {
			silentSideLen = 10000
		}
		e.stereo.silentSideLen = int16(silentSideLen)
	} else {
		e.stereo.silentSideLen = 0
	}

	if !midOnly && sideRate < 1 {
		sideRate = 1
		if total > 1 {
			midRate = total - sideRate
		}
	}

	// Match libopus stereo_LR_to_MS.c: in the collapsed-width branches we still
	// signal the quantized predictor indices, but the side transform itself uses
	// zero predictors once width is forced to zero.
	if widthQ14 == 0 {
		predQ13[0], predQ13[1] = 0, 0
	}

	// Interpolate predictors and subtract prediction from side channel.
	// Matches libopus stereo_LR_to_MS.c lines 198-224.
	//
	// In libopus, mid = &x1[-2], so mid[n+1] = inputBuf[n+1].
	// The SILK encoder reads from inputBuf[1] for frame_length samples,
	// i.e., inputBuf[1..frame_length] = mid[1..frameLength].
	// The prediction loop writes x2[n-1] for n=0..frame_length-1.
	// x2 = &inputBuf[2], so x2[n-1] = inputBuf[n+1]. The encoder reads
	// inputBuf[1..frame_length] for side too, which is x2[-1..frame_length-2] =
	// prediction at n=0..frame_length-1.
	// Our mid array: mid[0..1] = history, mid[2..frameLength+1] = current frame.
	// midOut[n] = mid[n+1] gives mid[1..frameLength] which matches inputBuf[1..frame_length].
	// sideOut[n] = prediction at n, which matches inputBuf[n+1] = x2[n-1].
	//
	// sumQ11 scaling: In libopus, mid values are int16 and the LP filter
	// sum = (mid[n]+2*mid[n+1]+mid[n+2]) << 9 produces Q11 in int32.
	// Our float mid values are in [-1,1] (= int16/32768), so we convert to
	// int16 scale first (multiply by 32768), then apply the << 9 shift.
	midOut = ensureFloat32Slice(&e.scratchStereoMidOut, frameLength)
	sideOut = ensureFloat32Slice(&e.scratchStereoSideOut, frameLength)
	for n := range frameLength {
		midOut[n] = float32(midQ0[n+1]) / 32768.0
	}

	pred0Q13 := -int32(e.stereo.predPrevQ13[0])
	pred1Q13 := -int32(e.stereo.predPrevQ13[1])
	wQ24 := int32(e.stereo.widthPrevQ14) << 10
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(e.stereo.predPrevQ13[0]), denomQ16), 16)
	delta1Q13 := -silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(e.stereo.predPrevQ13[1]), denomQ16), 16)
	deltawQ24 := int32(silkSMULWB(int32(widthQ14)-int32(e.stereo.widthPrevQ14), denomQ16)) << 10

	interpSamples := stereoInterpLenMs * fsKHz
	for n := 0; n < interpSamples && n < frameLength; n++ {
		pred0Q13 += delta0Q13
		pred1Q13 += delta1Q13
		wQ24 += deltawQ24

		sumQ11 := (int32(midQ0[n]) + int32(midQ0[n+2]) + (int32(midQ0[n+1]) << 1)) << 9
		sideQ8 := silkSMULWB(wQ24, int32(sideQ0[n+1]))
		sideQ8 = silkSMLAWB(sideQ8, sumQ11, pred0Q13)
		sideQ8 = silkSMLAWB(sideQ8, int32(midQ0[n+1])<<11, pred1Q13)
		sideOut[n] = float32(silkSAT16(silkRSHIFT_ROUND(sideQ8, 8))) / 32768.0
	}

	pred0Q13 = -predQ13[0]
	pred1Q13 = -predQ13[1]
	wQ24 = int32(widthQ14) << 10
	for n := interpSamples; n < frameLength; n++ {
		sumQ11 := (int32(midQ0[n]) + int32(midQ0[n+2]) + (int32(midQ0[n+1]) << 1)) << 9
		sideQ8 := silkSMULWB(wQ24, int32(sideQ0[n+1]))
		sideQ8 = silkSMLAWB(sideQ8, sumQ11, pred0Q13)
		sideQ8 = silkSMLAWB(sideQ8, int32(midQ0[n+1])<<11, pred1Q13)
		sideOut[n] = float32(silkSAT16(silkRSHIFT_ROUND(sideQ8, 8))) / 32768.0
	}

	// Update state for next frame.
	e.stereo.predPrevQ13[0] = int16(predQ13[0])
	e.stereo.predPrevQ13[1] = int16(predQ13[1])
	e.stereo.widthPrevQ14 = widthQ14
	e.prevStereoWeights[0] = int16(predQ13[0] + predQ13[1])
	e.prevStereoWeights[1] = int16(predQ13[1])

	return midOut, sideOut, ix, midOnly, midRate, sideRate, widthQ14
}

// EncodeStereoLRToMS is the public method that matches libopus stereo_LR_to_MS.
// It converts stereo L/R to M/S with proper LP filtering for predictor analysis.
// This should be used instead of EncodeStereoMidSide for proper libopus compatibility.
//
// Parameters:
//   - left, right: input stereo signals
//   - frameLength: number of samples (e.g., 160 for 10ms at 16kHz)
//   - fsKHz: sample rate in kHz
//
// Returns:
//   - mid, side: output mid/side signals (with 2 history samples prepended)
//   - predQ13: prediction coefficients [0]=LP predictor, [1]=HP predictor
func (e *Encoder) EncodeStereoLRToMS(left, right []float32, frameLength, fsKHz int) (mid, side []float32, predQ13 [2]int32) {
	return e.encodeStereoWithLPFilter(left, right, frameLength, fsKHz)
}

// ResetStereoState resets the stereo encoder state.
// Call this when starting a new stream.
func (e *Encoder) ResetStereoState() {
	resetStereoEncState(&e.stereo)
	e.prevStereoWeights = [2]int16{0, 0}
}

// InterpolatePredictorsFloat applies 8ms smooth predictor interpolation using float arithmetic.
// This is a cleaner implementation for testing and understanding the interpolation algorithm.
//
// The function linearly interpolates from prevPred to currPred over the first
// interpSamples (8ms * fsKHz), then uses currPred for the remainder.
//
// Parameters:
//   - prevPred: previous frame's predictor [LP, HP] as float
//   - currPred: current frame's predictor [LP, HP] as float
//   - prevWidth: previous frame's stereo width (0.0-1.0)
//   - currWidth: current frame's stereo width (0.0-1.0)
//   - mid: mid channel samples (length frameLength+2, with history)
//   - side: side channel samples (length frameLength+2, with history)
//   - frameLength: number of output samples
//   - fsKHz: sample rate in kHz
//
// Returns:
//   - sideOut: side channel with interpolated prediction applied
func InterpolatePredictorsFloat(prevPred, currPred [2]float32, prevWidth, currWidth float32,
	mid, side []float32, frameLength, fsKHz int) []float32 {

	sideOut := make([]float32, frameLength)
	interpSamples := stereoInterpLenMs * fsKHz

	// Interpolation region (first 8ms)
	for n := 0; n < interpSamples && n < frameLength; n++ {
		// Linear interpolation factor: t goes from 0 to 1 over interpSamples
		// But we increment after using, so at sample 0, t = 1/interpSamples
		t := float32(n+1) / float32(interpSamples)

		// Interpolate predictors and width
		pred0 := prevPred[0] + t*(currPred[0]-prevPred[0])
		pred1 := prevPred[1] + t*(currPred[1]-prevPred[1])
		width := prevWidth + t*(currWidth-prevWidth)

		// LP-filtered mid: (mid[n] + 2*mid[n+1] + mid[n+2]) / 4
		lpMid := (mid[n] + 2*mid[n+1] + mid[n+2]) / 4.0

		// Apply prediction to side channel:
		// side' = width * side - pred0 * lpMid - pred1 * mid[n+1]
		// The minus signs match libopus where pred is negated in the loop
		sideOut[n] = width*side[n+1] - pred0*lpMid - pred1*mid[n+1]
	}

	// Remainder (after 8ms) - use final predictor values
	for n := interpSamples; n < frameLength; n++ {
		// LP-filtered mid
		lpMid := (mid[n] + 2*mid[n+1] + mid[n+2]) / 4.0

		// Apply final prediction
		sideOut[n] = currWidth*side[n+1] - currPred[0]*lpMid - currPred[1]*mid[n+1]
	}

	return sideOut
}

// StereoEncStateInterp holds state for encoder-side 8ms predictor interpolation.
// This is a simplified float-based state for easier integration.
type StereoEncStateInterp struct {
	PrevPredQ13  [2]int32 // Previous frame's predictors in Q13
	PrevWidthQ14 int16    // Previous frame's stereo width in Q14
	SMid         [2]int16 // Mid signal history buffer
	SSide        [2]int16 // Side signal history buffer
}

// Reset clears the interpolation state for a new stream.
func (s *StereoEncStateInterp) Reset() {
	s.PrevPredQ13 = [2]int32{0, 0}
	s.PrevWidthQ14 = 0
	s.SMid = [2]int16{0, 0}
	s.SSide = [2]int16{0, 0}
}

// ApplyInterpolation applies 8ms predictor interpolation to transform the side channel.
// This updates the state with current predictor values for the next frame.
//
// Parameters:
//   - currPredQ13: current frame's predictors [LP, HP] in Q13
//   - currWidthQ14: current frame's stereo width in Q14 (16384 = full width)
//   - mid, side: mid/side channels with 2 history samples prepended
//   - frameLength: number of output samples
//   - fsKHz: sample rate in kHz
//
// Returns:
//   - sideOut: transformed side channel (length frameLength)
func (s *StereoEncStateInterp) ApplyInterpolation(currPredQ13 [2]int32, currWidthQ14 int16,
	mid, side []float32, frameLength, fsKHz int) []float32 {

	// Convert Q13 predictors to float for cleaner math
	prevPred := [2]float32{
		float32(s.PrevPredQ13[0]) / 8192.0,
		float32(s.PrevPredQ13[1]) / 8192.0,
	}
	currPred := [2]float32{
		float32(currPredQ13[0]) / 8192.0,
		float32(currPredQ13[1]) / 8192.0,
	}
	prevWidth := float32(s.PrevWidthQ14) / 16384.0
	currWidth := float32(currWidthQ14) / 16384.0

	// Apply interpolation
	sideOut := InterpolatePredictorsFloat(prevPred, currPred, prevWidth, currWidth,
		mid, side, frameLength, fsKHz)

	// Update state for next frame
	s.PrevPredQ13 = currPredQ13
	s.PrevWidthQ14 = currWidthQ14

	return sideOut
}

// GetInterpolationState returns the current interpolation state from the encoder.
// This allows external code to track the interpolation state.
func (e *Encoder) GetInterpolationState() (predPrevQ13 [2]int16, widthPrevQ14 int16) {
	return e.stereo.predPrevQ13, e.stereo.widthPrevQ14
}

// SetInterpolationState sets the interpolation state for the encoder.
// This is useful for testing or when restoring encoder state.
func (e *Encoder) SetInterpolationState(predPrevQ13 [2]int16, widthPrevQ14 int16) {
	e.stereo.predPrevQ13 = predPrevQ13
	e.stereo.widthPrevQ14 = widthPrevQ14
}
