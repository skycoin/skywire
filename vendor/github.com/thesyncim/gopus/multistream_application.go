package gopus

// SetApplication changes the encoder application hint before any stream has
// committed a frame. Reapplying the current hint remains valid afterward. An
// encode that returns a low-space packet without committing a child frame
// leaves the application mutable.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *MultistreamEncoder) SetApplication(application Application) error {
	if err := validateMutableApplication(e.application, e.enc.AnyStreamHasCodedFrame(), application); err != nil {
		return err
	}
	previousApplication := e.application
	settings, err := settingsForApplication(application)
	if err != nil {
		return err
	}
	if !e.modeSet && previousApplication == ApplicationLowDelay {
		e.enc.SetMode(EncoderModeAuto)
	}
	e.application = application
	e.enc.SetLowDelay(settings.lowDelay)
	e.enc.SetVoIPApplication(settings.voip)
	e.enc.SetRestrictedSilkApplication(false)
	return nil
}

// Application returns the current encoder application hint.
func (e *MultistreamEncoder) Application() Application {
	return e.application
}

// applyApplication records the application hint and forwards per-stream policy.
//
// Match libopus multistream OPUS_SET_APPLICATION forwarding semantics while
// preserving bitrate/complexity controls.
func (e *MultistreamEncoder) applyApplication(app Application) error {
	settings, err := settingsForApplication(app)
	if err != nil {
		return err
	}
	e.application = app
	e.enc.SetLowDelay(settings.lowDelay)
	e.enc.SetVoIPApplication(settings.voip)
	e.enc.SetRestrictedSilkApplication(app == ApplicationRestrictedSilk)
	e.enc.SetMode(settings.mode)
	e.enc.SetBandwidth(settings.bandwidth)
	e.enc.SetBandwidthAuto()
	e.enc.SetSignal(settings.signal)
	return nil
}
