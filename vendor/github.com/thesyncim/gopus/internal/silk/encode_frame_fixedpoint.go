//go:build gopus_fixed_point

package silk

// This file assembles the FIXED_POINT SILK per-frame analysis driver from
// silk/fixed/encode_frame_FIX.c (silk_encode_frame_FIX). It wires the already
// ported FIXED_POINT sub-drivers and leaf kernels into the full
// silk_encoder_state_FIX analysis flow:
//
//   - silk_VAD_GetSA_Q8 (silkVADGetSAQ8) for the speech-activity estimate and
//     the VAD/DTX signal-type decision (silk_encode_do_VAD_FIX),
//   - silk_find_pitch_lags_FIX: the LPC-whitening front-end
//     (silkFindPitchLagsFIXFrontEnd) followed by silk_pitch_analysis_core_FIX
//     (silkPitchAnalysisCoreFixed),
//   - silk_noise_shape_analysis_FIX (silkNoiseShapeAnalysisFIX),
//   - silk_find_pred_coefs_FIX (silkFindPredCoefsFIX),
//   - silk_process_gains_FIX (silkProcessGainsFixed), which also produces
//     Lambda_Q10,
//   - silk_NSQ (silkNSQFixed) noise-shaping quantization, producing the
//     excitation pulses and the updated NSQ state.
//
// The bitstream entropy coding (silk_encode_indices / silk_encode_pulses) is
// NOT re-implemented here: those range-coder kernels are already integer in the
// default build (encode_frame.go, excitation_encode.go, ...) and are shared,
// not part of the FIXED_POINT-only surface. This driver carries the analysis
// from PCM through NSQ, which is the full chain that determines the side-info
// indices, gains and pulses fed into the (already validated) shared range
// encoder. The output of this driver is bit-exact against the reference
// silk_encode_frame_FIX up to the silk_encode_indices call.
//
// The delayed-decision NSQ outer driver (silk_NSQ_del_dec_c subframe loop) is
// not yet ported; only its inner quantizer (silkNoiseShapeQuantizerDelDecFixed)
// and scale-states (silkNSQDelDecScaleStatesFixed) kernels exist. This driver
// therefore drives silkNSQFixed (the non-del-dec path), which libopus selects
// when nStatesDelayedDecision <= 1 and warping_Q16 == 0. When the encoder is
// configured for delayed decision, the analysis chain (everything up to NSQ)
// is still bit-exact; only the NSQ outer loop remains to be wired once the
// del-dec outer driver lands.

// Constants from silk/define.h used by the FIXED_POINT encode-frame driver.
const (
	nbSpeechFramesBeforeDTX = 10 // NB_SPEECH_FRAMES_BEFORE_DTX (eq 200 ms)
	maxConsecutiveDTX       = 20 // MAX_CONSECUTIVE_DTX (eq 400 ms)
	vadNoActivity           = 0  // VAD_NO_ACTIVITY
)

// nlsfCBForPredOrder selects the NLSF codebook for the prediction LPC order,
// matching libopus: WB (order 16) uses silk_NLSF_CB_WB, NB/MB uses
// silk_NLSF_CB_NB_MB.
func nlsfCBForPredOrder(predOrder int) *nlsfCB {
	if predOrder == 16 {
		return &silk_NLSF_CB_WB
	}
	return &silk_NLSF_CB_NB_MB
}

// silkEncodeFrameFIXState is a flattened view of the silk_encoder_state_FIX /
// silk_encoder_control_FIX fields that the analysis chain of
// silk_encode_frame_FIX reads and writes. It lets the driver be exercised in
// isolation against its own oracle without standing up the full encoder object.
type silkEncodeFrameFIXState struct {
	// ----- silk_encoder_state (sCmn) configuration -----
	fsKHz                   int
	frameLength             int
	subfrLength             int
	nbSubfr                 int
	ltpMemLength            int
	laPitch                 int
	laShape                 int
	pitchLPCWinLength       int
	pitchEstimationLPCOrder int
	predictLPCOrder         int
	shapingLPCOrder         int
	shapeWinLength          int
	complexity              int
	nStatesDelayedDecision  int
	warpingQ16              int32
	useCBR                  int
	nlsfMSVQSurvivors       int

	pitchEstimationThresholdQ16 int32

	// Rate-control / scaling inputs.
	snrDBQ7          int32
	inputTiltQ15     int32
	packetLossPerc   int32
	nFramesPerPacket int32
	lbrrFlag         int32
	condCoding       int32

	// VAD activity decision from the Opus-level detector.
	opusVADActivity int

	// ----- mutable sCmn state -----
	frameCounter         int32
	prevSignalType       int32
	prevLag              int32
	speechActivityQ8     int32
	inputQualityBandsQ15 [vadNBands]int32
	indicesSignalType    int8
	indicesQuantOffset   int8
	indicesSeed          int8
	noSpeechCounter      int32
	inDTX                int32
	firstFrameAfterReset bool
	ltpCorrQ15           int32
	sumLogGainQ7         int32
	prevNLSFqQ15         [maxLPCOrder]int16

	// ----- mutable sShape state -----
	harmShapeGainSmthQ16 int32
	tiltSmthQ16          int32
	lastGainIndex        int8

	// ----- VAD state -----
	vad silkVADState

	// ----- NSQ state -----
	nsq NSQState

	// ----- input buffers -----
	// vadInput is sCmn.inputBuf+1 (frame_length samples) fed to the VAD.
	vadInput []int16
	// xBuf is psEnc->x_buf with the new frame already copied to
	// x_frame + LA_SHAPE_MS*fs_kHz; its layout is
	// [ltp_mem_length | la_shape | frame_length(+lookahead)].
	// x_frame = xBuf[ltp_mem_length:].
	xBuf []int16
}

