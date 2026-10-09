//go:build gopus_osce

package gopus

// SetOSCEBWE enables or disables the OSCE bandwidth-extension request in
// builds with -tags gopus_osce. Enabling the request runs BWE only when a
// compatible model is loaded and the decoded SILK frame is wideband at the
// 16 kHz internal rate with a 48 kHz API rate and complexity of at least 4.
//
// The default gopus build keeps this outside the public API surface.
func (d *Decoder) SetOSCEBWE(enabled bool) error {
	d.osceBWEEnabled = enabled
	return nil
}

// OSCEBWE reports the configured BWE request bit in explicit extra-controls
// builds. A true result does not mean the most recent frame used BWE; model and
// frame eligibility still apply.
func (d *Decoder) OSCEBWE() (bool, error) {
	return d.osceBWEEnabled, nil
}

// SetOSCELACE sets an explicit OSCE LACE/NoLACE postfilter override in
// gopus_osce builds. Without an override, decoder complexity selects the
// method as libopus does.
//
// The default gopus build keeps this outside the public API surface.
// libopus selects between OSCE_METHOD_NONE / OSCE_METHOD_LACE / OSCE_METHOD_NOLACE
// based on decoder complexity (>=6 enables LACE, >=7 enables NoLACE); this
// boolean control gates whether the gopus decoder runs either postfilter on
// the SILK lowband output before the silk_resampler / OSCE BWE stages.
func (d *Decoder) SetOSCELACE(enabled bool) error {
	d.osceLACEEnabled = enabled
	d.osceLACEOverrideSet = true
	return nil
}

// OSCELACE reports whether the LACE/NoLACE gate is enabled by the explicit
// override or decoder complexity. It does not report whether the most recent
// frame used either filter; compatible models and eligible SILK frames are
// checked during decoding.
func (d *Decoder) OSCELACE() (bool, error) {
	return d.osceLACEEnabledForComplexity(), nil
}
