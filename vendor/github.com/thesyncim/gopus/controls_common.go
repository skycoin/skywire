package gopus

// ExpertFrameDuration mirrors libopus OPUS_SET/GET_EXPERT_FRAME_DURATION values.
//
// It selects the duration the encoder uses for each Opus frame, overriding the
// duration implied by the per-call sample count. A requested duration takes
// effect only when it fits within the samples passed to Encode; a longer
// duration than the input provides is rejected with ErrInvalidFrameSize. The
// numeric values match the libopus OPUS_FRAMESIZE_* constants.
type ExpertFrameDuration int

const (
	// ExpertFrameDurationArg keeps the frame duration implied by the per-call
	// sample count (libopus OPUS_FRAMESIZE_ARG). This is the default.
	ExpertFrameDurationArg ExpertFrameDuration = 5000
	// ExpertFrameDuration2_5Ms forces 2.5 ms frames (CELT-capable modes only).
	ExpertFrameDuration2_5Ms ExpertFrameDuration = 5001
	// ExpertFrameDuration5Ms forces 5 ms frames (CELT-capable modes only).
	ExpertFrameDuration5Ms ExpertFrameDuration = 5002
	// ExpertFrameDuration10Ms forces 10 ms frames.
	ExpertFrameDuration10Ms ExpertFrameDuration = 5003
	// ExpertFrameDuration20Ms forces 20 ms frames.
	ExpertFrameDuration20Ms ExpertFrameDuration = 5004
	// ExpertFrameDuration40Ms forces 40 ms frames.
	ExpertFrameDuration40Ms ExpertFrameDuration = 5005
	// ExpertFrameDuration60Ms forces 60 ms frames.
	ExpertFrameDuration60Ms ExpertFrameDuration = 5006
	// ExpertFrameDuration80Ms forces 80 ms frames.
	ExpertFrameDuration80Ms ExpertFrameDuration = 5007
	// ExpertFrameDuration100Ms forces 100 ms frames.
	ExpertFrameDuration100Ms ExpertFrameDuration = 5008
	// ExpertFrameDuration120Ms forces 120 ms frames.
	ExpertFrameDuration120Ms ExpertFrameDuration = 5009
)

func validApplication(application Application) bool {
	switch application {
	case ApplicationVoIP, ApplicationAudio, ApplicationLowDelay, ApplicationRestrictedSilk, ApplicationRestrictedCelt:
		return true
	default:
		return false
	}
}

func validateBitrate(bitrate int) error {
	if bitrate == BitrateAuto || bitrate == BitrateMax {
		return nil
	}
	if bitrate <= 0 {
		return ErrInvalidBitrate
	}
	return nil
}

func validateComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	return nil
}

func validateBitrateMode(mode BitrateMode) error {
	switch mode {
	case BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR:
		return nil
	default:
		return ErrInvalidBitrateMode
	}
}

func validateEncoderMode(mode EncoderMode) error {
	switch mode {
	case EncoderModeAuto, EncoderModeSILK, EncoderModeHybrid, EncoderModeCELT:
		return nil
	default:
		return ErrInvalidArgument
	}
}

func validSignal(signal Signal) bool {
	switch signal {
	case SignalAuto, SignalVoice, SignalMusic:
		return true
	default:
		return false
	}
}

func validateSignal(signal Signal) error {
	if !validSignal(signal) {
		return ErrInvalidSignal
	}
	return nil
}

func validBandwidth(bandwidth Bandwidth) bool {
	switch bandwidth {
	case BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperwideband, BandwidthFullband:
		return true
	default:
		return false
	}
}

func validateBandwidth(bandwidth Bandwidth) error {
	if !validBandwidth(bandwidth) {
		return ErrInvalidBandwidth
	}
	return nil
}

func validateForceChannels(channels int) error {
	if channels != -1 && channels != 1 && channels != 2 {
		return ErrInvalidForceChannels
	}
	return nil
}

func validatePacketLoss(lossPercent int) error {
	if lossPercent < 0 || lossPercent > 100 {
		return ErrInvalidPacketLoss
	}
	return nil
}

func validateInBandFEC(config int) error {
	if config < InBandFECDisabled || config > InBandFECMusicSafe {
		return ErrInvalidFECConfig
	}
	return nil
}

