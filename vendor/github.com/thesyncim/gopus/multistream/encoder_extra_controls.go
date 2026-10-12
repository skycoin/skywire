//go:build gopus_osce || gopus_dred

package multistream

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/encoder"
)

// DREDModelLoaded reports whether all stream encoders have a DRED-capable blob.
// DRED methods in this file are available in builds tagged gopus_dred or
// gopus_osce.
func (e *Encoder) DREDModelLoaded() bool {
	if len(e.encoders) == 0 {
		return false
	}
	for _, enc := range e.encoders {
		if !enc.DREDModelLoaded() {
			return false
		}
	}
	return true
}

// DREDReady reports whether all stream encoders are ready to emit DRED.
func (e *Encoder) DREDReady() bool {
	if len(e.encoders) == 0 {
		return false
	}
	for _, enc := range e.encoders {
		if !enc.DREDReady() {
			return false
		}
	}
	return true
}

// SetDREDDuration sets the maximum number of 10 ms DRED redundancy frames for all
// streams. Values from 0 through 104 are accepted; zero disables DRED emission.
func (e *Encoder) SetDREDDuration(duration int) error {
	if duration < 0 || duration > internaldred.MaxFrames {
		return encoder.ErrInvalidDREDDuration
	}
	for _, enc := range e.encoders {
		if err := enc.SetDREDDuration(duration); err != nil {
			return err
		}
	}
	return nil
}

// DREDDuration reports the configured DRED redundancy depth in 10 ms frames;
// zero means DRED emission is disabled.
func (e *Encoder) DREDDuration() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].DREDDuration()
	}
	return 0
}