// silkEncodeFrameFIXResult carries the side-info / encoder-control outputs and
// the NSQ excitation produced by the analysis chain.
type silkEncodeFrameFIXResult struct {
	// Side-info indices.
	signalType       int8
	quantOffsetType  int8
	seed             int8
	nlsfInterpCoefQ2 int8
	perIndex         int8
	ltpScaleIndex    int8
	lagIndex         int16
	contourIndex     int8
	nlsfIndices      []int8
	ltpIndex         [maxNbSubfr]int8
	gainsIndices     []int8

	// Encoder-control outputs.
	predCoefQ12      [2][]int16
	ltpCoefQ14       []int16
	gainsQ16         []int32
	gainsUnqQ16      []int32
	arQ13            []int16
	harmShapeGainQ14 []int32
	tiltQ14          []int32
	lfShpQ14         []int32
	pitchL           []int32
	lambdaQ10        int32
	ltpScaleQ14      int32
	ltpredCodGainQ7  int32

	// NSQ excitation.
	pulses []int8

	// Voicing flag from VAD (1 == active).
	vadFlag int

	// Echoed/updated state for inspection.
	lastGainIndex int8
}

// sEncCtrlFIX is a flattened view of the silk_encoder_control_FIX fields that
// silk_encode_frame_FIX's rate-control loop reads after the analysis chain. It
// is the Go equivalent of the C sEncCtrl that survives between
// silk_process_gains_FIX and the gain/Lambda iteration.
type sEncCtrlFIX struct {
	// Per-subframe coefficient/shaping parameters consumed by NSQ.
	predCoefQ12      [2][]int16
	ltpCoefQ14       []int16
	arQ13            []int16
	harmShapeGainQ14 []int32
	tiltQ14          []int32
	lfShpQ14         []int32
	gainsQ16         []int32 // mutated by the rate-control loop
	gainsUnqQ16      []int32
	pitchL           []int32
	lambdaQ10        int32
	ltpScaleQ14      int32
	ltpredCodGainQ7  int32

	// Side-info indices produced by the analysis chain (the parts that do not
	// change inside the rate loop; gains/Seed are updated per iteration).
	signalType       int8
	quantOffsetType  int8
	nlsfInterpCoefQ2 int8
	perIndex         int8
	ltpScaleIndex    int8
	lagIndex         int16
	contourIndex     int8
	nlsfIndices      []int8
	ltpIndex         [maxNbSubfr]int8

	// gainsIndices is the gain-index vector from process_gains (iteration 0
	// of the rate loop uses these directly; later iterations re-quantize).
	gainsIndices []int8

	// lastGainIndexPrev is psShapeSt->LastGainIndex captured before the first
	// gains_quant in process_gains, used to re-quantize each loop iteration.
	lastGainIndexPrev int8

	vadFlag int
}

