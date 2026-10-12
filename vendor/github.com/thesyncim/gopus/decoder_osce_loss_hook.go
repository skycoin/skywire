//go:build gopus_osce

package gopus

func (d *Decoder) initOSCELossHook() {
	d.silkDecoder.SetNativeLossHook(d.resetOSCELACELostChannel)
}

// resetOSCELACELostChannel mirrors osce_reset in silk/decode_frame.c. A lost
// channel resets its selected filter and two-frame fade without changing the
// other channel's state or the selected method.
func (d *Decoder) resetOSCELACELostChannel(channel int) {
	s := d.osceLACE
	if s == nil || channel < 0 || channel >= len(s.laceResetFrames) {
		return
	}
	s.osceLACEFeatures[channel].Reset()
	switch s.laceMethod {
	case osceLACEModeLACE:
		s.osceLACERuntime[channel].Reset()
	case osceLACEModeNoLACE:
		s.osceNoLACERuntime[channel].Reset()
	}
	s.laceResetFrames[channel] = 2
}
