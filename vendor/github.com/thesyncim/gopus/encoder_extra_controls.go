//go:build gopus_osce || gopus_dred

package gopus

import encpkg "github.com/thesyncim/gopus/internal/encoder"

// SetDREDDuration sets the maximum DRED redundancy depth in 10 ms frames.
// Values from 0 through 104 are accepted; zero disables DRED emission. A
// positive duration can produce redundancy only when a DRED-capable model is
// loaded. This method is available in builds tagged gopus_dred or gopus_osce;
// it returns ErrInvalidArgument outside the supported range.
func (e *Encoder) SetDREDDuration(duration int) error {
	if err := e.enc.SetDREDDuration(duration); err != nil {
		if err == encpkg.ErrInvalidDREDDuration {
			return ErrInvalidArgument
		}
		return err
	}
	return nil
}

// DREDDuration reports the configured DRED redundancy depth in 10 ms frames.
// Zero disables emission. A positive value is a configured limit, not a
// guarantee that a packet carries DRED; a compatible model must also be loaded.
func (e *Encoder) DREDDuration() (int, error) {
	return e.enc.DREDDuration(), nil
}