// silkEncodeFrameFIXAnalyze runs the bit-exact analysis chain of
// silk_encode_frame_FIX (VAD, pitch, noise-shape, pred-coefs, process-gains)
// WITHOUT the NSQ pass or the rate-control loop. It mutates st in place
// (frame counter, VAD/shape state, previous NLSFs, etc.) and returns the
// encoder-control struct that the rate loop iterates over.
func (e *Encoder) silkEncodeFrameFIXAnalyze(st *silkEncodeFrameFIXState) sEncCtrlFIX {
	var ctrl sEncCtrlFIX
	sc := e.fixedScratch()

	// x_frame = x_buf + ltp_mem_length.
	xFrame := st.ltpMemLength

	/****************************/
	/* Voice Activity Detection */
	/****************************/
	ctrl.vadFlag = e.silkEncodeDoVADFIX(st)

	/*****************************************/
	/* Find pitch lags, initial LPC analysis */
	/*****************************************/
	bufLen := st.laPitch + st.frameLength + st.ltpMemLength
	pitch := &silkFindPitchLagsInput{
		laPitch:                 st.laPitch,
		frameLength:             st.frameLength,
		ltpMemLength:            st.ltpMemLength,
		pitchLPCWinLength:       st.pitchLPCWinLength,
		pitchEstimationLPCOrder: st.pitchEstimationLPCOrder,
		x:                       st.xBuf[xFrame-st.ltpMemLength : xFrame-st.ltpMemLength+bufLen],
	}
	pitchFE := silkFindPitchLagsFIXFrontEnd(sc, pitch)
	resPitch := pitchFE.res
	resPitchFrame := st.ltpMemLength

	var pitchL [maxNbSubfr]int32
	var lagIndex int16
	var contourIndex int8

	if st.indicesSignalType != int8(typeNoVoiceActivity) && !st.firstFrameAfterReset {
		thrhldQ13 := int32(silkFixConst(0.6, 13))
		thrhldQ13 = silkSMLABB(thrhldQ13, int32(silkFixConst(-0.004, 13)), int32(st.pitchEstimationLPCOrder))
		thrhldQ13 = silkSMLAWB(thrhldQ13, int32(silkFixConst(-0.1, 21)), st.speechActivityQ8)
		thrhldQ13 = silkSMLABB(thrhldQ13, int32(silkFixConst(-0.15, 13)), silkRSHIFT(st.prevSignalType, 1))
		thrhldQ13 = silkSMLAWB(thrhldQ13, int32(silkFixConst(-0.1, 14)), st.inputTiltQ15)
		thrhldQ13 = int32(silkSAT16(thrhldQ13))

		var pitchOut [maxNbSubfr]int
		ltpCorr := st.ltpCorrQ15
		li, ci, voicing := silkPitchAnalysisCoreFixed(
			sc,
			resPitch,
			pitchOut[:st.nbSubfr],
			&ltpCorr,
			int(st.prevLag),
			st.pitchEstimationThresholdQ16,
			int(thrhldQ13),
			st.fsKHz,
			st.complexity,
			st.nbSubfr,
		)
		st.ltpCorrQ15 = ltpCorr
		for k := 0; k < st.nbSubfr; k++ {
			pitchL[k] = int32(pitchOut[k])
		}
		lagIndex = li
		contourIndex = ci
		if voicing == 0 {
			st.indicesSignalType = int8(typeVoiced)
		} else {
			st.indicesSignalType = int8(typeUnvoiced)
		}
	} else {
		st.ltpCorrQ15 = 0
	}
	signalType := int32(st.indicesSignalType)

	/************************/
	/* Noise shape analysis */
	/************************/
	nsaIn := &silkNoiseShapeAnalysisInput{
		laShape:              st.laShape,
		snrDBQ7:              st.snrDBQ7,
		inputQualityBandsQ15: [2]int32{st.inputQualityBandsQ15[0], st.inputQualityBandsQ15[1]},
		useCBR:               st.useCBR,
		speechActivityQ8:     st.speechActivityQ8,
		signalType:           int(signalType),
		fsKHz:                st.fsKHz,
		nbSubfr:              st.nbSubfr,
		subfrLength:          st.subfrLength,
		warpingQ16:           st.warpingQ16,
		shapeWinLength:       st.shapeWinLength,
		shapingLPCOrder:      st.shapingLPCOrder,
		ltpCorrQ15:           st.ltpCorrQ15,
		predGainQ16:          pitchFE.predGainQ16,
		harmShapeGainSmthQ16: st.harmShapeGainSmthQ16,
		tiltSmthQ16:          st.tiltSmthQ16,
		pitchRes:             resPitch[resPitchFrame : resPitchFrame+st.frameLength],
		x:                    st.xBuf[xFrame-st.laShape:],
	}
	for k := 0; k < st.nbSubfr; k++ {
		nsaIn.pitchL[k] = pitchL[k]
	}
	nsa := silkNoiseShapeAnalysisFIX(sc, nsaIn)
	st.harmShapeGainSmthQ16 = nsaIn.harmShapeGainSmthQ16
	st.tiltSmthQ16 = nsaIn.tiltSmthQ16

	/***************************************************/
	/* Find linear prediction coefficients (LPC + LTP) */
	/***************************************************/
	fpIn := &silkFindPredCoefsInput{
		predictLPCOrder:      st.predictLPCOrder,
		subfrLength:          st.subfrLength,
		nbSubfr:              st.nbSubfr,
		frameLength:          st.frameLength,
		signalType:           signalType,
		useInterpolatedNLSFs: 0,
		firstFrameAfterReset: st.firstFrameAfterReset,
		speechActivityQ8:     st.speechActivityQ8,
		nlsfMSVQSurvivors:    st.nlsfMSVQSurvivors,
		packetLossPerc:       st.packetLossPerc,
		nFramesPerPacket:     st.nFramesPerPacket,
		lbrrFlag:             st.lbrrFlag,
		snrDBQ7:              st.snrDBQ7,
		condCoding:           st.condCoding,
		codingQualityQ14:     nsa.codingQualityQ14,
		sumLogGainQ7:         st.sumLogGainQ7,
		prevNLSFqQ15:         st.prevNLSFqQ15,
		cb:                   nlsfCBForPredOrder(st.predictLPCOrder),
		resPitch:             resPitch,
		resPitchStart:        resPitchFrame,
		x:                    st.xBuf,
		xStart:               xFrame,
	}
	for k := 0; k < st.nbSubfr; k++ {
		fpIn.gainsQ16[k] = nsa.gainsQ16[k]
		fpIn.pitchL[k] = pitchL[k]
	}
	fp := e.silkFindPredCoefsFIX(fpIn)
	st.sumLogGainQ7 = fp.sumLogGainQ7
	st.prevNLSFqQ15 = fp.prevNLSFqQ15

	/****************************************/
	/* Process gains                        */
	/****************************************/
	resNrgQ := ensureInt32Slice(&sc.resNrgQ, st.nbSubfr)
	for k := 0; k < st.nbSubfr; k++ {
		resNrgQ[k] = int32(fp.resNrgQ[k])
	}
	gainsQ16 := ensureInt32Slice(&sc.gainsQ16, st.nbSubfr)
	copy(gainsQ16, nsa.gainsQ16[:st.nbSubfr])
	pgParams := &silkProcessGainsParams{
		signalType:             signalType,
		nbSubfr:                st.nbSubfr,
		subfrLength:            int32(st.subfrLength),
		snrDBQ7:                st.snrDBQ7,
		inputTiltQ15:           st.inputTiltQ15,
		nStatesDelayedDecision: int32(st.nStatesDelayedDecision),
		speechActivityQ8:       st.speechActivityQ8,
		quantOffsetType:        int32(nsa.quantOffsetType),
		ltpredCodGainQ7:        fp.ltpredCodGainQ7,
		inputQualityQ14:        nsa.inputQualityQ14,
		codingQualityQ14:       nsa.codingQualityQ14,
		gainsQ16:               gainsQ16,
		resNrg:                 fp.resNrg,
		resNrgQ:                resNrgQ,
		lastGainIndex:          st.lastGainIndex,
		condCoding:             st.condCoding,
	}
	pg := silkProcessGainsFixed(sc, pgParams)
	st.lastGainIndex = pg.lastGainIndex
	st.indicesQuantOffset = int8(pg.quantOffsetType)

	// Build the per-subframe NSQ coefficient inputs.
	predCoefFlat := ensureInt16Slice(&sc.predCoefFlat, 2*maxLPCOrder)
	for i := range predCoefFlat {
		predCoefFlat[i] = 0
	}
	for i := 0; i < st.predictLPCOrder && i < len(fp.predCoefQ12[0]); i++ {
		predCoefFlat[i] = fp.predCoefQ12[0][i]
	}
	for i := 0; i < st.predictLPCOrder && i < len(fp.predCoefQ12[1]); i++ {
		predCoefFlat[maxLPCOrder+i] = fp.predCoefQ12[1][i]
	}

	arQ13 := ensureInt16Slice(&sc.arQ13, st.nbSubfr*maxShapeLpcOrder)
	copy(arQ13, nsa.arQ13[:st.nbSubfr*maxShapeLpcOrder])

	ltpCoefQ14 := ensureInt16Slice(&sc.ltpCoefQ14, st.nbSubfr*ltpOrderConst)
	copy(ltpCoefQ14, fp.ltpCoefQ14)

	harmShapeGainQ14 := ensureInt32Slice(&sc.harmShapeGainQ14, st.nbSubfr)
	tiltQ14 := ensureInt32Slice(&sc.tiltQ14, st.nbSubfr)
	lfShpQ14 := ensureInt32Slice(&sc.lfShpQ14, st.nbSubfr)
	pitchLQ := ensureInt32Slice(&sc.pitchLQ, st.nbSubfr)
	for k := 0; k < st.nbSubfr; k++ {
		harmShapeGainQ14[k] = nsa.harmShapeGainQ14[k]
		tiltQ14[k] = nsa.tiltQ14[k]
		lfShpQ14[k] = nsa.lfShpQ14[k]
		pitchLQ[k] = int32(pitchL[k])
	}

	ltpScaleQ14 := int32(0)
	if signalType == typeVoiced {
		ltpScaleQ14 = fp.ltpScaleQ14
	}

	ctrl.predCoefQ12 = fp.predCoefQ12
	ctrl.predCoefQ12[0] = predCoefFlat[:st.predictLPCOrder]
	ctrl.predCoefQ12[1] = predCoefFlat[maxLPCOrder : maxLPCOrder+st.predictLPCOrder]
	ctrl.ltpCoefQ14 = ltpCoefQ14
	ctrl.arQ13 = arQ13
	ctrl.harmShapeGainQ14 = harmShapeGainQ14
	ctrl.tiltQ14 = tiltQ14
	ctrl.lfShpQ14 = lfShpQ14
	ctrl.gainsQ16 = gainsQ16
	ctrl.gainsUnqQ16 = pg.gainsUnqQ16
	ctrl.pitchL = pitchLQ
	ctrl.lambdaQ10 = pg.lambdaQ10
	ctrl.ltpScaleQ14 = ltpScaleQ14
	ctrl.ltpredCodGainQ7 = fp.ltpredCodGainQ7

	ctrl.signalType = int8(signalType)
	ctrl.quantOffsetType = st.indicesQuantOffset
	ctrl.nlsfInterpCoefQ2 = fp.nlsfInterpCoefQ2
	ctrl.perIndex = fp.perIndex
	if signalType == typeVoiced {
		ltpScaleIdx, _ := silkLTPScaleCtrlFixed(fp.ltpredCodGainQ7, st.packetLossPerc,
			st.nFramesPerPacket, st.lbrrFlag, st.snrDBQ7, st.condCoding)
		ctrl.ltpScaleIndex = int8(ltpScaleIdx)
	}
	ctrl.lagIndex = lagIndex
	ctrl.contourIndex = contourIndex
	ctrl.nlsfIndices = fp.nlsfIndices
	ctrl.ltpIndex = fp.ltpIndex
	ctrl.gainsIndices = pg.gainsIndices
	ctrl.lastGainIndexPrev = pg.lastGainIndexPrev

	// Parameters needed for next frame.
	st.prevLag = pitchLQ[st.nbSubfr-1]
	st.prevSignalType = signalType

	return ctrl
}

