package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/encoder"
)

// VAD analyzes mono PCM at 8, 12, or 16 kHz with Opus's SILK voice detector.
// It keeps an adaptive noise estimate across frames and is not safe for
// concurrent use.
type VAD struct {
	sampleRate int
	state      *encoder.VADState
	scratch    []float32
}

// NewVAD creates a detector for 8, 12, or 16 kHz mono PCM. It returns an error
// for other sample rates.
func NewVAD(sampleRate int) (*VAD, error) {
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 {
		return nil, errors.New("gopus: VAD sample rate must be 8000, 12000, or 16000 Hz")
	}
	return &VAD{
		sampleRate: sampleRate,
		state:      encoder.NewVADState(),
		scratch:    make([]float32, sampleRate/50),
	}, nil
}

// AnalyzeInt16 analyzes one mono frame of signed 16-bit PCM and returns SILK
// speech activity in Q8 (0-255), not a speech/no-speech boolean. The caller
// chooses the activity threshold and handles turn timing. PCM must use the rate
// passed to NewVAD and contain exactly 10 or 20 ms of samples; each successful
// call advances the detector state.
func (v *VAD) AnalyzeInt16(pcm []int16) (int, error) {
	if v == nil || v.state == nil || (len(pcm) != v.sampleRate/100 && len(pcm) != v.sampleRate/50) {
		return 0, ErrInvalidFrameSize
	}
	samples := v.scratch[:len(pcm)]
	for i, sample := range pcm {
		samples[i] = float32(sample) * (1.0 / 32768.0)
	}
	activity, _ := v.state.GetSpeechActivity(samples, len(samples), v.sampleRate/1000)
	return activity, nil
}

// Reset clears the adaptive noise estimate for a new audio stream.
func (v *VAD) Reset() {
	if v != nil && v.state != nil {
		v.state.Reset()
	}
}
