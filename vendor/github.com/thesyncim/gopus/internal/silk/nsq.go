// Package silk implements SILK Noise Shaping Quantization (NSQ).
// Reference: libopus silk/NSQ.c and silk/NSQ_del_dec.c
package silk

// NSQ constants from libopus define.h
const (
	nsqLpcBufLength   = 16  // NSQ_LPC_BUF_LENGTH = MAX_LPC_ORDER
	maxShapeLpcOrder  = 24  // MAX_SHAPE_LPC_ORDER
	harmShapeFirTaps  = 3   // HARM_SHAPE_FIR_TAPS
	ltpOrderConst     = 5   // LTP_ORDER
	decisionDelay     = 40  // DECISION_DELAY
	quantLevelAdjQ10  = 80  // QUANT_LEVEL_ADJUST_Q10
	offsetVLQ10       = 32  // OFFSET_VL_Q10
	offsetVHQ10       = 100 // OFFSET_VH_Q10
	offsetUVLQ10      = 100 // OFFSET_UVL_Q10
	offsetUVHQ10      = 240 // OFFSET_UVH_Q10
	maxFrameLengthNSQ = 320 // MAX_FRAME_LENGTH for 20ms at 16kHz
	ltpMemLength      = 320 // LTP_MEM_LENGTH = 20ms * 16kHz
)

// NSQState holds the noise shaping quantizer state.
// Mirrors libopus silk_nsq_state structure.
type NSQState struct {
	// Buffer for quantized output signal
	xq [2 * maxFrameLengthNSQ]int16

	// Long-term shaping state (Q14)
	sLTPShpQ14 [2 * maxFrameLengthNSQ]int32

	// Short-term LPC state (Q14)
	sLPCQ14 [maxSubFrameLength + nsqLpcBufLength]int32

	// AR noise shaping state (Q14)
	sAR2Q14 [maxShapeLpcOrder]int32

	// Low-frequency AR shaping state (Q14)
	sLFARShpQ14 int32

	// Difference shaping state (Q14)
	sDiffShpQ14 int32

	// Previous pitch lag (libopus int domain)
	lagPrev int32

	// LTP buffer index
	sLTPBufIdx int

	// LTP shaping buffer index
	sLTPShpBufIdx int

	// Random seed for dithering
	randSeed int32

	// Previous gain (Q16)
	prevGainQ16 int32

	// Rewhitening flag
	rewhiteFlag int

	// Pre-allocated scratch buffers for zero-allocation encoding
	scratchPulses  []int8  // Size: maxFrameLengthNSQ = 320
	scratchXq      []int16 // Size: maxFrameLengthNSQ = 320
	scratchSLTPQ15 []int32 // Size: ltpMemLength + maxFrameLengthNSQ = 640
	scratchSLTP    []int16 // Size: ltpMemLength + maxFrameLengthNSQ = 640
	scratchXScQ10  []int32 // Size: maxSubFrameLength = 80

	// Delayed decision states (NSQ_del_dec)
	delDecStates [maxDelDecStates]nsqDelDecState
}

// NewNSQState creates a new NSQ state with proper initialization.
func NewNSQState() *NSQState {
	state := &NSQState{
		prevGainQ16: 1 << 16, // 1.0 in Q16, matches libopus initialization
		// Pre-allocate scratch buffers for zero-allocation encoding
		scratchPulses:  make([]int8, maxFrameLengthNSQ),
		scratchXq:      make([]int16, maxFrameLengthNSQ),
		scratchSLTPQ15: make([]int32, ltpMemLength+maxFrameLengthNSQ),
		scratchSLTP:    make([]int16, ltpMemLength+maxFrameLengthNSQ),
		scratchXScQ10:  make([]int32, maxSubFrameLength),
	}
	return state
}

// Clone creates a deep copy of the NSQ state.
func (s *NSQState) Clone() *NSQState {
	c := &NSQState{
		lagPrev:       s.lagPrev,
		sLTPBufIdx:    s.sLTPBufIdx,
		sLTPShpBufIdx: s.sLTPShpBufIdx,
		randSeed:      s.randSeed,
		prevGainQ16:   s.prevGainQ16,
		rewhiteFlag:   s.rewhiteFlag,
	}
	copy(c.xq[:], s.xq[:])
	copy(c.sLTPShpQ14[:], s.sLTPShpQ14[:])
	copy(c.sLPCQ14[:], s.sLPCQ14[:])
	copy(c.sAR2Q14[:], s.sAR2Q14[:])
	c.delDecStates = s.delDecStates
	c.sLFARShpQ14 = s.sLFARShpQ14
	c.sDiffShpQ14 = s.sDiffShpQ14

	// Don't need to copy scratch buffers as they are transient per call
	c.scratchPulses = make([]int8, len(s.scratchPulses))
	c.scratchXq = make([]int16, len(s.scratchXq))
	c.scratchSLTPQ15 = make([]int32, len(s.scratchSLTPQ15))
	c.scratchSLTP = make([]int16, len(s.scratchSLTP))
	c.scratchXScQ10 = make([]int32, len(s.scratchXScQ10))

	return c
}

