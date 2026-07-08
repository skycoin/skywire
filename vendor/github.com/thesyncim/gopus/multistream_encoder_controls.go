package gopus

import "github.com/thesyncim/gopus/types"

// SetFrameSize sets the frame size in native-Fs samples.
//
// Valid sizes are the per-rate Opus frame sizes: (Fs/400)<<n (2.5/5/10 ms) and
// n*Fs/50 (20/40/60/80/100/120 ms). At 48 kHz these are 120, 240, 480, 960,
// 1920, 2880, 3840, 4800, and 5760.
func (e *MultistreamEncoder) SetFrameSize(samples int) error {
	if err := validateFrameSize(samples, int(e.sampleRate), e.application); err != nil {
		return err
	}
	e.frameSize = int32(samples)
	return nil
}

// FrameSize returns the current frame size in native-Fs samples.
func (e *MultistreamEncoder) FrameSize() int {
	return int(e.frameSize)
}

// SetExpertFrameDuration sets the preferred frame duration policy for multistream encoding.
func (e *MultistreamEncoder) SetExpertFrameDuration(duration ExpertFrameDuration) error {
	return setMultistreamExpertFrameDuration(duration, &e.expertFrameDuration)
}

// ExpertFrameDuration returns the current multistream expert frame duration policy.
func (e *MultistreamEncoder) ExpertFrameDuration() ExpertFrameDuration {
	return e.expertFrameDuration
}

// SetBitrate sets the total target bitrate in bits per second.
//
// The bitrate is distributed across streams with coupled streams getting
// proportionally more bits than mono streams.
//
// Positive bitrates are clamped to 500*channels through 750000*channels.
func (e *MultistreamEncoder) SetBitrate(bitrate int) error {
	if err := validateBitrate(bitrate); err != nil {
		return err
	}
	e.enc.SetBitrate(bitrate)
	return nil
}

// Bitrate returns the current total target bitrate in bits per second.
func (e *MultistreamEncoder) Bitrate() int {
	return e.enc.Bitrate()
}

// SetComplexity sets the encoder's computational complexity for all streams.
//
// complexity must be 0-10, where:
//   - 0-1: Minimal processing, fastest encoding
//   - 2-4: Basic analysis, good for real-time with limited CPU
//   - 5-7: Moderate analysis, balanced quality/speed
//   - 8-10: Thorough analysis, highest quality
//
// Returns ErrInvalidComplexity if out of range.
func (e *MultistreamEncoder) SetComplexity(complexity int) error {
	if err := validateComplexity(complexity); err != nil {
		return err
	}
	e.enc.SetComplexity(complexity)
	return nil
}

// Complexity returns the current complexity setting.
func (e *MultistreamEncoder) Complexity() int {
	return e.enc.Complexity()
}

// SetBitrateMode sets the multistream encoder bitrate control mode.
func (e *MultistreamEncoder) SetBitrateMode(mode BitrateMode) error {
	if err := validateBitrateMode(mode); err != nil {
		return err
	}
	e.enc.SetBitrateMode(mode)
	return nil
}

// BitrateMode returns the active bitrate control mode.
func (e *MultistreamEncoder) BitrateMode() BitrateMode {
	return e.enc.BitrateMode()
}

// SetMode sets the forced coding mode for all stream encoders.
func (e *MultistreamEncoder) SetMode(mode EncoderMode) error {
	if err := validateEncoderMode(mode); err != nil {
		return err
	}
	e.enc.SetMode(mode)
	e.modeSet = true
	return nil
}

// Mode returns the forced coding mode from the first stream encoder.
func (e *MultistreamEncoder) Mode() EncoderMode {
	return e.enc.Mode()
}

// SetVBR enables or disables VBR mode.
//
// Disabling VBR switches to CBR. Enabling VBR restores VBR while preserving
// the current VBR constraint state.
func (e *MultistreamEncoder) SetVBR(enabled bool) {
	e.enc.SetVBR(enabled)
}

