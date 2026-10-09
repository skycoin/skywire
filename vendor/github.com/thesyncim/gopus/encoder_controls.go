package gopus

// SetBitrate sets the target bitrate in bits per second. Positive values are
// clamped to the range supported by libopus for this stream. BitrateAuto and
// BitrateMax select the corresponding libopus sentinel modes. It returns
// ErrInvalidBitrate for other non-positive values.
func (e *Encoder) SetBitrate(bitrate int) error {
	if err := validateBitrate(bitrate); err != nil {
		return err
	}
	e.enc.SetBitrate(bitrate)
	return nil
}

// Bitrate returns the configured target bitrate, including BitrateAuto and
// BitrateMax when either sentinel mode is selected.
func (e *Encoder) Bitrate() int {
	return e.enc.Bitrate()
}

// SetComplexity sets encoder analysis and coding effort from 0 through 10.
// Higher values spend more CPU on analysis and search, but do not guarantee a
// better result for every input. It returns ErrInvalidComplexity outside this
// range.
func (e *Encoder) SetComplexity(complexity int) error {
	if err := validateComplexity(complexity); err != nil {
		return err
	}
	e.enc.SetComplexity(complexity)
	return nil
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return e.enc.Complexity()
}

// SetBitrateMode selects variable (VBR), constrained-variable (CVBR), or
// constant (CBR) packet sizing. It returns ErrInvalidBitrateMode for an
// unsupported mode.
func (e *Encoder) SetBitrateMode(mode BitrateMode) error {
	if err := validateBitrateMode(mode); err != nil {
		return err
	}
	e.enc.SetBitrateMode(mode)
	return nil
}

// BitrateMode returns the active encoder bitrate control mode.
func (e *Encoder) BitrateMode() BitrateMode {
	return e.enc.GetBitrateMode()
}

// SetMode selects automatic mode choice or forces SILK, Hybrid, or CELT coding
// where the application and frame duration permit it. EncoderModeAuto restores
// automatic selection. It returns ErrInvalidArgument for an unsupported mode.
func (e *Encoder) SetMode(mode EncoderMode) error {
	if err := validateEncoderMode(mode); err != nil {
		return err
	}
	e.enc.SetMode(mode)
	e.modeSet = true
	return nil
}

// Mode returns the configured coding mode. EncoderModeAuto means the encoder
// selects a mode for each frame.
func (e *Encoder) Mode() EncoderMode {
	return e.enc.Mode()
}

// SetVBR enables or disables VBR mode.
//
// Disabling VBR switches to CBR. Enabling VBR restores VBR while preserving
// the current VBR constraint state.
func (e *Encoder) SetVBR(enabled bool) {
	e.enc.SetVBR(enabled)
}

// VBR returns whether VBR mode is enabled.
func (e *Encoder) VBR() bool {
	return e.enc.VBR()
}

// SetVBRConstraint enables or disables VBR constraint.
//
// This setting is remembered even while VBR is disabled.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	e.enc.SetVBRConstraint(constrained)
}

// VBRConstraint returns whether VBR constraint is enabled.
func (e *Encoder) VBRConstraint() bool {
	return e.enc.VBRConstraint()
}

// SetFEC enables or disables in-band Forward Error Correction. When the
// encoder produces redundancy, it carries SILK data for a preceding frame
// in a later SILK or Hybrid packet. SetPacketLoss supplies the expected
// loss percentage used by the encoder.
func (e *Encoder) SetFEC(enabled bool) {
	e.enc.SetFEC(enabled)
}

// SetInBandFEC selects the in-band FEC policy: InBandFECDisabled,
// InBandFECEnabled, or InBandFECMusicSafe. The last policy lets the encoder
// omit redundancy for music-classified frames. It returns ErrInvalidFECConfig
// for any other value.
func (e *Encoder) SetInBandFEC(config int) error {
	if err := validateInBandFEC(config); err != nil {
		return err
	}
	return e.enc.SetInBandFEC(config)
}

// FECEnabled reports whether an in-band FEC policy is enabled. It does not
// indicate whether a particular packet contains redundancy.
func (e *Encoder) FECEnabled() bool {
	return e.enc.FECEnabled()
}

// InBandFEC returns the configured in-band FEC policy.
func (e *Encoder) InBandFEC() int {
	return e.enc.InBandFEC()
}

// SetPacketLoss sets the expected packet loss percentage from 0 through 100.
// The estimate informs redundancy decisions when in-band FEC is enabled. It
// returns ErrInvalidPacketLoss outside this range.
func (e *Encoder) SetPacketLoss(lossPercent int) error {
	if err := validatePacketLoss(lossPercent); err != nil {
		return err
	}
	e.enc.SetPacketLoss(lossPercent)
	return nil
}