// RestoreFrom copies state from another NSQState.
func (s *NSQState) RestoreFrom(other *NSQState) {
	s.lagPrev = other.lagPrev
	s.sLTPBufIdx = other.sLTPBufIdx
	s.sLTPShpBufIdx = other.sLTPShpBufIdx
	s.randSeed = other.randSeed
	s.prevGainQ16 = other.prevGainQ16
	s.rewhiteFlag = other.rewhiteFlag
	copy(s.xq[:], other.xq[:])
	copy(s.sLTPShpQ14[:], other.sLTPShpQ14[:])
	copy(s.sLPCQ14[:], other.sLPCQ14[:])
	copy(s.sAR2Q14[:], other.sAR2Q14[:])
	s.delDecStates = other.delDecStates
	s.sLFARShpQ14 = other.sLFARShpQ14
	s.sDiffShpQ14 = other.sDiffShpQ14
}

// Reset clears the NSQ state for a new stream.
func (s *NSQState) Reset() {
	for i := range s.xq {
		s.xq[i] = 0
	}
	for i := range s.sLTPShpQ14 {
		s.sLTPShpQ14[i] = 0
	}
	for i := range s.sLPCQ14 {
		s.sLPCQ14[i] = 0
	}
	for i := range s.sAR2Q14 {
		s.sAR2Q14[i] = 0
	}
	s.delDecStates = [maxDelDecStates]nsqDelDecState{}
	s.sLFARShpQ14 = 0
	s.sDiffShpQ14 = 0
	s.lagPrev = 0
	s.sLTPBufIdx = 0
	s.sLTPShpBufIdx = 0
	s.randSeed = 0
	s.prevGainQ16 = 1 << 16 // 1.0 in Q16, matches libopus initialization
	s.rewhiteFlag = 0
}

// NSQParams holds parameters for noise shaping quantization.
type NSQParams struct {
	// Signal type: 0=inactive, 1=unvoiced, 2=voiced
	SignalType int

	// Quantization offset type: 0=low, 1=high
	QuantOffsetType int

	// LPC prediction coefficients (Q12)
	PredCoefQ12 []int16

	// NLSF interpolation coefficient (Q2). 4 means no interpolation.
	NLSFInterpCoefQ2 int

	// LTP coefficients (Q14), 5 per subframe
	LTPCoefQ14 []int16

	// Noise shaping AR coefficients (Q13)
	ARShpQ13 []int16

	// Harmonic shaping gain (Q14)
	HarmShapeGainQ14 []int32

	// Spectral tilt (Q14)
	TiltQ14 []int32

	// Low-frequency shaping (Q14)
	LFShpQ14 []int32

	// Quantization gains (Q16) per subframe
	GainsQ16 []int32

	// Pitch lags per subframe
	PitchL []int32

	// Rate/distortion tradeoff (Lambda, Q10)
	LambdaQ10 int32

	// LTP scale (Q14) for first subframe
	LTPScaleQ14 int32

	// Frame configuration
	FrameLength            int
	SubfrLength            int
	NbSubfr                int
	LTPMemLength           int
	PredLPCOrder           int
	ShapeLPCOrder          int
	WarpingQ16             int
	NStatesDelayedDecision int

	// LCG seed for dithering
	Seed int
}