// VBR reports whether VBR mode is enabled.
func (e *MultistreamEncoder) VBR() bool {
	return e.enc.VBR()
}

// SetVBRConstraint enables or disables constrained VBR mode.
// This setting is remembered even while VBR is disabled.
func (e *MultistreamEncoder) SetVBRConstraint(constrained bool) {
	e.enc.SetVBRConstraint(constrained)
}

// VBRConstraint reports whether constrained VBR mode is enabled.
func (e *MultistreamEncoder) VBRConstraint() bool {
	return e.enc.VBRConstraint()
}

// SetFEC enables or disables in-band Forward Error Correction for all streams.
//
// When enabled, the encoders include redundant information for loss recovery.
func (e *MultistreamEncoder) SetFEC(enabled bool) {
	e.enc.SetFEC(enabled)
}

// SetInBandFEC sets the libopus-compatible in-band FEC configuration.
func (e *MultistreamEncoder) SetInBandFEC(config int) error {
	if err := validateInBandFEC(config); err != nil {
		return err
	}
	return e.enc.SetInBandFEC(config)
}

// FECEnabled returns whether FEC is enabled.
func (e *MultistreamEncoder) FECEnabled() bool {
	return e.enc.FECEnabled()
}

// InBandFEC returns the current in-band FEC configuration.
func (e *MultistreamEncoder) InBandFEC() int {
	return e.enc.InBandFEC()
}

// SetDTX enables or disables Discontinuous Transmission for all streams.
//
// When enabled, the encoders reduce bitrate during silence by emitting
// 1-byte TOC-only packets. The decoder handles CNG (Comfort Noise Generation).
func (e *MultistreamEncoder) SetDTX(enabled bool) {
	e.enc.SetDTX(enabled)
}

// DTXEnabled returns whether DTX is enabled.
func (e *MultistreamEncoder) DTXEnabled() bool {
	return e.enc.DTXEnabled()
}

// SetPacketLoss sets expected packet loss percentage for all stream encoders.
//
// lossPercent must be in the range [0, 100].
func (e *MultistreamEncoder) SetPacketLoss(lossPercent int) error {
	if err := validatePacketLoss(lossPercent); err != nil {
		return err
	}
	e.enc.SetPacketLoss(lossPercent)
	return nil
}

// PacketLoss returns expected packet loss percentage.
func (e *MultistreamEncoder) PacketLoss() int {
	return e.enc.PacketLoss()
}

// SetBandwidth sets the target audio bandwidth.
func (e *MultistreamEncoder) SetBandwidth(bw Bandwidth) error {
	if err := validateBandwidth(bw); err != nil {
		return err
	}
	e.enc.SetBandwidth(types.Bandwidth(bw))
	return nil
}

// SetBandwidthAuto restores automatic bandwidth selection for all stream encoders.
func (e *MultistreamEncoder) SetBandwidthAuto() error {
	e.enc.SetBandwidthAuto()
	return nil
}

// Bandwidth returns the currently configured target bandwidth.
func (e *MultistreamEncoder) Bandwidth() Bandwidth {
	return Bandwidth(e.enc.Bandwidth())
}

// SetForceChannels forces channel count on all stream encoders.
//
// channels must be -1 (auto), 1 (mono), or 2 (stereo).
func (e *MultistreamEncoder) SetForceChannels(channels int) error {
	if err := validateForceChannels(channels); err != nil {
		return err
	}
	if channels == 2 && e.enc.Streams() > e.enc.CoupledStreams() {
		return ErrInvalidForceChannels
	}
	e.enc.SetForceChannels(channels)
	return nil
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *MultistreamEncoder) ForceChannels() int {
	return e.enc.ForceChannels()
}

// SetPredictionDisabled toggles inter-frame prediction on all stream encoders.
func (e *MultistreamEncoder) SetPredictionDisabled(disabled bool) {
	e.enc.SetPredictionDisabled(disabled)
}