// PacketLoss returns the configured expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return e.enc.PacketLoss()
}

// SetDTX enables or disables discontinuous transmission during silence.
// During DTX, Encode can return no packet or a short one- or two-byte packet.
// DTXEnabled reports the configured setting; InDTX reports encoder activity.
func (e *Encoder) SetDTX(enabled bool) {
	e.enc.SetDTX(enabled)
}

// DTXEnabled reports whether the DTX setting is enabled; InDTX reports whether
// the encoder is currently in its DTX state.
func (e *Encoder) DTXEnabled() bool {
	return e.enc.DTXEnabled()
}

// InDTX reports whether the encoder is currently in DTX mode.
//
// This matches libopus OPUS_GET_IN_DTX semantics.
func (e *Encoder) InDTX() bool {
	return e.enc.InDTX()
}

// VADActivity returns the latest available Opus-level activity estimate in
// Q8 (0-255), not a percentage or the standalone SILK VAD result. It reads the
// most recent encoder decision without analyzing new PCM. It returns 0 before
// the first decision, after Reset, or when no Opus activity decision is available.
func (e *Encoder) VADActivity() int {
	return e.enc.GetVADActivity()
}

// SetExpertFrameDuration selects the frame duration used by Encode. The
// ExpertFrameDurationArg setting uses FrameSize(); a fixed duration selects that
// duration from the configured input frame. Encode still requires the full
// configured frame and uses the selected prefix. A duration longer than the
// configured frame fails with ErrInvalidFrameSize. Unsupported values return
// ErrInvalidArgument.
func (e *Encoder) SetExpertFrameDuration(duration ExpertFrameDuration) error {
	return setExpertFrameDuration(duration, &e.expertFrameDuration)
}

// ExpertFrameDuration returns the current expert frame duration policy.
func (e *Encoder) ExpertFrameDuration() ExpertFrameDuration {
	return e.expertFrameDuration
}

// SetFrameSize sets the input frame size in samples per channel at the API
// rate. At 96 kHz the wrapper stores a 48 kHz-equivalent count while the core
// receives the native-rate size. It returns ErrInvalidFrameSize for a duration
// that Opus does not support or that violates the restricted-SILK minimum.
func (e *Encoder) SetFrameSize(samples int) error {
	// Validate at the API rate before converting the wrapper's bookkeeping.
	// libopus src/opus_encoder.c:frame_size_select requires an exact duration;
	// dividing an odd 96 kHz count first would silently round it down.
	if err := validateFrameSize(samples, int(e.sampleRate), e.application); err != nil {
		return err
	}
	internal := samples
	if e.is96kHz() {
		internal = samples / 2
	}
	e.frameSize = int32(internal)
	coreFrameSize := internal
	if e.is96kHz() {
		coreFrameSize = samples
	}
	e.enc.SetFrameSize(coreFrameSize)
	return nil
}

// internalSampleRate returns the sample rate used by the wrapper's frame-size
// bookkeeping. The 96 kHz API stores a 48 kHz-equivalent count while the core
// encoder retains native 96 kHz state.
func (e *Encoder) internalSampleRate() int {
	if e.is96kHz() {
		return 48000
	}
	return int(e.sampleRate)
}

// FrameSize returns the configured input frame size in samples per channel at
// the API rate. A fixed ExpertFrameDuration can make an Encode call code a
// shorter prefix. At 96 kHz, FrameSize is twice the 48 kHz-equivalent size.
func (e *Encoder) FrameSize() int {
	return e.apiFrameSize()
}

// Reset clears codec and analysis history, resets the final range, and
// restarts first-frame processing for a new stream. It retains the sample rate,
// channel count, configured frame size, application, and ordinary controls.
// The selected bandwidth is reinitialized; an explicit bandwidth request
// remains configured. In builds with DRED controls, Reset sets DREDDuration
// to zero, disabling DRED emission.
func (e *Encoder) Reset() {
	// e.enc.Reset() sets the encoder's first-frame flag, so SetApplication's
	// FirstFrameCoded() gate is released; no separate wrapper flag is needed.
	e.enc.Reset()
}

// Channels returns the configured input channel count: 1 for mono or 2 for
// stereo. SetForceChannels can request fewer coded output channels.
func (e *Encoder) Channels() int {
	return int(e.channels)
}

// SampleRate returns the configured input sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// FinalRange returns the range coder state from the most recent encode, matching
// libopus OPUS_GET_FINAL_RANGE. Reset sets it to zero.
func (e *Encoder) FinalRange() uint32 {
	return e.enc.FinalRange()
}