// NoiseShapeQuantize performs noise shaping quantization on input samples.
// This is the main NSQ function matching libopus silk_NSQ_c.
//
// Parameters:
//   - nsq: NSQ state (modified)
//   - input: Input signal (Q0, scaled)
//   - params: NSQ parameters
//
// Returns:
//   - pulses: Quantized pulse signal (int8 per sample)
//   - xq: Quantized/reconstructed signal
func NoiseShapeQuantize(nsq *NSQState, input []int16, params *NSQParams) ([]int8, []int16) {
	frameLength := params.FrameLength
	subfrLength := params.SubfrLength
	nbSubfr := params.NbSubfr
	ltpMemLength := params.LTPMemLength

	// Initialize random seed
	nsq.randSeed = int32(params.Seed)

	// Get quantization offset
	offsetQ10 := getQuantizationOffset(params.SignalType, params.QuantOffsetType)

	// Set unvoiced lag to previous, overwrite for voiced
	lag := int(nsq.lagPrev)

	// Use pre-allocated output buffers if available, otherwise allocate
	var pulses []int8
	if nsq.scratchPulses != nil && len(nsq.scratchPulses) >= frameLength {
		pulses = nsq.scratchPulses[:frameLength]
		for i := range pulses {
			pulses[i] = 0
		}
	} else {
		pulses = make([]int8, frameLength)
	}

	var xq []int16
	xqStart := ltpMemLength
	xqEnd := ltpMemLength + frameLength
	if xqEnd <= len(nsq.xq) {
		// Use state buffer so LTP rewhitening and history update work correctly.
		xq = nsq.xq[xqStart:xqEnd]
		for i := range xq {
			xq[i] = 0
		}
	} else if nsq.scratchXq != nil && len(nsq.scratchXq) >= frameLength {
		// Fallback to scratch if frame length exceeds state buffer.
		xq = nsq.scratchXq[:frameLength]
		for i := range xq {
			xq[i] = 0
		}
	} else {
		xq = make([]int16, frameLength)
	}

	// Use pre-allocated working buffers if available
	var sLTPQ15 []int32
	if nsq.scratchSLTPQ15 != nil && len(nsq.scratchSLTPQ15) >= ltpMemLength+frameLength {
		sLTPQ15 = nsq.scratchSLTPQ15[:ltpMemLength+frameLength]
		for i := range sLTPQ15 {
			sLTPQ15[i] = 0
		}
	} else {
		sLTPQ15 = make([]int32, ltpMemLength+frameLength)
		nsq.scratchSLTPQ15 = sLTPQ15
	}

	var sLTP []int16
	if nsq.scratchSLTP != nil && len(nsq.scratchSLTP) >= ltpMemLength+frameLength {
		sLTP = nsq.scratchSLTP[:ltpMemLength+frameLength]
		for i := range sLTP {
			sLTP[i] = 0
		}
	} else {
		sLTP = make([]int16, ltpMemLength+frameLength)
		nsq.scratchSLTP = sLTP
	}

	var xScQ10 []int32
	if nsq.scratchXScQ10 != nil && len(nsq.scratchXScQ10) >= subfrLength {
		xScQ10 = nsq.scratchXScQ10[:subfrLength]
		for i := range xScQ10 {
			xScQ10[i] = 0
		}
	} else {
		xScQ10 = make([]int32, subfrLength)
		nsq.scratchXScQ10 = xScQ10
	}

	// Check LSF interpolation
	lsfInterpFlag := 1
	if params.NLSFInterpCoefQ2 == 4 {
		lsfInterpFlag = 0
	}

	// Set up pointers
	nsq.sLTPShpBufIdx = ltpMemLength
	nsq.sLTPBufIdx = ltpMemLength

	// Process each subframe
	for k := range nbSubfr {
		// Get coefficients for this subframe
		predCoefIdx := ((k >> 1) | (1 - lsfInterpFlag)) * maxLPCOrder
		aQ12 := params.PredCoefQ12[predCoefIdx : predCoefIdx+params.PredLPCOrder]
		bQ14 := params.LTPCoefQ14[k*ltpOrderConst : (k+1)*ltpOrderConst]
		arShpQ13 := params.ARShpQ13[k*maxShapeLpcOrder : (k+1)*maxShapeLpcOrder]

		// Pack harmonic shape FIR coefficients
		harmShapeFIRPackedQ14 := (params.HarmShapeGainQ14[k] >> 2) |
			((params.HarmShapeGainQ14[k] >> 1) << 16)

		nsq.rewhiteFlag = 0
		if params.SignalType == typeVoiced {
			lag = int(params.PitchL[k])

			// Re-whitening for voiced frames at specific subframes
			if (k & (3 - (lsfInterpFlag << 1))) == 0 {
				// Compute start index for rewhitening
				startIdx := max(ltpMemLength-lag-params.PredLPCOrder-ltpOrderConst/2, 0)

				// Rewhiten with LPC analysis filter
				rewhitenLTP(sLTP, nsq.xq[:], startIdx, k*subfrLength, aQ12, ltpMemLength-startIdx, params.PredLPCOrder)

				nsq.rewhiteFlag = 1
				nsq.sLTPBufIdx = ltpMemLength
			}
		}

		// Scale states
		scaleNSQStates(nsq, input[k*subfrLength:], xScQ10, sLTP, sLTPQ15,
			k, params.LTPScaleQ14, params.GainsQ16, params.PitchL,
			params.SignalType, subfrLength, ltpMemLength)

		// Noise shape quantizer for this subframe
		noiseShapeQuantizerSubframe(
			nsq,
			params.SignalType,
			xScQ10,
			pulses[k*subfrLength:(k+1)*subfrLength],
			xq[k*subfrLength:(k+1)*subfrLength],
			sLTPQ15,
			aQ12,
			bQ14,
			arShpQ13[:params.ShapeLPCOrder],
			lag,
			harmShapeFIRPackedQ14,
			params.TiltQ14[k],
			params.LFShpQ14[k],
			params.GainsQ16[k],
			params.LambdaQ10,
			offsetQ10,
			subfrLength,
			params.ShapeLPCOrder,
			params.PredLPCOrder,
		)
	}

	// Copy reconstructed samples to scratch BEFORE shifting state.
	outXQ := nsq.scratchXq
	if len(outXQ) < frameLength {
		outXQ = make([]int16, frameLength)
		nsq.scratchXq = outXQ
	}
	outXQ = outXQ[:frameLength]
	copy(outXQ, nsq.xq[ltpMemLength:ltpMemLength+frameLength])

	// Update state for next frame
	nsq.lagPrev = params.PitchL[nbSubfr-1]

	// Shift buffers
	copy(nsq.xq[:ltpMemLength], nsq.xq[frameLength:frameLength+ltpMemLength])
	copy(nsq.sLTPShpQ14[:ltpMemLength], nsq.sLTPShpQ14[frameLength:frameLength+ltpMemLength])

	return pulses, outXQ
}

