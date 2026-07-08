package lpcnetplc

// PredictorSnapshot exposes the retained predictor GRU state for parity tests.
type PredictorSnapshot struct {
	GRU1 [GRU1Size]float32
	GRU2 [GRU2Size]float32
}

// StateSnapshot exposes the retained PLC state needed by decoder-level parity
// tests without changing the production hot path.
type StateSnapshot struct {
	Blend       int
	LossCount   int
	AnalysisGap int
	AnalysisPos int
	PredictPos  int
	FECReadPos  int
	FECFillPos  int
	FECSkip     int
	Features    [NumTotalFeatures]float32
	Cont        [ContVectors * NumFeatures]float32
	PCM         [PLCBufSize]float32
	PLCNet      PredictorSnapshot
	PLCBak      [2]PredictorSnapshot
}

// FARGANSnapshot exposes the retained FARGAN state needed by decoder-level
// parity tests.
type FARGANSnapshot struct {
	ContInitialized bool
	DeemphMem       float32
	PitchBuf        [PitchMaxPeriod]float32
	CondConv1State  [FARGANCondConv1State]float32
	FWC0Mem         [SigNetFWC0StateSize]float32
	GRU1State       [SigNetGRU1StateSize]float32
	GRU2State       [SigNetGRU2StateSize]float32
	GRU3State       [SigNetGRU3StateSize]float32
	LastPeriod      int
}

// Snapshot returns a copy of the retained PLC state for parity tests.
func (s *State) Snapshot() StateSnapshot {
	if s == nil {
		return StateSnapshot{}
	}
	s.ensureRuntimeInit()
	snap := StateSnapshot{
		Blend:       s.blend,
		LossCount:   s.lossCount,
		AnalysisGap: s.analysisGap,
		AnalysisPos: s.analysisPos,
		PredictPos:  s.predictPos,
		FECReadPos:  s.fecReadPos,
		FECFillPos:  s.fecFillPos,
		FECSkip:     s.fecSkip,
	}
	copy(snap.Features[:], s.features[:])
	copy(snap.Cont[:], s.cont[:])
	copy(snap.PCM[:], s.pcm[:])
	copy(snap.PLCNet.GRU1[:], s.plcNet.gru1[:])
	copy(snap.PLCNet.GRU2[:], s.plcNet.gru2[:])
	for i := range s.plcBak {
		copy(snap.PLCBak[i].GRU1[:], s.plcBak[i].gru1[:])
		copy(snap.PLCBak[i].GRU2[:], s.plcBak[i].gru2[:])
	}
	return snap
}

// Snapshot returns a copy of the retained FARGAN runtime state for parity
// tests.
func (f *FARGAN) Snapshot() FARGANSnapshot {
	if f == nil {
		return FARGANSnapshot{}
	}
	return FARGANSnapshot{
		ContInitialized: f.state.contInitialized,
		DeemphMem:       f.state.deemphMem,
		PitchBuf:        f.state.pitchBuf,
		CondConv1State:  f.state.condConv1State,
		FWC0Mem:         f.state.fwc0Mem,
		GRU1State:       f.state.gru1State,
		GRU2State:       f.state.gru2State,
		GRU3State:       f.state.gru3State,
		LastPeriod:      f.state.lastPeriod,
	}
}
