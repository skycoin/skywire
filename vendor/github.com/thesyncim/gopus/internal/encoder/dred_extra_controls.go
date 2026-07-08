//go:build gopus_osce || gopus_dred

package encoder

import internaldred "github.com/thesyncim/gopus/internal/dred"

// DREDModelLoaded reports whether the retained blob is DRED-encoder capable.
func (e *Encoder) DREDModelLoaded() bool {
	return e.dredModelsLoaded()
}

// DREDReady reports whether DRED can be emitted on the next packet.
func (e *Encoder) DREDReady() bool {
	return e.dredModelsLoaded() && e.DREDDuration() > 0
}

// SetDREDDuration stores libopus-style DRED redundancy depth in 10 ms units.
func (e *Encoder) SetDREDDuration(duration int) error {
	if duration < 0 || duration > internaldred.MaxFrames {
		return ErrInvalidDREDDuration
	}
	if duration == 0 && e.dred == nil {
		return nil
	}
	if duration > 0 {
		e.ensureExtensionPacketScratch()
	}
	extra := e.ensureDREDExtras()
	extra.duration = int32(duration)
	if duration == 0 {
		e.pruneDREDExtrasIfDormant()
	}
	return nil
}

// DREDDuration reports the stored DRED redundancy depth in 10 ms units.
func (e *Encoder) DREDDuration() int {
	if e.dred == nil {
		return 0
	}
	return int(e.dred.duration)
}