// silkEncodeFrameFIX is the bit-exact Go port of the analysis chain of
// silk_encode_frame_FIX. It runs the VAD, pitch analysis, noise-shape analysis,
// prediction-coefficient search, gain processing and NSQ in the exact libopus
// order, mutating st in place (frame counter, VAD/NSQ/shape state, previous
// NLSFs, etc.) and returning the side-info indices, encoder-control parameters
// and excitation pulses.
func (e *Encoder) silkEncodeFrameFIX(st *silkEncodeFrameFIXState) silkEncodeFrameFIXResult {
	var res silkEncodeFrameFIXResult
	sc := e.fixedScratch()

	// silk_encode_frame_FIX: indices.Seed = frameCounter++ & 3.
	st.indicesSeed = int8(st.frameCounter & 3)
	st.frameCounter++

	// x_frame = x_buf + ltp_mem_length.
	xFrame := st.ltpMemLength

	/****************************/
	/* Voice Activity Detection */
	/****************************/
	// silk_encode_do_VAD_FIX runs silk_VAD_GetSA_Q8 then the VAD/DTX decision.
	res.vadFlag = e.silkEncodeDoVADFIX(st)

	/*****************************************/
	/* Find pitch lags, initial LPC analysis */
	/*****************************************/
	// res_pitch buffer: la_pitch + frame_length + ltp_mem_length samples.
	// res_pitch_frame = res_pitch + ltp_mem_length.
	bufLen := st.laPitch + st.frameLength + st.ltpMemLength
	pitch := &silkFindPitchLagsInput{
		laPitch:                 st.laPitch,
		frameLength:             st.frameLength,
		ltpMemLength:            st.ltpMemLength,
		pitchLPCWinLength:       st.pitchLPCWinLength,
		pitchEstimationLPCOrder: st.pitchEstimationLPCOrder,
		x:                       st.xBuf[xFrame-st.ltpMemLength : xFrame-st.ltpMemLength+bufLen],
	}
	pitchFE := silkFindPitchLagsFIXFrontEnd(sc, pitch)
	resPitch := pitchFE.res // bufLen samples
	resPitchFrame := st.ltpMemLength

	// silk_pitch_analysis_core threshold/contour search.
	// Mirrors silk_find_pitch_lags_FIX exactly: the core runs only when
	// signalType != TYPE_NO_VOICE_ACTIVITY && first_frame_after_reset == 0;
	// otherwise pitchL/lagIndex/contourIndex/LTPCorr_Q15 are zeroed.
	var pitchL [maxNbSubfr]int32
	var lagIndex int16
	var contourIndex int8

	if st.indicesSignalType != int8(typeNoVoiceActivity) && !st.firstFrameAfterReset {
		// Threshold for pitch estimator (Q13).
		thrhldQ13 := int32(silkFixConst(0.6, 13))
		thrhldQ13 = silkSMLABB(thrhldQ13, int32(silkFixConst(-0.004, 13)), int32(st.pitchEstimationLPCOrder))
		thrhldQ13 = silkSMLAWB(thrhldQ13, int32(silkFixConst(-0.1, 21)), st.speechActivityQ8)
		thrhldQ13 = silkSMLABB(thrhldQ13, int32(silkFixConst(-0.15, 13)), silkRSHIFT(st.prevSignalType, 1))
		thrhldQ13 = silkSMLAWB(thrhldQ13, int32(silkFixConst(-0.1, 14)), st.inputTiltQ15)
		thrhldQ13 = int32(silkSAT16(thrhldQ13))

		var pitchOut [maxNbSubfr]int
		ltpCorr := st.ltpCorrQ15
		// silk_find_pitch_lags_FIX passes the residual buffer head (res, the
		// full buf_len samples). The pitch core reads
		// (PE_LTP_MEM_LENGTH_MS + nb_subfr*PE_SUBFR_LENGTH_MS)*fs_kHz =
		// ltp_mem_length + frame_length samples plus la_pitch lookahead.
		li, ci, voicing := silkPitchAnalysisCoreFixed(
			sc,
			resPitch,
			pitchOut[:st.nbSubfr],
			&ltpCorr,
			int(st.prevLag),
			st.pitchEstimationThresholdQ16,
			int(thrhldQ13),
			st.fsKHz,
			st.complexity,
			st.nbSubfr,
		)
		st.ltpCorrQ15 = ltpCorr
		for k := 0; k < st.nbSubfr; k++ {
			pitchL[k] = int32(pitchOut[k])
		}
		lagIndex = li
		contourIndex = ci
		if voicing == 0 {
			st.indicesSignalType = int8(typeVoiced)
		} else {
			st.indicesSignalType = int8(typeUnvoiced)
		}
	} else {
		// pitchL already zero; lagIndex/contourIndex zero.
		st.ltpCorrQ15 = 0
	}
	signalType := int32(st.indicesSignalType)

	/************************/
	/* Noise shape analysis */
	/************************/
	nsaIn := &silkNoiseShapeAnalysisInput{
		laShape:              st.laShape,
		snrDBQ7:              st.snrDBQ7,
		inputQualityBandsQ15: [2]int32{st.inputQualityBandsQ15[0], st.inputQualityBandsQ15[1]},
		useCBR:               st.useCBR,
		speechActivityQ8:     st.speechActivityQ8,
		signalType:           int(signalType),
		fsKHz:                st.fsKHz,
		nbSubfr:              st.nbSubfr,
		subfrLength:          st.subfrLength,
		warpingQ16:           st.warpingQ16,
		shapeWinLength:       st.shapeWinLength,
		shapingLPCOrder:      st.shapingLPCOrder,
		ltpCorrQ15:           st.ltpCorrQ15,
		predGainQ16:          pitchFE.predGainQ16,
		harmShapeGainSmthQ16: st.harmShapeGainSmthQ16,
		tiltSmthQ16:          st.tiltSmthQ16,
		// pitch_res = res_pitch_frame (the residual used by the sparseness path).
		pitchRes: resPitch[resPitchFrame : resPitchFrame+st.frameLength],
		// x = x_frame - la_shape.
		x: st.xBuf[xFrame-st.laShape:],
	}
	for k := 0; k < st.nbSubfr; k++ {
		nsaIn.pitchL[k] = pitchL[k]
	}
	nsa := silkNoiseShapeAnalysisFIX(sc, nsaIn)
	st.harmShapeGainSmthQ16 = nsaIn.harmShapeGainSmthQ16
	st.tiltSmthQ16 = nsaIn.tiltSmthQ16

	/***************************************************/
	/* Find linear prediction coefficients (LPC + LTP) */
	/***************************************************/
	fpIn := &silkFindPredCoefsInput{
		predictLPCOrder:      st.predictLPCOrder,
		subfrLength:          st.subfrLength,
		nbSubfr:              st.nbSubfr,
		frameLength:          st.frameLength,
		signalType:           signalType,
		useInterpolatedNLSFs: 0,
		firstFrameAfterReset: st.firstFrameAfterReset,
		speechActivityQ8:     st.speechActivityQ8,
		nlsfMSVQSurvivors:    st.nlsfMSVQSurvivors,
		packetLossPerc:       st.packetLossPerc,
		nFramesPerPacket:     st.nFramesPerPacket,
		lbrrFlag:             st.lbrrFlag,
		snrDBQ7:              st.snrDBQ7,
		condCoding:           st.condCoding,
		codingQualityQ14:     nsa.codingQualityQ14,
		sumLogGainQ7:         st.sumLogGainQ7,
		prevNLSFqQ15:         st.prevNLSFqQ15,
		cb:                   nlsfCBForPredOrder(st.predictLPCOrder),
		// res_pitch_frame for the LTP search.
		resPitch:      resPitch,
		resPitchStart: resPitchFrame,
		// x_frame; the driver reads x - predictLPCOrder internally.
		x:      st.xBuf,
		xStart: xFrame,
	}
	for k := 0; k < st.nbSubfr; k++ {
		fpIn.gainsQ16[k] = nsa.gainsQ16[k]
		fpIn.pitchL[k] = pitchL[k]
	}
	fp := e.silkFindPredCoefsFIX(fpIn)
	st.sumLogGainQ7 = fp.sumLogGainQ7
	st.prevNLSFqQ15 = fp.prevNLSFqQ15

	/****************************************/
	/* Process gains                        */
	/****************************************/
	resNrgQ := ensureInt32Slice(&sc.resNrgQ, st.nbSubfr)
	for k := 0; k < st.nbSubfr; k++ {
		resNrgQ[k] = int32(fp.resNrgQ[k])
	}
	gainsQ16 := ensureInt32Slice(&sc.gainsQ16, st.nbSubfr)
	copy(gainsQ16, nsa.gainsQ16[:st.nbSubfr])
	pgParams := &silkProcessGainsParams{
		signalType:             signalType,
		nbSubfr:                st.nbSubfr,
		subfrLength:            int32(st.subfrLength),
		snrDBQ7:                st.snrDBQ7,
		inputTiltQ15:           st.inputTiltQ15,
		nStatesDelayedDecision: int32(st.nStatesDelayedDecision),
		speechActivityQ8:       st.speechActivityQ8,
		quantOffsetType:        int32(nsa.quantOffsetType),
		ltpredCodGainQ7:        fp.ltpredCodGainQ7,
		inputQualityQ14:        nsa.inputQualityQ14,
		codingQualityQ14:       nsa.codingQualityQ14,
		gainsQ16:               gainsQ16,
		resNrg:                 fp.resNrg,
		resNrgQ:                resNrgQ,
		lastGainIndex:          st.lastGainIndex,
		condCoding:             st.condCoding,
	}
	pg := silkProcessGainsFixed(sc, pgParams)
	st.lastGainIndex = pg.lastGainIndex
	st.indicesQuantOffset = int8(pg.quantOffsetType)

	/*****************************************/
	/* Noise shaping quantization            */
	/*****************************************/
	// LTP scale Q14 selected by the LTP scale index (voiced only).
	ltpScaleQ14 := int32(0)
	if signalType == typeVoiced {
		ltpScaleQ14 = fp.ltpScaleQ14
	}

	// Build the per-subframe NSQ coefficient inputs.
	predCoefFlat := ensureInt16Slice(&sc.predCoefFlat, 2*maxLPCOrder)
	for i := range predCoefFlat {
		predCoefFlat[i] = 0
	}
	for i := 0; i < st.predictLPCOrder && i < len(fp.predCoefQ12[0]); i++ {
		predCoefFlat[i] = fp.predCoefQ12[0][i]
	}
	for i := 0; i < st.predictLPCOrder && i < len(fp.predCoefQ12[1]); i++ {
		predCoefFlat[maxLPCOrder+i] = fp.predCoefQ12[1][i]
	}

	arQ13 := ensureInt16Slice(&sc.arQ13, st.nbSubfr*maxShapeLpcOrder)
	copy(arQ13, nsa.arQ13[:st.nbSubfr*maxShapeLpcOrder])

	ltpCoefQ14 := ensureInt16Slice(&sc.ltpCoefQ14, st.nbSubfr*ltpOrderConst)
	copy(ltpCoefQ14, fp.ltpCoefQ14)

	harmShapeGainQ14 := ensureInt32Slice(&sc.harmShapeGainQ14, st.nbSubfr)
	tiltQ14 := ensureInt32Slice(&sc.tiltQ14, st.nbSubfr)
	lfShpQ14 := ensureInt32Slice(&sc.lfShpQ14, st.nbSubfr)
	pitchLQ := ensureInt32Slice(&sc.pitchLQ, st.nbSubfr)
	for k := 0; k < st.nbSubfr; k++ {
		harmShapeGainQ14[k] = nsa.harmShapeGainQ14[k]
		tiltQ14[k] = nsa.tiltQ14[k]
		lfShpQ14[k] = nsa.lfShpQ14[k]
		pitchLQ[k] = int32(pitchL[k])
	}

	pulses := make([]int8, st.frameLength)
	// silk_encode_frame_FIX selects silk_NSQ when nStatesDelayedDecision <= 1
	// and warping_Q16 == 0. The del-dec outer loop is not yet ported.
	silkNSQFixed(
		sc,
		&st.nsq,
		int(st.indicesSeed),
		int(signalType),
		int(st.indicesQuantOffset),
		int(fp.nlsfInterpCoefQ2),
		st.xBuf[xFrame:xFrame+st.frameLength],
		pulses,
		predCoefFlat,
		ltpCoefQ14,
		arQ13,
		harmShapeGainQ14,
		tiltQ14,
		lfShpQ14,
		gainsQ16,
		pitchLQ,
		pg.lambdaQ10,
		ltpScaleQ14,
		st.ltpMemLength,
		st.frameLength,
		st.subfrLength,
		st.nbSubfr,
		st.predictLPCOrder,
		st.shapingLPCOrder,
	)

	// Parameters needed for next frame.
	st.prevLag = pitchLQ[st.nbSubfr-1]
	st.prevSignalType = signalType

	// Populate result side-info / control outputs.
	res.signalType = int8(signalType)
	res.quantOffsetType = st.indicesQuantOffset
	res.seed = st.indicesSeed
	res.nlsfInterpCoefQ2 = fp.nlsfInterpCoefQ2
	res.perIndex = fp.perIndex
	// LTPScaleIndex: silk_find_pred_coefs_FIX only sets it for voiced frames
	// (silk_LTP_scale_ctrl_FIX); it stays 0 for unvoiced.
	if signalType == typeVoiced {
		ltpScaleIdx, _ := silkLTPScaleCtrlFixed(fp.ltpredCodGainQ7, st.packetLossPerc,
			st.nFramesPerPacket, st.lbrrFlag, st.snrDBQ7, st.condCoding)
		res.ltpScaleIndex = int8(ltpScaleIdx)
	}
	res.lagIndex = lagIndex
	res.contourIndex = contourIndex
	res.nlsfIndices = fp.nlsfIndices
	res.ltpIndex = fp.ltpIndex
	res.gainsIndices = pg.gainsIndices

	res.predCoefQ12 = fp.predCoefQ12
	res.ltpCoefQ14 = fp.ltpCoefQ14
	res.gainsQ16 = gainsQ16
	res.gainsUnqQ16 = pg.gainsUnqQ16
	res.arQ13 = arQ13
	res.harmShapeGainQ14 = harmShapeGainQ14
	res.tiltQ14 = tiltQ14
	res.lfShpQ14 = lfShpQ14
	res.pitchL = pitchLQ
	res.lambdaQ10 = pg.lambdaQ10
	res.ltpScaleQ14 = ltpScaleQ14
	res.ltpredCodGainQ7 = fp.ltpredCodGainQ7
	res.pulses = pulses
	res.lastGainIndex = st.lastGainIndex

	return res
}

