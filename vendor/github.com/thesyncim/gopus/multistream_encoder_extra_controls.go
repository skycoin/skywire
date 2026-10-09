//go:build gopus_osce || gopus_dred

package gopus

// SetDREDDuration sets the maximum number of 10 ms DRED redundancy frames for all
// streams. Values from 0 through 104 are accepted; zero disables DRED emission.
// This control is available in builds tagged gopus_dred or gopus_osce.
func (e *MultistreamEncoder) SetDREDDuration(duration int) error {
	if err := e.enc.SetDREDDuration(duration); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

// DREDDuration reports the configured DRED redundancy depth in 10 ms frames;
// zero means DRED emission is disabled. This control is available in builds
// tagged gopus_dred or gopus_osce.
func (e *MultistreamEncoder) DREDDuration() (int, error) {
	return e.enc.DREDDuration(), nil
}
