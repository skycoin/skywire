//go:build gopus_celt_trace && !gopus_fixed_point

package celt

import (
	"sync"
	"sync/atomic"
)

const (
	encodePitchTraceMaxCalls           = 8
	pitchDownsampleTraceCaptureEnabled = true
)

type pitchDownsampleTraceScope struct {
	input     *celtSig
	output    *float32
	inputLen  int
	outputLen int
	length    int
	channels  int
	factor    int
	capture   *pitchDownsampleIntermediateCapture
	invalid   bool
}

var (
	pitchDownsampleTraceScopeMu sync.Mutex
	pitchDownsampleTraceSinkMu  sync.Mutex
	pitchDownsampleTraceActive  atomic.Bool
	activePitchDownsampleTrace  *pitchDownsampleTraceScope
)

// EnablePitchAnalysisTraceForTesting enables the bounded pitch-analysis
// snapshots used by the late-frame CBR differential diagnostic.
func (e *Encoder) EnablePitchAnalysisTraceForTesting() {
	if e.encodeStageTrace.enabled {
		e.encodeStageTrace.pitchEnabled = true
	}
}

// EnableRemoveDoublingOperandTraceForTesting enables the bounded arithmetic
// snapshots for the next remove_doubling call after pitch tracing is enabled.
func (e *Encoder) EnableRemoveDoublingOperandTraceForTesting() {
	if e.encodeStageTrace.enabled && e.encodeStageTrace.pitchEnabled && removeDoublingMathTraceCaptureEnabled {
		e.encodeStageTrace.removeDoublingMathEnabled = true
	}
}

func (e *Encoder) runPrefilterPitchDownsample(input []celtSig, output []float32, length, channels, perChannelLength, factor int) {
	if !e.encodeStageTrace.enabled || !e.encodeStageTrace.pitchEnabled {
		pitchDownsampleSig(input, output, length, channels, factor)
		return
	}
	var capture pitchDownsampleIntermediateCapture
	scope := beginPitchDownsampleTrace(input, output, length, channels, factor, &capture)
	complete := false
	func() {
		defer func() { complete = finishPitchDownsampleTrace(scope) }()
		pitchDownsampleSig(input, output, length, channels, factor)
	}()
	if !complete {
		e.encodeStageTrace.trace.StageOverflow = true
	}
	e.encodeStageTrace.recordPitchDownsample(input, output, length, channels, perChannelLength, factor, capture)
}

func beginPitchDownsampleTrace(input []celtSig, output []float32, length, channels, factor int,
	capture *pitchDownsampleIntermediateCapture) *pitchDownsampleTraceScope {
	pitchDownsampleTraceScopeMu.Lock()
	if len(input) == 0 || len(output) == 0 || length <= 0 || channels <= 0 || channels > 2 || factor <= 0 || capture == nil {
		pitchDownsampleTraceScopeMu.Unlock()
		return nil
	}
	scope := &pitchDownsampleTraceScope{
		input: &input[0], output: &output[0], inputLen: len(input), outputLen: len(output),
		length: length, channels: channels, factor: factor, capture: capture,
	}
	pitchDownsampleTraceSinkMu.Lock()
	if activePitchDownsampleTrace != nil {
		pitchDownsampleTraceSinkMu.Unlock()
		pitchDownsampleTraceScopeMu.Unlock()
		return nil
	}
	activePitchDownsampleTrace = scope
	pitchDownsampleTraceActive.Store(true)
	pitchDownsampleTraceSinkMu.Unlock()
	return scope
}

func finishPitchDownsampleTrace(scope *pitchDownsampleTraceScope) bool {
	if scope == nil {
		return false
	}
	pitchDownsampleTraceSinkMu.Lock()
	complete := activePitchDownsampleTrace == scope && !scope.invalid && scope.capture != nil && scope.capture.Captured == 15 &&
		len(scope.capture.Decimated) == scope.length
	if activePitchDownsampleTrace == scope {
		pitchDownsampleTraceActive.Store(false)
		activePitchDownsampleTrace = nil
	}
	pitchDownsampleTraceSinkMu.Unlock()
	pitchDownsampleTraceScopeMu.Unlock()
	return complete
}

func recordPitchDownsampleDecimated(input []celtSig, output []float32, length, channels, factor int) {
	if !pitchDownsampleTraceActive.Load() {
		return
	}
	pitchDownsampleTraceSinkMu.Lock()
	defer pitchDownsampleTraceSinkMu.Unlock()
	scope := pitchDownsampleScopeForRecord(input, output, length, channels, factor, 1)
	if scope != nil {
		scope.capture.Decimated = append(scope.capture.Decimated[:0], output[:length]...)
		scope.capture.Captured |= 1
	}
}