func validateLSBDepth(depth int) error {
	if depth < 8 || depth > 24 {
		return ErrInvalidLSBDepth
	}
	return nil
}

// validFrameSize reports whether samples is a legal Opus frame size at the
// native sample rate fs. libopus frame_size_select accepts the short durations
// (fs/400)<<n for n in 0..2 (2.5/5/10 ms) and the long durations n*fs/50 for
// n in 1..6 (20/40/60/80/100/120 ms). At 48 kHz this is the legacy
// {120,240,480,960,1920,2880,3840,4800,5760} set.
func validFrameSize(samples, fs int) bool {
	if fs <= 0 {
		return false
	}
	short := fs / 400
	for n := range 3 {
		if samples == short<<n {
			return true
		}
	}
	for n := 1; n <= 6; n++ {
		if samples == n*fs/50 {
			return true
		}
	}
	return false
}

func validateFrameSize(samples, fs int, application Application) error {
	if !validFrameSize(samples, fs) {
		return ErrInvalidFrameSize
	}
	if application == ApplicationRestrictedSilk && samples < fs/100 {
		return ErrInvalidFrameSize
	}
	return nil
}

func validExpertFrameDuration(duration ExpertFrameDuration) bool {
	switch duration {
	case ExpertFrameDurationArg,
		ExpertFrameDuration2_5Ms,
		ExpertFrameDuration5Ms,
		ExpertFrameDuration10Ms,
		ExpertFrameDuration20Ms,
		ExpertFrameDuration40Ms,
		ExpertFrameDuration60Ms,
		ExpertFrameDuration80Ms,
		ExpertFrameDuration100Ms,
		ExpertFrameDuration120Ms:
		return true
	default:
		return false
	}
}

// expertFrameDurationFrameSize returns the native-Fs frame size for an expert
// frame duration. At 48 kHz this is the legacy 120..5760 set; at sub-48 kHz it
// scales by fs (e.g. 20 ms at 16 kHz = 320).
func expertFrameDurationFrameSize(duration ExpertFrameDuration, fs int) int {
	switch duration {
	case ExpertFrameDuration2_5Ms:
		return fs / 400
	case ExpertFrameDuration5Ms:
		return fs / 200
	case ExpertFrameDuration10Ms:
		return fs / 100
	case ExpertFrameDuration20Ms:
		return fs / 50
	case ExpertFrameDuration40Ms:
		return 2 * fs / 50
	case ExpertFrameDuration60Ms:
		return 3 * fs / 50
	case ExpertFrameDuration80Ms:
		return 4 * fs / 50
	case ExpertFrameDuration100Ms:
		return 5 * fs / 50
	case ExpertFrameDuration120Ms:
		return 6 * fs / 50
	default:
		return 0
	}
}

func setExpertFrameDuration(duration ExpertFrameDuration, current *ExpertFrameDuration) error {
	if !validExpertFrameDuration(duration) {
		return ErrInvalidArgument
	}
	*current = duration
	return nil
}

func setMultistreamExpertFrameDuration(duration ExpertFrameDuration, current *ExpertFrameDuration) error {
	*current = duration
	return nil
}

func selectExpertFrameSize(inputFrameSize int, duration ExpertFrameDuration, application Application, fs int) (int, error) {
	if inputFrameSize < fs/400 {
		return 0, ErrInvalidFrameSize
	}
	selected := inputFrameSize
	if duration != ExpertFrameDurationArg {
		if !validExpertFrameDuration(duration) {
			return 0, ErrInvalidFrameSize
		}
		selected = expertFrameDurationFrameSize(duration, fs)
	}
	if selected > inputFrameSize || !validFrameSize(selected, fs) {
		return 0, ErrInvalidFrameSize
	}
	if application == ApplicationRestrictedSilk && selected < fs/100 {
		return 0, ErrInvalidFrameSize
	}
	return selected, nil
}

func lookaheadSamples(sampleRate int, application Application) int {
	base := sampleRate / 400
	if application == ApplicationLowDelay || application == ApplicationRestrictedCelt {
		return base
	}
	return base + sampleRate/250
}
