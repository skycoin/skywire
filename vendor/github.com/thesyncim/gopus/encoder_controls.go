package gopus

// SetBitrate sets the target bitrate in bits per second.
//
// Positive bitrates are clamped to the libopus range.
// Returns ErrInvalidBitrate for non-positive non-sentinel values.
func (e *Encoder) SetBitrate(bitrate int) error {
	if err := validateBitrate(bitrate); err != nil {
		return err
	}
	e.enc.SetBitrate(bitrate)
	return nil
}

// Bitrate returns the current target bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return e.enc.Bitrate()
}

// SetComplexity sets the encoder's computational complexity.
//
// complexity must be 0-10, where:
//   - 0-1: Minimal processing, fastest encoding
//   - 2-4: Basic analysis, good for real-time with limited CPU
//   - 5-7: Moderate analysis, balanced quality/speed
//   - 8-10: Thorough analysis, highest quality
//
// Returns ErrInvalidComplexity if out of range.
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

// SetBitrateMode sets the encoder bitrate control mode.
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

// SetMode sets the encoder's forced coding mode.
func (e *Encoder) SetMode(mode EncoderMode) error {
	if err := validateEncoderMode(mode); err != nil {
		return err
	}
	e.enc.SetMode(mode)
	e.modeSet = true
	return nil
}

// Mode returns the current forced coding mode.
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

// SetFEC enables or disables in-band Forward Error Correction.
//
// When enabled, the encoder includes redundant information for loss recovery.
// FEC is most effective when the receiver can request retransmission of lost packets.
func (e *Encoder) SetFEC(enabled bool) {
	e.enc.SetFEC(enabled)
}

// SetInBandFEC sets the libopus-compatible in-band FEC configuration.
func (e *Encoder) SetInBandFEC(config int) error {
	if err := validateInBandFEC(config); err != nil {
		return err
	}
	return e.enc.SetInBandFEC(config)
}

// FECEnabled returns whether FEC is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.enc.FECEnabled()
}

// InBandFEC returns the current in-band FEC configuration.
func (e *Encoder) InBandFEC() int {
	return e.enc.InBandFEC()
}

// SetPacketLoss sets the expected packet loss percentage.
//
// lossPercent must be in the range [0, 100].
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

// SetDTX enables or disables Discontinuous Transmission.
//
// When enabled, the encoder reduces bitrate during silence by:
//   - Suppressing packets entirely during silence
//   - Sending periodic comfort noise frames
//
// DTX is useful for VoIP applications to reduce bandwidth.
func (e *Encoder) SetDTX(enabled bool) {
	e.enc.SetDTX(enabled)
}

// DTXEnabled returns whether DTX is enabled.
func (e *Encoder) DTXEnabled() bool {
	return e.enc.DTXEnabled()
}

// InDTX reports whether the encoder is currently in DTX mode.
//
// This matches libopus OPUS_GET_IN_DTX semantics.
func (e *Encoder) InDTX() bool {
	return e.enc.InDTX()
}

// VADActivity returns the current VAD speech activity level in Q8 (0-255).
func (e *Encoder) VADActivity() int {
	return e.enc.GetVADActivity()
}

// SetExpertFrameDuration sets the preferred frame duration policy.
//
// `ExpertFrameDurationArg` keeps using the current `FrameSize()` value. Fixed
// durations are applied during Encode when they fit within the current frame.
func (e *Encoder) SetExpertFrameDuration(duration ExpertFrameDuration) error {
	return setExpertFrameDuration(duration, &e.expertFrameDuration)
}

// ExpertFrameDuration returns the current expert frame duration policy.
func (e *Encoder) ExpertFrameDuration() ExpertFrameDuration {
	return e.expertFrameDuration
}