func recordPitchDownsampleAutocorrelation(input []celtSig, output []float32, length, channels, factor int, ac [5]float32) {
	if !pitchDownsampleTraceActive.Load() {
		return
	}
	pitchDownsampleTraceSinkMu.Lock()
	defer pitchDownsampleTraceSinkMu.Unlock()
	scope := pitchDownsampleScopeForRecord(input, output, length, channels, factor, 2)
	if scope != nil {
		scope.capture.RawAutocorrelation = ac
		scope.capture.Captured |= 2
	}
}

func recordPitchDownsampleLPCInput(input []celtSig, output []float32, length, channels, factor int, ac [5]float32) {
	if !pitchDownsampleTraceActive.Load() {
		return
	}
	pitchDownsampleTraceSinkMu.Lock()
	defer pitchDownsampleTraceSinkMu.Unlock()
	scope := pitchDownsampleScopeForRecord(input, output, length, channels, factor, 4)
	if scope != nil {
		scope.capture.LPCInput = ac
		scope.capture.Captured |= 4
	}
}

func recordPitchDownsampleLPC(input []celtSig, output []float32, length, channels, factor int, lpc [4]float32) {
	if !pitchDownsampleTraceActive.Load() {
		return
	}
	pitchDownsampleTraceSinkMu.Lock()
	defer pitchDownsampleTraceSinkMu.Unlock()
	scope := pitchDownsampleScopeForRecord(input, output, length, channels, factor, 8)
	if scope != nil {
		scope.capture.LPC = lpc
		scope.capture.Captured |= 8
	}
}

func pitchDownsampleScopeForRecord(input []celtSig, output []float32, length, channels, factor int, bit uint32) *pitchDownsampleTraceScope {
	scope := activePitchDownsampleTrace
	if scope == nil || len(input) == 0 || len(output) == 0 || &input[0] != scope.input || &output[0] != scope.output {
		return nil
	}
	if len(input) != scope.inputLen || len(output) != scope.outputLen || length != scope.length ||
		channels != scope.channels || factor != scope.factor || scope.capture == nil || scope.capture.Captured != bit-1 {
		scope.invalid = true
		return nil
	}
	return scope
}

func (e *Encoder) recordPitchControls(frameSize, channels int, enabled bool, complexity int32, maxPeriod, minPeriod int,
	tfEstimate, toneFreq, toneishness, maxPitchRatio float32) {
	s := &e.encodeStageTrace
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.PitchControls) >= 1 || frameSize <= 0 || channels <= 0 || channels > 2 ||
		complexity < 0 || maxPeriod <= 0 || minPeriod <= 0 || frameSize > int(^uint32(0)>>1) ||
		maxPeriod > int(^uint32(0)>>1) || minPeriod > int(^uint32(0)>>1) {
		s.trace.StageOverflow = true
		return
	}
	enabledFlag := int32(0)
	if enabled {
		enabledFlag = 1
	}
	s.trace.PitchControls = append(s.trace.PitchControls, EncodePitchControlsTrace{
		FrameSize: int32(frameSize), Channels: int32(channels), Enabled: enabledFlag,
		Complexity: complexity, MaxPeriod: int32(maxPeriod), MinPeriod: int32(minPeriod),
		TFEstimate: tfEstimate, ToneFreq: toneFreq, Toneishness: toneishness, MaxPitchRatio: maxPitchRatio,
	})
}

func (e *Encoder) runPrefilterPitchSearch(buffer []float32, xOffset, length, maxPitch int) int {
	result := pitchSearch(buffer[xOffset:], buffer, length, maxPitch, &e.scratch)
	e.encodeStageTrace.recordPitchSearch(buffer, xOffset, length, maxPitch, result)
	return result
}