// SetSignal sets the signal type hint for mode selection.
//
// signal must be one of:
//   - SignalAuto: automatically detect signal type
//   - SignalVoice: optimize for speech (biases toward SILK)
//   - SignalMusic: optimize for music (biases toward CELT)
//
// Returns ErrInvalidSignal if the value is not valid.
func (e *Encoder) SetSignal(signal Signal) error {
	if err := validateSignal(signal); err != nil {
		return err
	}
	e.enc.SetSignalType(signal)
	return nil
}

// Signal returns the current signal type hint.
func (e *Encoder) Signal() Signal {
	return e.enc.SignalType()
}

// SetBandwidth requests a target audio bandwidth. The selected bandwidth can
// differ until a frame is encoded. It returns ErrInvalidBandwidth for an
// unsupported value.
func (e *Encoder) SetBandwidth(bandwidth Bandwidth) error {
	if err := validateBandwidth(bandwidth); err != nil {
		return err
	}
	e.enc.SetBandwidth(bandwidth)
	return nil
}

// SetBandwidthAuto restores automatic bandwidth selection.
func (e *Encoder) SetBandwidthAuto() error {
	e.enc.SetBandwidthAuto()
	return nil
}

// Bandwidth returns the bandwidth currently selected by the encoder. It can
// differ from the explicit request made by SetBandwidth until a frame is
// encoded.
func (e *Encoder) Bandwidth() Bandwidth {
	return e.enc.Bandwidth()
}

// SetMaxBandwidth caps the bandwidth selected for encoded audio without
// changing the input sample rate. It returns ErrInvalidBandwidth for an
// unsupported bandwidth.
func (e *Encoder) SetMaxBandwidth(bandwidth Bandwidth) error {
	if err := validateBandwidth(bandwidth); err != nil {
		return err
	}
	e.enc.SetMaxBandwidth(bandwidth)
	return nil
}

// MaxBandwidth returns the current maximum bandwidth limit.
func (e *Encoder) MaxBandwidth() Bandwidth {
	return e.enc.MaxBandwidth()
}

// SetForceChannels selects the coded channel count: -1 follows the configured
// input count, 1 forces mono, and 2 forces stereo. The requested count cannot
// exceed the configured input count; forcing mono from stereo downmixes the
// input. It returns ErrInvalidForceChannels for an unsupported or unavailable
// count.
func (e *Encoder) SetForceChannels(channels int) error {
	if err := validateForceChannels(channels); err != nil {
		return err
	}
	if channels > int(e.channels) {
		return ErrInvalidForceChannels
	}
	e.enc.SetForceChannels(channels)
	return nil
}

// ForceChannels returns the configured output-channel request: -1 follows the
// input channel count, while 1 or 2 forces that coded channel count.
func (e *Encoder) ForceChannels() int {
	return e.enc.ForceChannels()
}

// Lookahead returns the encoder delay in samples per channel at the API sample
// rate, matching libopus OPUS_GET_LOOKAHEAD. The base delay is 2.5 ms; VoIP,
// Audio, and RestrictedSilk add 4 ms of compensation. LowDelay and
// RestrictedCelt omit that compensation.
func (e *Encoder) Lookahead() int {
	return lookaheadSamples(int(e.sampleRate), e.application)
}

// SetLSBDepth sets the nominal input precision in bits, from 8 through 24.
// The value informs digital-silence detection and CELT noise-floor decisions;
// it does not rescale PCM samples. The default is 24. It returns
// ErrInvalidLSBDepth outside this range.
func (e *Encoder) SetLSBDepth(depth int) error {
	if err := validateLSBDepth(depth); err != nil {
		return err
	}
	e.enc.SetLSBDepth(depth)
	return nil
}

// LSBDepth returns the current input bit depth setting.
func (e *Encoder) LSBDepth() int {
	return e.enc.LSBDepth()
}

// SetPredictionDisabled disables inter-frame prediction. This reduces
// dependence on preceding frames and can improve resilience to packet loss at the
// cost of compression efficiency. The default is false.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.enc.SetPredictionDisabled(disabled)
}

// PredictionDisabled returns whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	return e.enc.PredictionDisabled()
}

// SetPhaseInversionDisabled disables stereo phase inversion.
//
// Phase inversion is a technique used to improve stereo decorrelation.
// Some audio processing pipelines may have issues with phase-inverted audio.
// Disabling it (true) ensures no phase inversion is applied.
//
// Default is false (phase inversion enabled).
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.enc.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.enc.PhaseInversionDisabled()
}
