package gopus

// SetApplication updates the encoder application hint.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *Encoder) SetApplication(application Application) error {
	// libopus OPUS_SET_APPLICATION rejects an application change once a frame
	// has been committed (!st->first). st->first stays 1 through the SILK
	// nBytes==0 silence early return, so derive the gate from the encoder's
	// authoritative first-frame state rather than a wrapper-side "encoded once"
	// flag that flips on every Encode call.
	if err := validateMutableApplication(e.application, e.enc.FirstFrameCoded(), application); err != nil {
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
func (e *Encoder) Application() Application {
	return e.application
}

// applyApplication configures the encoder based on the application hint.
func (e *Encoder) applyApplication(app Application) error {
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
	e.enc.SetSignalType(settings.signal)
	return nil
}