// silkEncodeDoVADFIX is the bit-exact port of silk_encode_do_VAD_FIX
// (encode_frame_FIX.c): it runs silk_VAD_GetSA_Q8 then converts the resulting
// speech activity into the VAD/DTX signal-type decision. It returns 1 when the
// frame is VAD-active, 0 otherwise.
func (e *Encoder) silkEncodeDoVADFIX(st *silkEncodeFrameFIXState) int {
	const activityThreshold = speechActivityDTXThresholdQ8 // SILK_FIX_CONST(SPEECH_ACTIVITY_DTX_THRES, 8)

	vadRes := silkVADGetSAQ8(e.fixedScratch(), &st.vad, st.vadInput, st.frameLength, st.fsKHz)
	st.speechActivityQ8 = vadRes.speechActivityQ8
	st.inputTiltQ15 = vadRes.inputTiltQ15
	st.inputQualityBandsQ15 = vadRes.inputQualityBandsQ15

	// If Opus VAD is inactive and Silk VAD is active: lower Silk VAD to just
	// under the threshold.
	if st.opusVADActivity == vadNoActivity && st.speechActivityQ8 >= activityThreshold {
		st.speechActivityQ8 = activityThreshold - 1
	}

	if st.speechActivityQ8 < activityThreshold {
		st.indicesSignalType = int8(typeNoVoiceActivity)
		st.noSpeechCounter++
		if st.noSpeechCounter <= nbSpeechFramesBeforeDTX {
			st.inDTX = 0
		} else if st.noSpeechCounter > maxConsecutiveDTX+nbSpeechFramesBeforeDTX {
			st.noSpeechCounter = nbSpeechFramesBeforeDTX
			st.inDTX = 0
		}
		return 0
	}
	st.noSpeechCounter = 0
	st.inDTX = 0
	st.indicesSignalType = int8(typeUnvoiced)
	return 1
}