// noiseShapeQuantizerSubframe quantizes one subframe with noise shaping.
// Matches libopus silk_noise_shape_quantizer.
func noiseShapeQuantizerSubframe(
	nsq *NSQState,
	signalType int,
	xScQ10 []int32,
	pulses []int8,
	xq []int16,
	sLTPQ15 []int32,
	aQ12 []int16,
	bQ14 []int16,
	arShpQ13 []int16,
	lag int,
	harmShapeFIRPackedQ14 int32,
	tiltQ14 int32,
	lfShpQ14 int32,
	gainQ16 int32,
	lambdaQ10 int32,
	offsetQ10 int,
	length int,
	shapingLPCOrder int,
	predictLPCOrder int,
) {
	// Get pointers into LTP buffers
	shpLagPtr := nsq.sLTPShpBufIdx - lag + harmShapeFirTaps/2
	predLagPtr := nsq.sLTPBufIdx - lag + ltpOrderConst/2

	gainQ10 := gainQ16 >> 6

	// Set up short-term AR state pointer
	psLPCQ14Idx := nsqLpcBufLength - 1

	for i := range length {
		// Generate dither
		nsq.randSeed = silk_RAND(nsq.randSeed)

		// Short-term prediction (LPC)
		lpcPredQ10 := shortTermPrediction(nsq.sLPCQ14[:], psLPCQ14Idx, aQ12, predictLPCOrder)

		// Long-term prediction (LTP) for voiced
		var ltpPredQ13 int32
		if signalType == typeVoiced && predLagPtr >= 0 && predLagPtr < len(sLTPQ15) {
			ltpPredQ13 = 2 // Rounding bias
			for j := range ltpOrderConst {
				idx := predLagPtr - j
				if idx >= 0 && idx < len(sLTPQ15) {
					ltpPredQ13 += silk_SMLAWB(0, sLTPQ15[idx], int32(bQ14[j]))
				}
			}
			predLagPtr++
		}

		// Noise shape feedback (AR filter)
		nARQ12 := noiseShapeFeedback(nsq.sDiffShpQ14, nsq.sAR2Q14[:], arShpQ13, shapingLPCOrder)

		// Add tilt component
		nARQ12 = silk_SMLAWB(nARQ12, nsq.sLFARShpQ14, int32(tiltQ14))

		// Low-frequency shaping
		nLFQ12 := int32(0)
		if nsq.sLTPShpBufIdx > 0 {
			nLFQ12 = silk_SMULWB(nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-1], lfShpQ14)
		}
		nLFQ12 = silk_SMLAWT(nLFQ12, nsq.sLFARShpQ14, lfShpQ14)

		// Combine prediction and noise shaping
		tmp1 := silk_SUB32(silk_LSHIFT32(lpcPredQ10, 2), nARQ12) // Q12
		tmp1 = silk_SUB32(tmp1, nLFQ12)                          // Q12

		var nLTPQ13 int32
		if lag > 0 && shpLagPtr >= 0 && shpLagPtr < len(nsq.sLTPShpQ14) {
			// Symmetric FIR for harmonic shaping
			shp0, shp1, shp2 := int32(0), int32(0), int32(0)
			if shpLagPtr >= 0 && shpLagPtr < len(nsq.sLTPShpQ14) {
				shp0 = nsq.sLTPShpQ14[shpLagPtr]
			}
			if shpLagPtr-1 >= 0 && shpLagPtr-1 < len(nsq.sLTPShpQ14) {
				shp1 = nsq.sLTPShpQ14[shpLagPtr-1]
			}
			if shpLagPtr-2 >= 0 && shpLagPtr-2 < len(nsq.sLTPShpQ14) {
				shp2 = nsq.sLTPShpQ14[shpLagPtr-2]
			}
			nLTPQ13 = silk_SMULWB(silk_ADD_SAT32(shp0, shp2), harmShapeFIRPackedQ14)
			nLTPQ13 = silk_SMLAWT(nLTPQ13, shp1, harmShapeFIRPackedQ14)
			nLTPQ13 = silk_LSHIFT32(nLTPQ13, 1)
			shpLagPtr++

			tmp2 := silk_SUB32(ltpPredQ13, nLTPQ13)         // Q13
			tmp1 = silk_ADD32(tmp2, silk_LSHIFT32(tmp1, 1)) // Q13
			tmp1 = silk_RSHIFT_ROUND(tmp1, 3)               // Q10
		} else {
			tmp1 = silk_RSHIFT_ROUND(tmp1, 2) // Q10
		}

		// Residual error
		rQ10 := silk_SUB32(xScQ10[i], tmp1)

		// Flip sign depending on dither
		if nsq.randSeed < 0 {
			rQ10 = -rQ10
		}

		// Limit range
		rQ10 = silk_LIMIT_32(rQ10, -(31 << 10), 30<<10)

		// Rate-distortion quantization
		q1Q10, q2Q10, rd1Q20, rd2Q20 := computeRDQuantization(rQ10, offsetQ10, lambdaQ10)

		// Compute distortion for both candidates
		rrQ10 := silk_SUB32(rQ10, q1Q10)
		rd1Q20 = silk_SMLABB(rd1Q20, rrQ10, rrQ10)
		rrQ10 = silk_SUB32(rQ10, q2Q10)
		rd2Q20 = silk_SMLABB(rd2Q20, rrQ10, rrQ10)

		// Select best quantization level
		if rd2Q20 < rd1Q20 {
			q1Q10 = q2Q10
		}

		// Store pulse
		pulses[i] = int8(silk_RSHIFT_ROUND(q1Q10, 10))

		// Compute excitation
		excQ14 := silk_LSHIFT32(q1Q10, 4)
		if nsq.randSeed < 0 {
			excQ14 = -excQ14
		}

		// Add predictions
		lpcExcQ14 := silk_ADD_LSHIFT32(excQ14, ltpPredQ13, 1)
		xqQ14 := silk_ADD32(lpcExcQ14, silk_LSHIFT32(lpcPredQ10, 4))

		// Scale back to output level (dequantize from Q14 to Q0)
		// Matches libopus: xq[i] = (int16)silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(xq_Q14, Gain_Q10), 8))
		xq[i] = int16(silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(xqQ14, gainQ10), 8)))

		// Update states
		psLPCQ14Idx++
		if psLPCQ14Idx < len(nsq.sLPCQ14) {
			nsq.sLPCQ14[psLPCQ14Idx] = xqQ14
		}

		nsq.sDiffShpQ14 = silk_SUB32(xqQ14, silk_LSHIFT32(xScQ10[i], 4))
		sLFARShpQ14 := silk_SUB32(nsq.sDiffShpQ14, silk_LSHIFT32(nARQ12, 2))
		nsq.sLFARShpQ14 = sLFARShpQ14

		if nsq.sLTPShpBufIdx < len(nsq.sLTPShpQ14) {
			nsq.sLTPShpQ14[nsq.sLTPShpBufIdx] = silk_SUB32(sLFARShpQ14, silk_LSHIFT32(nLFQ12, 2))
		}
		if nsq.sLTPBufIdx < len(sLTPQ15) {
			sLTPQ15[nsq.sLTPBufIdx] = silk_LSHIFT32(lpcExcQ14, 1)
		}

		nsq.sLTPShpBufIdx++
		nsq.sLTPBufIdx++

		// Update dither based on quantized signal
		nsq.randSeed = silk_ADD32(nsq.randSeed, int32(pulses[i]))
	}

	// Update LPC synth buffer
	copy(nsq.sLPCQ14[:nsqLpcBufLength], nsq.sLPCQ14[length:length+nsqLpcBufLength])
}