func (e *Encoder) runPrefilterRemoveDoubling(buffer []float32, maxPeriod, minPeriod, n int, t0 *int) float32 {
	t0Before := *t0
	prevPeriod, prevGain := e.prefilterPeriod, e.prefilterGain
	captureMath := e.encodeStageTrace.enabled && e.encodeStageTrace.pitchEnabled &&
		e.encodeStageTrace.removeDoublingMathEnabled && removeDoublingMathTraceCaptureEnabled
	started := false
	if captureMath {
		started = beginRemoveDoublingMathTrace(buffer, maxPeriod, n, t0Before)
		if !started {
			e.encodeStageTrace.trace.StageOverflow = true
		}
	}
	gain := removeDoubling(buffer, maxPeriod, minPeriod, n, t0, prevPeriod, prevGain, &e.scratch)
	var mathTrace EncodeRemoveDoublingMathTrace
	mathComplete := false
	if started {
		mathTrace, mathComplete = finishRemoveDoublingMathTrace()
		if !mathComplete {
			e.encodeStageTrace.trace.StageOverflow = true
		}
	}
	traceIndex := len(e.encodeStageTrace.trace.RemoveDoubling)
	e.encodeStageTrace.recordRemoveDoubling(buffer, maxPeriod, minPeriod, n, t0Before, *t0, prevPeriod, prevGain, gain)
	if started && mathComplete {
		if len(e.encodeStageTrace.trace.RemoveDoubling) != traceIndex+1 {
			e.encodeStageTrace.trace.StageOverflow = true
		} else {
			e.encodeStageTrace.trace.RemoveDoubling[traceIndex].Math = mathTrace
		}
	}
	return gain
}

func (s *encodeStageTraceState) recordPitchDownsample(input []celtSig, output []float32, length, channels, perChannelLength, factor int, capture pitchDownsampleIntermediateCapture) {
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.PitchDownsample) >= encodePitchTraceMaxCalls || length <= 0 || channels <= 0 || channels > 2 ||
		factor <= 0 || perChannelLength < length*factor || len(input) < channels*perChannelLength || len(output) < length ||
		capture.Captured != 15 || len(capture.Decimated) != length {
		s.trace.StageOverflow = true
		return
	}
	inputPerChannel := length * factor
	inputCopy := make([]float32, inputPerChannel*channels)
	for channel := range channels {
		copySigToFloat32(inputCopy[channel*inputPerChannel:(channel+1)*inputPerChannel],
			input[channel*perChannelLength:channel*perChannelLength+inputPerChannel])
	}
	s.trace.PitchDownsample = append(s.trace.PitchDownsample, EncodePitchDownsampleTrace{
		Length: int32(length), Channels: int32(channels), Factor: int32(factor),
		Input: inputCopy, Decimated: copyStageFloat32(capture.Decimated),
		RawAutocorrelation: capture.RawAutocorrelation, LPCInput: capture.LPCInput,
		LPC: capture.LPC, Output: copyStageFloat32(output[:length]),
	})
}

func (s *encodeStageTraceState) recordPitchSearch(buffer []float32, xOffset, length, maxPitch, result int) {
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.PitchSearch) >= encodePitchTraceMaxCalls || len(buffer) == 0 || xOffset < 0 || xOffset >= len(buffer) ||
		length <= 0 || maxPitch <= 0 || result < -1 || result > int(^uint32(0)>>1) {
		s.trace.StageOverflow = true
		return
	}
	s.trace.PitchSearch = append(s.trace.PitchSearch, EncodePitchSearchTrace{
		Length: int32(length), MaxPitch: int32(maxPitch), XOffset: int32(xOffset),
		Buffer: copyStageFloat32(buffer), Result: int32(result),
	})
}

func (s *encodeStageTraceState) recordRemoveDoubling(buffer []float32, maxPeriod, minPeriod, n int,
	t0Before, t0After, prevPeriod int, prevGain, gain float32) {
	if !s.enabled || !s.pitchEnabled {
		return
	}
	if len(s.trace.RemoveDoubling) >= encodePitchTraceMaxCalls || len(buffer) == 0 || maxPeriod <= 0 || minPeriod <= 0 || n <= 0 ||
		maxPeriod > int(^uint32(0)>>1) || minPeriod > int(^uint32(0)>>1) || n > int(^uint32(0)>>1) ||
		t0Before < -1 || t0Before > int(^uint32(0)>>1) || t0After < -1 || t0After > int(^uint32(0)>>1) ||
		prevPeriod < 0 || prevPeriod > int(^uint32(0)>>1) {
		s.trace.StageOverflow = true
		return
	}
	s.trace.RemoveDoubling = append(s.trace.RemoveDoubling, EncodeRemoveDoublingTrace{
		MaxPeriod: int32(maxPeriod), MinPeriod: int32(minPeriod), N: int32(n),
		T0Before: int32(t0Before), T0After: int32(t0After), PrevPeriod: int32(prevPeriod),
		PrevGain: prevGain, Gain: gain, Buffer: copyStageFloat32(buffer),
	})
}
