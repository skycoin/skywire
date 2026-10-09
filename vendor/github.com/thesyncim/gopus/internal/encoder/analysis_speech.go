package encoder

// SpeechProbability feeds one mono frame to the analyzer and returns the
// activity_probability (libopus AnalysisInfo) of the newest completed analysis
// window, the value RunAnalysis reports as VADProb. It is 0 until the first
// window completes and while the newest window is digital silence, for which
// libopus repeats the previous entry instead.
//
// With no encoder lookahead the newest window ends 240 samples at 24 kHz (10 ms)
// before the last input sample. Frames shorter than 20 ms complete a window on
// every second call and repeat the previous result in between.
func (s *TonalityAnalysisState) SpeechProbability(pcm []float32) float32 {
	info := s.RunAnalysis(pcm, len(pcm), 1)
	if !info.Valid || s.silentWindow {
		return 0
	}
	return info.VADProb
}
