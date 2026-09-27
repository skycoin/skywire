package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/encoder"
)

// VAD analyzes 10 or 20 ms mono PCM frames with Opus's SILK voice detector.
// It keeps a noise estimate across frames and is not safe for concurrent use.
type VAD struct {
	sampleRate int
	state      *encoder.VADState
	scratch    []float32
}

// NewVAD creates a detector for a SILK sample rate.
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

// AnalyzeInt16 returns SILK speech activity in Q8 (0-255). The caller chooses
// the activity threshold and handles turn timing. PCM must be mono at the
// sample rate passed to NewVAD, with exactly 10 or 20 ms per frame.
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
