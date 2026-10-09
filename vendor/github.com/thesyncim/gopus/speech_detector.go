package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/encoder"
)

// SpeechDetector scores how likely mono PCM is speech with the neural analysis
// that Opus runs inside its encoder (libopus activity_probability). Unlike VAD,
// which follows level against a noise estimate, the network scores spectral and
// temporal structure over several windows, so steady noise, hum, and isolated
// clicks score low even when loud. It does not separate speech from every other
// foreground sound: a cough or other people talking can score high. It keeps a
// recurrent network state and is not safe for concurrent use.
type SpeechDetector struct {
	sampleRate int
	state      *encoder.TonalityAnalysisState
	scratch    []float32
}

// NewSpeechDetector creates a detector for 16, 24, or 48 kHz mono PCM, the rates
// of the Opus analysis. It returns an error for other sample rates.
func NewSpeechDetector(sampleRate int) (*SpeechDetector, error) {
	if sampleRate != 16000 && sampleRate != 24000 && sampleRate != 48000 {
		return nil, errors.New("gopus: SpeechDetector sample rate must be 16000, 24000, or 48000 Hz")
	}
	d := &SpeechDetector{
		sampleRate: sampleRate,
		state:      encoder.NewTonalityAnalysisState(sampleRate),
		scratch:    make([]float32, sampleRate/50),
	}
	// One nonzero window sizes the analyzer's scratch, so analysis never allocates.
	for i := range d.scratch {
		d.scratch[i] = float32(1+i&1) * (1.0 / 32768.0)
	}
	d.state.SpeechProbability(d.scratch)
	d.Reset()
	return d, nil
}

// AnalyzeInt16 analyzes one mono frame of signed 16-bit PCM and returns the
// speech probability in [0, 1], not a speech/no-speech boolean. The caller
// chooses the threshold and handles turn timing. PCM must use the rate passed to
// NewSpeechDetector and contain exactly 10 or 20 ms of samples; each successful
// call advances the detector state.
//
// The network works on 20 ms windows and completes one for every 20 ms of input.
// The newest window ends 10 ms before the newest sample, and the network's
// recurrent output smooths over earlier windows. A 10 ms frame completes a window
// on every second call and repeats the previous result in between. The result is
// 0 until the first window completes and while the newest window is exactly zero,
// where the encoder repeats its previous estimate. The first ten windows (about
// 200 ms) after construction or Reset are a warm-up in which the estimate is less
// reliable.
func (d *SpeechDetector) AnalyzeInt16(pcm []int16) (float32, error) {
	if d == nil || d.state == nil || (len(pcm) != d.sampleRate/100 && len(pcm) != d.sampleRate/50) {
		return 0, ErrInvalidFrameSize
	}
	samples := d.scratch[:len(pcm)]
	for i, sample := range pcm {
		samples[i] = float32(sample) * (1.0 / 32768.0)
	}
	return d.state.SpeechProbability(samples), nil
}

// AnalyzeFloat32 is AnalyzeInt16 for float32 PCM where 1.0 is full scale, the
// scale [Encoder.Encode] uses. Samples beyond twice full scale are clamped.
func (d *SpeechDetector) AnalyzeFloat32(pcm []float32) (float32, error) {
	if d == nil || d.state == nil || (len(pcm) != d.sampleRate/100 && len(pcm) != d.sampleRate/50) {
		return 0, ErrInvalidFrameSize
	}
	return d.state.SpeechProbability(pcm), nil
}

// Reset clears the analysis and network state for a new audio stream.
func (d *SpeechDetector) Reset() {
	if d != nil && d.state != nil {
		d.state.Reset()
	}
}
