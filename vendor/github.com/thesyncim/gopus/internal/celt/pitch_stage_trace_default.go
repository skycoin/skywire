//go:build !gopus_celt_trace || gopus_fixed_point

package celt

const pitchDownsampleTraceCaptureEnabled = false

func recordPitchDownsampleDecimated([]celtSig, []float32, int, int, int) {}

func recordPitchDownsampleAutocorrelation([]celtSig, []float32, int, int, int, [5]float32) {}

func recordPitchDownsampleLPCInput([]celtSig, []float32, int, int, int, [5]float32) {}

func recordPitchDownsampleLPC([]celtSig, []float32, int, int, int, [4]float32) {}

func (e *Encoder) recordPitchControls(frameSize, channels int, enabled bool, complexity int32, maxPeriod, minPeriod int,
	tfEstimate, toneFreq, toneishness, maxPitchRatio float32) {
}

func (e *Encoder) runPrefilterPitchDownsample(input []celtSig, output []float32, length, channels, perChannelLength, factor int) {
	pitchDownsampleSig(input, output, length, channels, factor)
}

func (e *Encoder) runPrefilterPitchSearch(buffer []float32, xOffset, length, maxPitch int) int {
	return pitchSearch(buffer[xOffset:], buffer, length, maxPitch, &e.scratch)
}

func (e *Encoder) runPrefilterRemoveDoubling(buffer []float32, maxPeriod, minPeriod, n int, t0 *int) float32 {
	return removeDoubling(buffer, maxPeriod, minPeriod, n, t0, e.prefilterPeriod, e.prefilterGain, &e.scratch)
}
