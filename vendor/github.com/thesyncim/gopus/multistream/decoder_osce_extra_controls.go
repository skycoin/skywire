//go:build gopus_osce

package multistream

// OSCEModelsLoaded reports whether the retained blob contains the LACE and
// NoLACE OSCE model families.
func (d *Decoder) OSCEModelsLoaded() bool {
	return d.osceModelsLoaded
}

// OSCEBWEModelLoaded reports whether the retained blob contains the OSCE_BWE
// model family. OSCE methods in this file are available in builds tagged
// gopus_osce.
func (d *Decoder) OSCEBWEModelLoaded() bool {
	return d.osceBWEModelLoaded
}

// SetOSCEBWE stores the OSCE_BWE enable setting and propagates it to every
// child stream decoder, matching libopus OPUS_SET_OSCE_BWE. It does not load
// the OSCE_BWE model; SetDNNBlob handles model loading separately.
func (d *Decoder) SetOSCEBWE(enabled bool) {
	d.osceBWEEnabled = enabled
	for _, dec := range d.decoders {
		if s, ok := dec.(*streamState); ok {
			s.setOSCEBWEEnabled(enabled)
		}
	}
}

// OSCEBWE reports the stored tag-gated OSCE_BWE enable state.
func (d *Decoder) OSCEBWE() bool {
	return d.osceBWEEnabled
}

// SetOSCELACE explicitly overrides the complexity-based OSCE LACE/NoLACE
// selection and fans the setting out to every child stream decoder. Without an
// explicit override, each child follows libopus and selects no method below
// complexity 6, LACE at 6, and NoLACE at 7 or above.
func (d *Decoder) SetOSCELACE(enabled bool) {
	d.osceLACEEnabled = enabled
	d.osceLACEOverrideSet = true
	for _, dec := range d.decoders {
		if s, ok := dec.(*streamState); ok {
			s.setOSCELACEEnabled(enabled)
		}
	}
}

// OSCELACE reports whether the OSCE LACE/NoLACE gate is enabled by the
// explicit override or by decoder complexity.
func (d *Decoder) OSCELACE() bool {
	if d == nil {
		return false
	}
	if st := d.firstStreamState(); st != nil {
		return st.osceLACEEnabledForComplexity()
	}
	return false
}