// SetFrameSize sets the frame size in samples at 48kHz.
//
// Valid sizes depend on the encoding mode:
//   - SILK: 480, 960, 1920, 2880, 3840, 4800, 5760 (10-120 ms)
//   - CELT: 120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760
//   - Hybrid: 480, 960, 1920, 2880, 3840, 4800, 5760
//
// Default is 960 (20ms).
func (e *Encoder) SetFrameSize(samples int) error {
	// At 96 kHz API rate, frame sizes are in 96 kHz samples (2x the 48 kHz size).
	// Convert to the 48 kHz internal frame size before validation. At sub-48 kHz
	// native rates the frame size is already in native (internal) samples.
	internal := samples
	if e.is96kHz() {
		internal = samples / 2
	}
	if err := validateFrameSize(internal, e.internalSampleRate(), e.application); err != nil {
		return err
	}
	e.frameSize = int32(internal)
	e.enc.SetFrameSize(internal)
	return nil
}

// internalSampleRate returns the sample rate of the internal encode pipeline:
// the native API rate for 8/12/16/24/48 kHz, and 48 kHz for the 96 kHz API
// (which decimates 2:1 at the input boundary).
func (e *Encoder) internalSampleRate() int {
	if e.is96kHz() {
		return 48000
	}
	return int(e.sampleRate)
}

// FrameSize returns the current frame size in samples at the API sample rate.
// At 96 kHz, this is 2x the 48 kHz internal frame size.
func (e *Encoder) FrameSize() int {
	return e.apiFrameSize()
}

// Reset clears the encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	// e.enc.Reset() sets the encoder's first-frame flag, so SetApplication's
	// FirstFrameCoded() gate is released; no separate wrapper flag is needed.
	e.enc.Reset()
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return int(e.channels)
}

// SampleRate returns the sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// FinalRange returns the final range coder state after encoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after Encode() to get a meaningful value.
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

// SetBandwidth sets the target audio bandwidth.
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

// Bandwidth returns the currently configured target bandwidth.
func (e *Encoder) Bandwidth() Bandwidth {
	return e.enc.Bandwidth()
}

// SetMaxBandwidth sets the maximum audio bandwidth.
//
// The encoder will not use a bandwidth higher than this limit.
// This is useful for limiting bandwidth without changing the sample rate.
//
// Valid values are:
//   - BandwidthNarrowband (4kHz)
//   - BandwidthMediumband (6kHz)
//   - BandwidthWideband (8kHz)
//   - BandwidthSuperwideband (12kHz)
//   - BandwidthFullband (20kHz)
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

// SetForceChannels forces the encoder to use a specific channel count.
//
// channels must be one of:
//   - -1: automatic (use input channels)
//   - 1: force mono output
//   - 2: force stereo output
//
// Note: Forcing stereo on mono input will duplicate the channel.
// Forcing mono on stereo input will downmix to mono.
//
// Returns ErrInvalidForceChannels if the value is not valid.
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

// ForceChannels returns the forced channel count (-1 = auto).
func (e *Encoder) ForceChannels() int {
	return e.enc.ForceChannels()
}

// Lookahead returns the encoder's algorithmic delay in samples.
//
// Matches libopus OPUS_GET_LOOKAHEAD behavior:
//   - Base lookahead is Fs/400 (2.5ms)
//   - Delay compensation Fs/250 is included for VoIP/Audio
//   - Delay compensation is omitted for LowDelay
func (e *Encoder) Lookahead() int {
	return lookaheadSamples(int(e.sampleRate), e.application)
}

// SetLSBDepth sets the bit depth of the input signal.
//
// depth must be 8-24. This affects DTX silence detection:
// lower bit depths have a higher noise floor, so the encoder
// adjusts its silence threshold accordingly.
//
// Default is 24 (full precision).
// Returns ErrInvalidLSBDepth if out of range.
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

// SetPredictionDisabled disables inter-frame prediction.
//
// When disabled (true), each frame can be decoded independently,
// which improves error resilience at the cost of compression efficiency.
// This is useful for applications with high packet loss.
//
// Default is false (prediction enabled).
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