// shortTermPrediction computes LPC prediction.
// Matches libopus silk_noise_shape_quantizer_short_prediction_c.
func shortTermPrediction(sLPCQ14 []int32, idx int, aQ12 []int16, order int) int32 {
	switch order {
	case 16:
		return shortTermPrediction16(sLPCQ14, idx, aQ12)
	case 10:
		return shortTermPrediction10(sLPCQ14, idx, aQ12)
	default:
		out := int32(order >> 1)
		for k := 0; k < order && idx-k >= 0; k++ {
			out = silk_SMLAWB(out, sLPCQ14[idx-k], int32(aQ12[k]))
		}
		return out
	}
}

// shortTermPrediction16 and shortTermPrediction10 are implemented in:
//   - nsq_pred_arm64.s / nsq_pred_amd64.s (assembly, arm64 || amd64)
//   - nsq_pred_default.go (pure Go fallback, !arm64 && !amd64)

// noiseShapeFeedback computes AR noise shaping feedback.
// Matches libopus silk_NSQ_noise_shape_feedback_loop_c.
func noiseShapeFeedback(sDiffShpQ14 int32, sAR2Q14 []int32, arShpQ13 []int16, order int) int32 {
	tmp2 := sDiffShpQ14
	tmp1 := sAR2Q14[0]
	sAR2Q14[0] = tmp2

	out := int32(order >> 1)
	out = silk_SMLAWB(out, tmp2, int32(arShpQ13[0]))

	for j := 2; j < order; j += 2 {
		tmp2 = sAR2Q14[j-1]
		sAR2Q14[j-1] = tmp1
		out = silk_SMLAWB(out, tmp1, int32(arShpQ13[j-1]))

		tmp1 = sAR2Q14[j]
		sAR2Q14[j] = tmp2
		out = silk_SMLAWB(out, tmp2, int32(arShpQ13[j]))
	}

	if order > 0 {
		sAR2Q14[order-1] = tmp1
		out = silk_SMLAWB(out, tmp1, int32(arShpQ13[order-1]))
	}

	// Q11 -> Q12
	out = silk_LSHIFT32(out, 1)
	return out
}

