package silk

type encodeFrameTraceStage uint8

const (
	encodeFrameTraceAfterIndices encodeFrameTraceStage = iota + 1
	encodeFrameTraceAfterPulses
)

type encodeFrameIndexTracePoint uint8

const (
	encodeFrameIndexTraceAfterType encodeFrameIndexTracePoint = iota
	encodeFrameIndexTraceAfterGains
	encodeFrameIndexTraceAfterNLSF
	encodeFrameIndexTraceAfterPitch
	encodeFrameIndexTraceAfterLTP
	encodeFrameIndexTraceAfterSeed
	encodeFrameIndexTracePointCount
)

type encodeFrameRangeTrace struct {
	tell int
	rng  uint32
}

type encodeFrameTrace struct {
	stage              encodeFrameTraceStage
	iter               int
	tell               int
	rng                uint32
	indexTrace         [encodeFrameIndexTracePointCount]encodeFrameRangeTrace
	indices            sideInfoIndices
	predGainBits       int32
	pitchAutoCorr0Bits int32
	pitchResNrgBits    int32
	gainsPreQ16        [maxNbSubfr]int32
	resNrgBits         [maxNbSubfr]int32
	gainsUnqQ16        [maxNbSubfr]int32
	gainsQuantQ16      [maxNbSubfr]int32
	pulses             []int8

	// Frame-level SILK control state captured just before the rate-control
	// loop (mirrors silk_encoder_control_FLP after silk_process_gains_FLP).
	// Populated only on the first (iter==0) AfterPulses trace; used by the
	// frame-level SILK control oracle comparison.
	ctrlSignalType   int
	ctrlQuantOffset  int
	ctrlNbSubfr      int
	ctrlLambdaQ10    int32
	ctrlCodingQual   float32
	ctrlInputQual    float32
	ctrlGainsQ16     [maxNbSubfr]int32
	ctrlTiltQ14      [maxNbSubfr]int32
	ctrlHarmShapeQ14 [maxNbSubfr]int32
	ctrlLFShpQ14     [maxNbSubfr]int32
	ctrlARShpQ13     [maxNbSubfr * maxShapeLpcOrder]int16
	ctrlPitchL       [maxNbSubfr]int32
}

// fillCtrlTrace records the frame-level SILK control state (Lambda, shaping
// AR/Tilt/HarmShapeGain/LF, gains, pitch lags) that the iter-0 NSQ pass
// consumes. This mirrors the silk_encoder_control_FLP dumped by the C oracle
// in tools/csrc/silk_encode_frame_FLP_dump.c.
func fillCtrlTrace(tr *encodeFrameTrace, signalType, quantOffset, numSubframes int, params *NoiseShapeParams, gainsQ16, pitchLags []int32) {
	tr.ctrlSignalType = signalType
	tr.ctrlQuantOffset = quantOffset
	tr.ctrlNbSubfr = numSubframes
	if params != nil {
		tr.ctrlLambdaQ10 = params.LambdaQ10
		tr.ctrlCodingQual = params.CodingQuality
		tr.ctrlInputQual = params.InputQuality
	}
	for i := 0; i < numSubframes && i < maxNbSubfr; i++ {
		if i < len(gainsQ16) {
			tr.ctrlGainsQ16[i] = gainsQ16[i]
		}
		if i < len(pitchLags) {
			tr.ctrlPitchL[i] = pitchLags[i]
		}
		if params != nil {
			if i < len(params.TiltQ14) {
				tr.ctrlTiltQ14[i] = params.TiltQ14[i]
			}
			if i < len(params.HarmShapeGainQ14) {
				tr.ctrlHarmShapeQ14[i] = params.HarmShapeGainQ14[i]
			}
			if i < len(params.LFShpQ14) {
				tr.ctrlLFShpQ14[i] = params.LFShpQ14[i]
			}
		}
	}
	if params != nil {
		n := min(min(numSubframes*maxShapeLpcOrder, len(tr.ctrlARShpQ13)), len(params.ARShpQ13))
		copy(tr.ctrlARShpQ13[:n], params.ARShpQ13[:n])
	}
}
