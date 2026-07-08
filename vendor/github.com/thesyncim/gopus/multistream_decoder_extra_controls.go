//go:build gopus_osce

package gopus

// SetOSCEBWE exposes the extra libopus ENABLE_OSCE_BWE control only
// when built with -tags gopus_osce.
//
// The default gopus build keeps this outside the public API surface.
func (d *MultistreamDecoder) SetOSCEBWE(enabled bool) error {
	d.dec.SetOSCEBWE(enabled)
	return nil
}

// OSCEBWE reports decoder-side OSCE bandwidth-extension state for explicit
// extra-controls builds.
func (d *MultistreamDecoder) OSCEBWE() (bool, error) {
	return d.dec.OSCEBWE(), nil
}

// SetOSCELACE exposes the extra libopus OSCE LACE/NoLACE postfilter
// activation control only when built with -tags gopus_osce.
//
// The default gopus build keeps this outside the public API surface.
// The control is fanned out to every child stream decoder, matching how
// libopus toggles `DecControl.osce_method` on each per-stream decoder.
func (d *MultistreamDecoder) SetOSCELACE(enabled bool) error {
	d.dec.SetOSCELACE(enabled)
	return nil
}

// OSCELACE reports decoder-side OSCE LACE/NoLACE postfilter activation state
// for explicit extra-controls builds.
func (d *MultistreamDecoder) OSCELACE() (bool, error) {
	return d.dec.OSCELACE(), nil
}