// computeRDQuantization finds two quantization candidates with R-D cost.
func computeRDQuantization(rQ10 int32, offsetQ10 int, lambdaQ10 int32) (q1Q10, q2Q10, rd1Q20, rd2Q20 int32) {
	q1Q10 = silk_SUB32(rQ10, int32(offsetQ10))
	q1Q0 := silk_RSHIFT(q1Q10, 10)

	// For aggressive RDO, adjust bias
	if lambdaQ10 > 2048 {
		rdoOffset := lambdaQ10/2 - 512
		if q1Q10 > rdoOffset {
			q1Q0 = silk_RSHIFT(q1Q10-rdoOffset, 10)
		} else if q1Q10 < -rdoOffset {
			q1Q0 = silk_RSHIFT(q1Q10+rdoOffset, 10)
		} else if q1Q10 < 0 {
			q1Q0 = -1
		} else {
			q1Q0 = 0
		}
	}

	if q1Q0 > 0 {
		q1Q10 = silk_SUB32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
		q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
		q2Q10 = silk_ADD32(q1Q10, 1024)
		rd1Q20 = silk_SMULBB(q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(q2Q10, lambdaQ10)
	} else if q1Q0 == 0 {
		q1Q10 = int32(offsetQ10)
		q2Q10 = silk_ADD32(q1Q10, 1024-quantLevelAdjQ10)
		rd1Q20 = silk_SMULBB(q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(q2Q10, lambdaQ10)
	} else if q1Q0 == -1 {
		q2Q10 = int32(offsetQ10)
		q1Q10 = silk_SUB32(q2Q10, 1024-quantLevelAdjQ10)
		rd1Q20 = silk_SMULBB(-q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(q2Q10, lambdaQ10)
	} else {
		q1Q10 = silk_ADD32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
		q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
		q2Q10 = silk_ADD32(q1Q10, 1024)
		rd1Q20 = silk_SMULBB(-q1Q10, lambdaQ10)
		rd2Q20 = silk_SMULBB(-q2Q10, lambdaQ10)
	}

	return q1Q10, q2Q10, rd1Q20, rd2Q20
}

// scaleNSQStates scales NSQ states for gain changes.
// Matches libopus silk_nsq_scale_states.
func scaleNSQStates(
	nsq *NSQState,
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	subfr int,
	ltpScaleQ14 int32,
	gainsQ16 []int32,
	pitchL []int32,
	signalType int,
	subfrLength int,
	ltpMemLength int,
) {
	lag := int(pitchL[subfr])
	invGainQ31 := silk_INVERSE32_varQ(silk_max(gainsQ16[subfr], 1), 47)

	// Scale input (to Q10)
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	for i := 0; i < subfrLength && i < len(x16); i++ {
		// Matches libopus: x_sc_Q10[ i ] = silk_SMULWW( x16[ i ], inv_gain_Q26 )
		// x16 is Q0, invGainQ26 is Q26. SMULWW is (32*32)>>16.
		// So Q0 * Q26 >> 16 = Q10.
		xScQ10[i] = int32((int64(x16[i]) * int64(invGainQ26)) >> 16)
	}

	// After rewhitening, scale LTP state
	if nsq.rewhiteFlag != 0 {
		if subfr == 0 {
			// LTP downscaling for first subframe
			invGainQ31 = silk_LSHIFT32(silk_SMULWB(invGainQ31, ltpScaleQ14), 2)
		}
		startIdx := max(nsq.sLTPBufIdx-lag-ltpOrderConst/2, 0)
		for i := startIdx; i < nsq.sLTPBufIdx && i < len(sLTPQ15) && i < len(sLTP); i++ {
			sLTPQ15[i] = silk_SMULWB(invGainQ31, int32(sLTP[i]))
		}
	}

	// Adjust for changing gain
	if gainsQ16[subfr] != nsq.prevGainQ16 {
		gainAdjQ16 := silk_DIV32_varQ(nsq.prevGainQ16, gainsQ16[subfr], 16)

		// Scale long-term shaping state
		startIdx := max(nsq.sLTPShpBufIdx-ltpMemLength, 0)
		for i := startIdx; i < nsq.sLTPShpBufIdx && i < len(nsq.sLTPShpQ14); i++ {
			nsq.sLTPShpQ14[i] = silk_SMULWW(gainAdjQ16, nsq.sLTPShpQ14[i])
		}

		// Scale long-term prediction state
		if signalType == typeVoiced && nsq.rewhiteFlag == 0 {
			startIdx := max(nsq.sLTPBufIdx-lag-ltpOrderConst/2, 0)
			for i := startIdx; i < nsq.sLTPBufIdx && i < len(sLTPQ15); i++ {
				sLTPQ15[i] = silk_SMULWW(gainAdjQ16, sLTPQ15[i])
			}
		}

		nsq.sLFARShpQ14 = silk_SMULWW(gainAdjQ16, nsq.sLFARShpQ14)
		nsq.sDiffShpQ14 = silk_SMULWW(gainAdjQ16, nsq.sDiffShpQ14)

		// Scale short-term states
		for i := range nsqLpcBufLength {
			nsq.sLPCQ14[i] = silk_SMULWW(gainAdjQ16, nsq.sLPCQ14[i])
		}
		for i := range maxShapeLpcOrder {
			nsq.sAR2Q14[i] = silk_SMULWW(gainAdjQ16, nsq.sAR2Q14[i])
		}

		nsq.prevGainQ16 = gainsQ16[subfr]
	}
}

// rewhitenLTP applies LPC analysis filter for rewhitening.
// Matches libopus silk_LPC_analysis_filter behavior:
// - First 'order' outputs are set to zero
// - Remaining outputs computed as: out[ix] = in[ix] - sum(a[k] * in[ix-1-k])
func rewhitenLTP(sLTP []int16, xq []int16, startIdx, offset int, aQ12 []int16, length, order int) {
	// Set first 'order' outputs to zero (per libopus silk_LPC_analysis_filter)
	for i := startIdx; i < startIdx+order && i < len(sLTP); i++ {
		sLTP[i] = 0
	}

	// Compute LPC analysis filter for remaining samples
	// libopus iterates ix from d to len-1 and writes to out[ix]
	// Input pointer is in[ix-1], so it reads in[ix-1], in[ix-2], ..., in[ix-d]
	// Output is: in[ix] - prediction
	for ix := order; ix < length && startIdx+ix < len(sLTP); ix++ {
		inIdx := startIdx + offset + ix
		if inIdx < 0 || inIdx >= len(xq) {
			continue
		}

		// in_ptr = &in[ix-1] in libopus, so in_ptr[0] = in[ix-1], in_ptr[-1] = in[ix-2], etc.
		// Compute prediction: sum(a[k] * in[ix-1-k]) for k=0..order-1
		var predQ12 int32
		for k := range order {
			prevIdx := inIdx - 1 - k // in[ix-1-k]
			if prevIdx >= 0 && prevIdx < len(xq) {
				// silk_SMULBB: low 16 bits * low 16 bits
				predQ12 += int32(int16(aQ12[k])) * int32(xq[prevIdx])
			}
		}

		// Output = in[ix] - prediction, then scale from Q12 to Q0
		outQ12 := (int32(xq[inIdx]) << 12) - predQ12
		out := silk_RSHIFT_ROUND(outQ12, 12)

		// Saturate and store
		sLTP[startIdx+ix] = int16(silk_SAT16(out))
	}
}

// quantOffsets is a package-level constant table replacing the per-call [][]int literal.
// Per libopus: silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType].
var quantOffsets = [2][2]int{
	{offsetUVLQ10, offsetUVHQ10}, // Unvoiced (signalType 0, 1)
	{offsetVLQ10, offsetVHQ10},   // Voiced (signalType 2, 3)
}

// getQuantizationOffset returns the quantization offset based on signal type and offset type.
func getQuantizationOffset(signalType, quantOffsetType int) int {
	sigIdx := max(signalType>>1, 0)
	if sigIdx > 1 {
		sigIdx = 1
	}
	if quantOffsetType < 0 {
		quantOffsetType = 0
	}
	if quantOffsetType > 1 {
		quantOffsetType = 1
	}

	return quantOffsets[sigIdx][quantOffsetType]
}

// Fixed-point math helpers matching libopus SigProc_FIX.h

func silk_RAND(seed int32) int32 {
	// Linear congruential generator
	return seed*196314165 + 907633515
}

func silk_SMLAWB(a, b, c int32) int32 {
	// a + ((b * (int16)(c)) >> 16)
	// Per libopus: uses signed int16 extraction
	c16 := int16(c) // Extract low 16 bits as signed
	return a + int32((int64(b)*int64(c16))>>16)
}

func silk_SMLAWT(a, b, c int32) int32 {
	// a + ((b * (c >> 16)) >> 16)
	return a + int32((int64(b)*(int64(c)>>16))>>16)
}

func silk_SMULWB(a, b int32) int32 {
	// (a * (int16)(b)) >> 16
	// Per libopus: uses signed int16 extraction
	b16 := int16(b)
	return int32((int64(a) * int64(b16)) >> 16)
}

func silk_SMULWW(a, b int32) int32 {
	// Matches libopus silk_SMULWW: SMULWB(a, b) + a * RSHIFT_ROUND(b, 16).
	return silk_SMULWB(a, b) + a*silk_RSHIFT_ROUND(b, 16)
}

func silk_SMULBB(a, b int32) int32 {
	// Low 16-bit * low 16-bit (SIGNED extraction like libopus)
	// libopus: #define silk_SMULBB(a32, b32) ((opus_int32)((opus_int16)(a32)) * (opus_int32)((opus_int16)(b32)))
	return int32(int16(a)) * int32(int16(b))
}

func silk_SMLABB(a, b, c int32) int32 {
	return a + silk_SMULBB(b, c)
}

func silk_LSHIFT32(a int32, shift int) int32 {
	if shift < 0 {
		return a >> (-shift)
	}
	return a << shift
}

func silk_RSHIFT(a int32, shift int) int32 {
	if shift < 0 {
		return a << (-shift)
	}
	return a >> shift
}

func silk_RSHIFT_ROUND(a int32, shift int) int32 {
	if shift <= 0 {
		return a << (-shift)
	}
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}

func silk_ADD32(a, b int32) int32 {
	return a + b
}

func silk_SUB32(a, b int32) int32 {
	return a - b
}

func silk_ADD_SAT32(a, b int32) int32 {
	res := a + b
	if ((a ^ res) & (b ^ res)) < 0 {
		return int32((uint32(a) >> 31) + 0x7fffffff)
	}
	return res
}

func silk_SUB_SAT32(a, b int32) int32 {
	res := a - b
	if ((a ^ b) & (a ^ res)) < 0 {
		return int32((uint32(a) >> 31) + 0x7fffffff)
	}
	return res
}

func silk_ADD_LSHIFT32(a, b int32, shift int) int32 {
	return a + (b << shift)
}

func silk_SUB_LSHIFT32(a, b int32, shift int) int32 {
	return a - (b << shift)
}

func silk_ADD32_ovflw(a, b int32) int32 {
	return a + b
}

func silk_SUB32_ovflw(a, b int32) int32 {
	return a - b
}

func silk_LIMIT_32(val, minVal, maxVal int32) int32 {
	if minVal > maxVal {
		if val > minVal {
			return minVal
		}
		if val < maxVal {
			return maxVal
		}
		return val
	}
	if val > maxVal {
		return maxVal
	}
	if val < minVal {
		return minVal
	}
	return val
}

func silk_SAT16(a int32) int32 {
	return max(-32768, min(a, 32767))
}

func silk_max(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// silk_INVERSE32_varQ computes 1/a in Qres format.
// Matches libopus silk/Inlines.h silk_INVERSE32_varQ using Newton-Raphson.
func silk_INVERSE32_varQ(b32 int32, qres int) int32 {
	if b32 == 0 {
		return 0x7FFFFFFF
	}

	// Count leading zeros and normalize
	absB32 := b32
	if absB32 < 0 {
		absB32 = -absB32
	}
	bHeadrm := silk_CLZ32(absB32) - 1
	b32Nrm := silk_LSHIFT32(b32, bHeadrm) // Q: b_headrm

	// Inverse of b32, with 14 bits of precision
	// b32_inv = (silk_int32_MAX >> 2) / (b32_nrm >> 16)
	b32Inv := silk_DIV32_16(0x7FFFFFFF>>2, int16(b32Nrm>>16)) // Q: 29 + 16 - b_headrm

	// First approximation
	result := silk_LSHIFT32(b32Inv, 16) // Q: 61 - b_headrm

	// Compute residual: (1<<29) - (b32_nrm * b32_inv >> 16)
	errQ32 := silk_LSHIFT32((1<<29)-silk_SMULWB(b32Nrm, b32Inv), 3) // Q32

	// Refinement
	result = silk_SMLAWW_int32(result, errQ32, b32Inv) // Q: 61 - b_headrm

	// Convert to Qres domain
	lshift := 61 - bHeadrm - qres
	if lshift <= 0 {
		return silk_LSHIFT_SAT32(result, -lshift)
	} else if lshift < 32 {
		return result >> lshift
	}
	return 0
}

// silk_CLZ32 counts leading zeros in a 32-bit value.
func silk_CLZ32(x int32) int {
	if x == 0 {
		return 32
	}
	n := 0
	if x < 0 {
		return 0 // Negative number has no leading zeros in 2's complement
	}
	ux := uint32(x)
	if ux <= 0x0000FFFF {
		n += 16
		ux <<= 16
	}
	if ux <= 0x00FFFFFF {
		n += 8
		ux <<= 8
	}
	if ux <= 0x0FFFFFFF {
		n += 4
		ux <<= 4
	}
	if ux <= 0x3FFFFFFF {
		n += 2
		ux <<= 2
	}
	if ux <= 0x7FFFFFFF {
		n += 1
	}
	return n
}

// silk_LSHIFT_SAT32 shifts left with saturation.
func silk_LSHIFT_SAT32(a int32, shift int) int32 {
	if shift < 0 {
		return a >> (-shift)
	}
	if shift == 0 {
		return a
	}
	min := int32(-0x80000000) >> shift
	max := int32(0x7FFFFFFF) >> shift
	if a < min {
		a = min
	} else if a > max {
		a = max
	}
	return silk_LSHIFT32(a, shift)
}

// silk_DIV32_16 divides int32 by int16.
func silk_DIV32_16(a int32, b int16) int32 {
	if b == 0 {
		if a >= 0 {
			return 0x7FFFFFFF
		}
		return -0x80000000
	}
	return a / int32(b)
}

// silk_SMLAWW_int32 is multiply-accumulate for inverse computation.
func silk_SMLAWW_int32(a, b, c int32) int32 {
	return silk_SMLAWB(a, b, c) + b*silk_RSHIFT_ROUND(c, 16)
}

// silk_DIV32_varQ computes a/b in Qres format
func silk_DIV32_varQ(a, b int32, qres int) int32 {
	// Use libopus-matching implementation.
	return silkDiv32VarQ(a, b, qres)
}
