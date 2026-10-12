//go:build gopus_osce

package multistream

func (d *streamState) initOSCELossHook() {
	d.silkDec.SetNativeLossHook(d.resetOSCELACELostChannel)
}

// resetOSCELACELostChannel follows the per-channel osce_reset call in
// silk/decode_frame.c, retaining the selected method and the other channel.
func (d *streamState) resetOSCELACELostChannel(channel int) {
	s := d.osceState
	if s == nil || channel < 0 || channel >= len(s.laceResetFrames) {
		return
	}
	s.laceFeatureState[channel].Reset()
	switch s.laceMethod {
	case streamOSCELACEModeLACE:
		s.laceRuntime[channel].Reset()
	case streamOSCELACEModeNoLACE:
		s.noLACERuntime[channel].Reset()
	}
	s.laceResetFrames[channel] = 2
}
