//go:build gopus_silk_trace

package silk

const encodeFrameTraceEnabled = true

var encodeFrameTraceHook func(*Encoder, encodeFrameTrace)

func recordEncodeFrameTrace(e *Encoder, trace encodeFrameTrace) {
	if encodeFrameTraceHook != nil {
		encodeFrameTraceHook(e, trace)
	}
}

func withEncodeFrameTraceHook(hook func(*Encoder, encodeFrameTrace), fn func()) {
	prev := encodeFrameTraceHook
	encodeFrameTraceHook = hook
	defer func() {
		encodeFrameTraceHook = prev
	}()
	fn()
}

// SILKCtrlSnapshot is an exported, test-only view of the per-SILK-frame
// silk_encoder_control_FLP state captured at iter==0 (after process_gains,
// before the rate-control loop). It mirrors the C oracle dump in
// tools/csrc/silk_encode_frame_FLP_dump.c so a root-package comparison test
// can bisect the first diverging shaping/NSQ quantity vs libopus.
type SILKCtrlSnapshot struct {
	SignalType   int
	QuantOffset  int
	NbSubfr      int
	LambdaQ10    int32
	GainsQ16     [4]int32
	TiltQ14      [4]int32
	HarmShapeQ14 [4]int32
	LFShpQ14     [4]int32
	ARShpQ13     [4 * maxShapeLpcOrder]int16
	PitchL       [4]int32

	// Frame-level SILK rate-control inputs that drive the residual-quantizer SNR
	// and Lambda, captured so a parity test can bisect a per-frame size delta to
	// the rate-control stage (SNR_dB_Q7 / coding_quality) rather than the shaping
	// or NSQ stages.
	SNRdBQ7       int32
	InQBandsQ15   [4]int32
	SpeechActivQ8 int32
	CodingQuality float32
	InputQuality  float32
	TargetRateBps int32
}

// SILKEncodeStage identifies a boundary in the per-frame rate-control loop.
type SILKEncodeStage uint8

const (
	SILKEncodeAfterNSQ SILKEncodeStage = iota + 1
	SILKEncodeAfterIndices
	SILKEncodeAfterPulses
)

// SILKEncodeStageSnapshot is a test-only copy of the NSQ output and encoded
// side-information state at one rate-control iteration boundary. Pulses are
// valid only for the duration of the callback.
type SILKEncodeStageSnapshot struct {
	Stage            SILKEncodeStage
	Iteration        int
	Tell             int
	Range            uint32
	SignalType       int8
	QuantOffsetType  int8
	Seed             int8
	LagIndex         int16
	ContourIndex     int8
	NLSFInterpCoefQ2 int8
	PERIndex         int8
	LTPScaleIndex    int8
	GainIndices      [maxNbSubfr]int8
	LTPIndices       [maxNbSubfr]int8
	NLSFIndices      [maxLPCOrder + 1]int8
	Pulses           []int8
}

// WithSILKCtrlSnapshotHook installs a per-frame snapshot callback for the
// duration of fn. The callback fires once per SILK frame at the iter==0
// AfterPulses trace point.
func WithSILKCtrlSnapshotHook(cb func(SILKCtrlSnapshot), fn func()) {
	WithSILKEncodeTraceSnapshotHooks(cb, nil, fn)
}

// WithSILKEncodeTraceSnapshotHooks installs frame-control and rate-control
// stage callbacks for the duration of fn.
func WithSILKEncodeTraceSnapshotHooks(
	ctrlCB func(SILKCtrlSnapshot),
	stageCB func(*Encoder, SILKEncodeStageSnapshot),
	fn func(),
) {
	withEncodeFrameTraceHook(func(e *Encoder, tr encodeFrameTrace) {
		if ctrlCB != nil && tr.iter == 0 && tr.stage == encodeFrameTraceAfterPulses {
			var s SILKCtrlSnapshot
			s.SignalType = tr.ctrlSignalType
			s.QuantOffset = tr.ctrlQuantOffset
			s.NbSubfr = tr.ctrlNbSubfr
			s.LambdaQ10 = tr.ctrlLambdaQ10
			s.GainsQ16 = tr.ctrlGainsQ16
			s.TiltQ14 = tr.ctrlTiltQ14
			s.HarmShapeQ14 = tr.ctrlHarmShapeQ14
			s.LFShpQ14 = tr.ctrlLFShpQ14
			s.ARShpQ13 = tr.ctrlARShpQ13
			s.PitchL = tr.ctrlPitchL
			s.SNRdBQ7 = e.snrDBQ7
			s.InQBandsQ15 = e.inputQualityBandsQ15
			s.SpeechActivQ8 = e.speechActivityQ8
			s.CodingQuality = tr.ctrlCodingQual
			s.InputQuality = tr.ctrlInputQual
			s.TargetRateBps = e.targetRateBps
			ctrlCB(s)
		}
		if stageCB == nil {
			return
		}
		var stage SILKEncodeStage
		switch tr.stage {
		case encodeFrameTraceAfterNSQ:
			stage = SILKEncodeAfterNSQ
		case encodeFrameTraceAfterIndices:
			stage = SILKEncodeAfterIndices
		case encodeFrameTraceAfterPulses:
			stage = SILKEncodeAfterPulses
		default:
			return
		}
		stageCB(e, SILKEncodeStageSnapshot{
			Stage:            stage,
			Iteration:        tr.iter,
			Tell:             tr.tell,
			Range:            tr.rng,
			SignalType:       tr.indices.signalType,
			QuantOffsetType:  tr.indices.quantOffsetType,
			Seed:             tr.indices.Seed,
			LagIndex:         tr.indices.lagIndex,
			ContourIndex:     tr.indices.contourIndex,
			NLSFInterpCoefQ2: tr.indices.NLSFInterpCoefQ2,
			PERIndex:         tr.indices.PERIndex,
			LTPScaleIndex:    tr.indices.LTPScaleIndex,
			GainIndices:      tr.indices.GainsIndices,
			LTPIndices:       tr.indices.LTPIndex,
			NLSFIndices:      tr.indices.NLSFIndices,
			Pulses:           tr.pulses,
		})
	}, fn)
}
