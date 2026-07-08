//go:build gopus_osce || gopus_dred

package multistream

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/encoder"
)

// DREDModelLoaded reports whether all stream encoders have a DRED-capable blob.
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

// SetDREDDuration propagates libopus-style DRED duration to all stream encoders.
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

// DREDDuration reports the DRED duration from the first stream encoder.
func (e *Encoder) DREDDuration() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].DREDDuration()
	}
	return 0
}