// PredictionDisabled reports whether inter-frame prediction is disabled.
func (e *MultistreamEncoder) PredictionDisabled() bool {
	return e.enc.PredictionDisabled()
}

// SetPhaseInversionDisabled toggles stereo phase inversion on all stream encoders.
func (e *MultistreamEncoder) SetPhaseInversionDisabled(disabled bool) {
	e.enc.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (e *MultistreamEncoder) PhaseInversionDisabled() bool {
	return e.enc.PhaseInversionDisabled()
}

// Reset clears the encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *MultistreamEncoder) Reset() {
	e.enc.Reset()
	e.encodedOnce = false
}

// Channels returns the number of audio channels.
func (e *MultistreamEncoder) Channels() int {
	return int(e.channels)
}

// SampleRate returns the sample rate in Hz.
func (e *MultistreamEncoder) SampleRate() int {
	return int(e.sampleRate)
}

// Streams returns the total number of elementary streams.
func (e *MultistreamEncoder) Streams() int {
	return e.enc.Streams()
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (e *MultistreamEncoder) CoupledStreams() int {
	return e.enc.CoupledStreams()
}

// GetFinalRange returns the final range coder state for all streams.
// The values from all streams are XOR combined to produce a single verification value.
// This matches libopus OPUS_GET_FINAL_RANGE for multistream encoders.
// Must be called after Encode() to get a meaningful value.
func (e *MultistreamEncoder) GetFinalRange() uint32 {
	return e.enc.GetFinalRange()
}

// FinalRange returns the final range coder state.
func (e *MultistreamEncoder) FinalRange() uint32 {
	return e.GetFinalRange()
}

// Lookahead returns the encoder's algorithmic delay in samples.
//
// Matches libopus OPUS_GET_LOOKAHEAD behavior:
//   - Base lookahead is Fs/400 (2.5ms)
//   - Delay compensation Fs/250 is included for VoIP/Audio
//   - Delay compensation is omitted for LowDelay
func (e *MultistreamEncoder) Lookahead() int {
	return lookaheadSamples(int(e.sampleRate), e.application)
}

// Signal returns the current signal type hint.
// Returns SignalAuto, SignalVoice, or SignalMusic.
func (e *MultistreamEncoder) Signal() Signal {
	return Signal(e.enc.Signal())
}

// SetSignal sets the signal type hint for all stream encoders.
// Use SignalVoice for speech content, SignalMusic for music content,
// or SignalAuto (default) for automatic detection.
func (e *MultistreamEncoder) SetSignal(signal Signal) error {
	if err := validateSignal(signal); err != nil {
		return err
	}
	e.enc.SetSignal(types.Signal(signal))
	return nil
}

// SetMaxBandwidth sets the maximum bandwidth limit for all stream encoders.
// The actual bandwidth will be clamped to this limit.
func (e *MultistreamEncoder) SetMaxBandwidth(bw Bandwidth) error {
	if err := validateBandwidth(bw); err != nil {
		return err
	}
	e.enc.SetMaxBandwidth(types.Bandwidth(bw))
	return nil
}

// MaxBandwidth returns the maximum bandwidth limit.
func (e *MultistreamEncoder) MaxBandwidth() Bandwidth {
	return Bandwidth(e.enc.MaxBandwidth())
}

// SetLSBDepth sets the input signal's LSB depth for all stream encoders.
// Valid range is 8-24 bits. This affects DTX sensitivity.
// Returns an error if the depth is out of range.
func (e *MultistreamEncoder) SetLSBDepth(depth int) error {
	if err := validateLSBDepth(depth); err != nil {
		return err
	}
	return e.enc.SetLSBDepth(depth)
}

// LSBDepth returns the current LSB depth setting.
func (e *MultistreamEncoder) LSBDepth() int {
	return e.enc.LSBDepth()
}
