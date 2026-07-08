package celt

// PLCStateSnapshot exposes the libopus-shaped CELT PLC cadence state needed by
// parity tests around periodic/noise/neural/DRED concealment.
type PLCStateSnapshot struct {
	LastFrameType    int
	LossDuration     int
	PLCDuration      int
	LastPitchPeriod  int
	SkipPLC          bool
	PrefilterAndFold bool
}

// SnapshotPLCState returns the current CELT PLC cadence state.
func (d *Decoder) SnapshotPLCState() PLCStateSnapshot {
	if d == nil {
		return PLCStateSnapshot{}
	}
	return PLCStateSnapshot{
		LastFrameType:    int(d.plcLastFrameType),
		LossDuration:     int(d.plcLossDuration),
		PLCDuration:      int(d.plcDuration),
		LastPitchPeriod:  int(d.plcLastPitchPeriod),
		SkipPLC:          d.plcSkip,
		PrefilterAndFold: d.plcPrefilterAndFoldPending,
	}
}

// LastPLCFrameWasNeural reports whether the retained CELT lost-frame cadence is
// currently in a neural/DRED state.
func (d *Decoder) LastPLCFrameWasNeural() bool {
	if d == nil {
		return false
	}
	return plcFrameIsNeural(int(d.plcLastFrameType))
}

// SnapshotPreemphasisState returns the retained CELT deemphasis memory that
// libopus keeps in preemph_memD across decode and PLC/DRED loss handling.
func (d *Decoder) SnapshotPreemphasisState() [2]float32 {
	if d == nil {
		return [2]float32{}
	}
	var out [2]float32
	if len(d.preemphState) > 0 {
		out[0] = d.preemphState[0] * (1.0 / 32768.0)
	}
	if len(d.preemphState) > 1 {
		out[1] = d.preemphState[1] * (1.0 / 32768.0)
	}
	return out
}
