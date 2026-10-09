//go:build gopus_celt_trace && !gopus_fixed_point

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// A stereo LM=3 frame has two long analysis transforms and sixteen short
// transforms; see celt_encoder.c compute_mdcts and secondMdct.
const encodeMDCTTraceMaxCalls = 18

const (
	encodeCoderRangeBeforeCoarse int32 = iota + 1
	encodeCoderRangeAfterCoarse
	encodeCoderRangeBeforeQuant
)

// EncodeStageTrace holds bounded CELT frame intermediates for the opt-in
// first-divergence oracle test. Values use the codec's float32 storage width.
type EncodeStageTrace struct {
	BandStages      []EncodeBandStageTrace
	Normalizations  []EncodeNormalizationTrace
	CoarseEnergy    []EncodeCoarseEnergyTrace
	CoderRanges     []EncodeCoderRangeTrace
	BandQuantize    []EncodeBandQuantizeTrace
	Preemphasis     []EncodePreemphasisTrace
	PrefilterComb   []EncodePrefilterCombTrace
	PrefilterNoop   []EncodePrefilterNoopTrace
	PitchControls   []EncodePitchControlsTrace
	PitchDownsample []EncodePitchDownsampleTrace
	PitchSearch     []EncodePitchSearchTrace
	RemoveDoubling  []EncodeRemoveDoublingTrace
	MDCTCalls       []EncodeMDCTCallTrace
	MDCTOverflow    bool
	StageOverflow   bool
}

// EncodePitchControlsTrace captures the inputs that select runPrefilter's
// tone-frequency shortcut or pitch-analysis path.
type EncodePitchControlsTrace struct {
	FrameSize     int32
	Channels      int32
	Enabled       int32
	Complexity    int32
	MaxPeriod     int32
	MinPeriod     int32
	TFEstimate    float32
	ToneFreq      float32
	Toneishness   float32
	MaxPitchRatio float32
}

// EncodePitchDownsampleTrace captures the actual pitch_downsample input and
// output buffers at runPrefilter's pitch analysis boundary.
type EncodePitchDownsampleTrace struct {
	Length             int32
	Channels           int32
	Factor             int32
	Input              []float32
	Decimated          []float32
	RawAutocorrelation [5]float32
	LPCInput           [5]float32
	LPC                [4]float32
	Output             []float32
}

// EncodePitchSearchTrace captures the pitch buffer and result at the actual
// pitch_search call in runPrefilter.
type EncodePitchSearchTrace struct {
	Length   int32
	MaxPitch int32
	XOffset  int32
	Buffer   []float32
	Result   int32
}

// EncodeRemoveDoublingTrace captures the actual remove_doubling operands,
// prior state, and result in runPrefilter.
type EncodeRemoveDoublingTrace struct {
	MaxPeriod  int32
	MinPeriod  int32
	N          int32
	T0Before   int32
	T0After    int32
	PrevPeriod int32
	PrevGain   float32
	Gain       float32
	Buffer     []float32
	Math       EncodeRemoveDoublingMathTrace
}

type EncodeRemoveDoublingYYTrace struct {
	Index     int32
	XBefore   float32
	XAfter    float32
	UpdatedYY float32
	LookupYY  float32
}

type EncodeRemoveDoublingGainTrace struct {
	XY          float32
	XX          float32
	YY          float32
	Denominator float32
	Sqrt        float32
	Gain        float32
}

// EncodeRemoveDoublingMathTrace records the live correlation, running-energy,
// and pitch-gain boundaries for one explicitly selected diagnostic call.
type EncodeRemoveDoublingMathTrace struct {
	DualCount  int32
	DualFirst  [16]float32
	DualSecond [16]float32
	YYCount    int32
	YY         [512]EncodeRemoveDoublingYYTrace
	GainCount  int32
	Gains      [16]EncodeRemoveDoublingGainTrace
	Overflow   bool
}

// EncodePreemphasisTrace captures one channel's exact raw input, carry, and
// output at celt_preemphasis' boundary.
type EncodePreemphasisTrace struct {
	Channel      int
	Channels     int
	FrameSize    int
	Upsample     int
	Clip         bool
	Coefficients [4]float32
	Input        []float32
	Output       []float32
	StateBefore  float32
	StateAfter   float32
}

// EncodePrefilterCombTrace captures the actual materialized history and frame
// samples passed to one run_prefilter comb-filter call.
type EncodePrefilterCombTrace struct {
	Channel   int
	Start     int
	T0        int32
	T1        int32
	N         int
	Gain0     float32
	Gain1     float32
	Tapset0   int32
	Tapset1   int32
	Overlap   int
	WindowNil bool
	History   []float32
	Input     []float32
	Window    []float32
	Output    []float32
}

// EncodePrefilterNoopTrace captures the actual run_prefilter boundary when
// both comb gains are zero and the Go path copies history without calling the
// comb kernel. C may execute identity comb calls for the same frame; the
// oracle checks each such call against this source frame and history.
type EncodePrefilterNoopTrace struct {
	Channel   int
	FrameSize int
	Overlap   int
	Offset    int
	T0        int32
	T1        int32
	Gain0     float32
	Gain1     float32
	Tapset0   int32
	Tapset1   int32
	History   []float32
	Input     []float32
	Frame     []float32
	Window    []float32
}

// EncodeMDCTCallTrace captures the exact pre-transform inputs and tables for
// one clt_mdct_forward call. Calls are ordered by channel, then block.
type EncodeMDCTCallTrace struct {
	Channel    int
	Block      int
	LookupN    int
	MaxShift   int
	TransformN int
	Shift      int
	Stride     int
	Overlap    int
	FFTSize    int
	FFTScale   float32
	Input      []float32
	Window     []float32
	Trig       []float32
}

type EncodeBandStageTrace struct {
	FrameCoeffs   int
	Bands         int
	Channels      int
	LM            int
	HasAmplitudes bool
	Spectrum      []float32
	Amplitudes    []float32
	LogEnergy     []float32
}

type EncodeNormalizationTrace struct {
	ActiveCoeffs int
	Bands        int
	Channels     int
	BandEnergy   []float32
	Normalized   []float32
}

type EncodeCoarseEnergyTrace struct {
	Bands       int
	Channels    int
	BudgetBytes int
	Input       []float32
	Quantized   []float32
	Error       []float32
}

// EncodeCoderRangeTrace records the live range coder at one selected boundary.
// Coder lets tests prove that the snapshots refer to the same encoder instance.
type EncodeCoderRangeTrace struct {
	Stage    int32
	TellFrac int32
	Range    uint32
	Coder    *rangecoding.Encoder
}

type EncodeBandQuantizeTrace struct {
	ActiveCoeffs int
	Bands        int
	Channels     int
	BandEnergy   []float32
	Input        []float32
	Output       []float32
}

type encodeStageTraceState struct {
	enabled                   bool
	pitchEnabled              bool
	removeDoublingMathEnabled bool
	coderRangeClosed          bool
	trace                     EncodeStageTrace
}

// EnableEncodeStageTraceForTesting resets and enables frame-stage captures.
// It is available only with the gopus_celt_trace build tag.
func (e *Encoder) EnableEncodeStageTraceForTesting() {
	e.encodeStageTrace.reset()
}

// DisableEncodeStageTraceForTesting stops collecting after a selected frame.
func (e *Encoder) DisableEncodeStageTraceForTesting() {
	e.encodeStageTrace.enabled = false
}

func (e *Encoder) beginEncodePreemphasisTrace(pcm []float32, frameSize, overlap int, nativeInput bool) int {
	coefficients := [4]float32{float32(PreemphCoef), 0, 1, 1}
	if e.hd96kPreemph[1] != 0 {
		coefficients = e.hd96kPreemph
	}
	return e.encodeStageTrace.beginPreemphasis(pcm, frameSize, overlap, int(e.channels), e.effectiveUpsample(), nativeInput, e.preemphState, coefficients)
}

func (e *Encoder) finishEncodePreemphasisTrace(call int, in []float32, frameSize, overlap int) {
	e.encodeStageTrace.finishPreemphasis(call, in, frameSize, overlap, e.preemphState)
}

func (e *Encoder) beginEncodePrefilterCombTrace(channel int, dst, src []celtSig, start, t0, t1, n int, gain0, gain1 float32, tapset0, tapset1 int, window []float32, overlap int) int {
	return e.encodeStageTrace.beginPrefilterComb(channel, dst, src, start, t0, t1, n, gain0, gain1, tapset0, tapset1, window, overlap)
}

func (e *Encoder) recordEncodePrefilterNoopTrace(pre []celtSig, in []float32,
	frameSize, channels, overlap, perChanLen, t0, t1 int, gain0, gain1 float32, tapset0, tapset1 int) {
	if !e.encodeStageTrace.enabled {
		return
	}
	maxPeriod := e.combMaxPeriod()
	stride := frameSize + overlap
	if frameSize <= 0 || channels <= 0 || channels > 2 || overlap < 0 || overlap > frameSize ||
		len(in) < channels*stride || maxPeriod <= 0 || len(e.prefilterMem) < channels*maxPeriod ||
		(pre != nil && (perChanLen < maxPeriod+frameSize || len(pre) < channels*perChanLen)) {
		e.encodeStageTrace.trace.StageOverflow = true
		return
	}
	mode := e.modeConfig(frameSize)
	if mode.ShortBlocks <= 0 {
		e.encodeStageTrace.trace.StageOverflow = true
		return
	}
	offset := max(frameSize/mode.ShortBlocks-overlap, 0)
	if offset > frameSize || offset > maxPeriod || offset+overlap > frameSize {
		e.encodeStageTrace.trace.StageOverflow = true
		return
	}
	window := e.scratch.modeWindow(overlap)
	if len(window) < overlap {
		e.encodeStageTrace.trace.StageOverflow = true
		return
	}
	for channel := range channels {
		frame := in[channel*stride+overlap : channel*stride+overlap+frameSize]
		history := e.prefilterMem[channel*maxPeriod : (channel+1)*maxPeriod]
		var source []celtSig
		if pre != nil {
			preCh := pre[channel*perChanLen : (channel+1)*perChanLen]
			history = preCh[:maxPeriod]
			source = preCh[maxPeriod : maxPeriod+frameSize]
		}
		input := copyStageFloat32(source)
		if source == nil {
			input = copyStageFloat32(frame)
		}
		windowCopy := window
		if len(windowCopy) > overlap {
			windowCopy = windowCopy[:overlap]
		}
		e.encodeStageTrace.recordPrefilterNoop(EncodePrefilterNoopTrace{
			Channel: channel, FrameSize: frameSize, Overlap: overlap, Offset: offset,
			T0: int32(t0), T1: int32(t1), Gain0: gain0, Gain1: gain1,
			Tapset0: int32(tapset0), Tapset1: int32(tapset1),
			History: copyStageFloat32(history), Input: input, Frame: copyStageFloat32(frame), Window: copyStageFloat32(windowCopy),
		})
	}
}

func (e *Encoder) finishEncodePrefilterCombTrace(call int, dst []celtSig, start, n int) {
	e.encodeStageTrace.finishPrefilterComb(call, dst, start, n)
}

// EncodeStageTraceForTesting returns the captured CELT frame-stage values.
// Slices remain valid until the next encode or trace reset.
func (e *Encoder) EncodeStageTraceForTesting() EncodeStageTrace {
	return e.encodeStageTrace.trace
}

func (s *encodeStageTraceState) reset() {
	s.enabled = true
	s.pitchEnabled = false
	s.removeDoublingMathEnabled = false
	s.coderRangeClosed = false
	s.trace = EncodeStageTrace{
		BandStages:      make([]EncodeBandStageTrace, 0, 4),
		Normalizations:  make([]EncodeNormalizationTrace, 0, 2),
		CoarseEnergy:    make([]EncodeCoarseEnergyTrace, 0, 2),
		CoderRanges:     make([]EncodeCoderRangeTrace, 0, 3),
		BandQuantize:    make([]EncodeBandQuantizeTrace, 0, 2),
		Preemphasis:     make([]EncodePreemphasisTrace, 0, 2),
		PrefilterComb:   make([]EncodePrefilterCombTrace, 0, 4),
		PrefilterNoop:   make([]EncodePrefilterNoopTrace, 0, 2),
		PitchControls:   make([]EncodePitchControlsTrace, 0, 1),
		PitchDownsample: make([]EncodePitchDownsampleTrace, 0, 2),
		PitchSearch:     make([]EncodePitchSearchTrace, 0, 2),
		RemoveDoubling:  make([]EncodeRemoveDoublingTrace, 0, 2),
		MDCTCalls:       make([]EncodeMDCTCallTrace, 0, 4),
	}
}

func (s *encodeStageTraceState) beginPreemphasis(pcm []float32, frameSize, overlap, channels, upsample int, nativeInput bool, state []celtSig, coefficients [4]float32) int {
	if !s.enabled {
		return -1
	}
	if channels <= 0 || channels > 2 || frameSize < 0 || overlap < 0 || upsample <= 0 || len(pcm)%channels != 0 || len(state) < channels {
		s.trace.StageOverflow = true
		return -1
	}
	inputPerChannel := frameSize
	if nativeInput {
		inputPerChannel = frameSize / upsample
	}
	if inputPerChannel < 0 || len(pcm)/channels != inputPerChannel {
		s.trace.StageOverflow = true
		return -1
	}
	first := len(s.trace.Preemphasis)
	for channel := range channels {
		if len(s.trace.Preemphasis) >= 2 {
			s.trace.StageOverflow = true
			return -1
		}
		input := make([]float32, inputPerChannel)
		for i := range input {
			input[i] = pcm[i*channels+channel]
		}
		s.trace.Preemphasis = append(s.trace.Preemphasis, EncodePreemphasisTrace{
			Channel: channel, Channels: channels, FrameSize: frameSize, Upsample: upsample,
			Coefficients: coefficients, Input: input, StateBefore: float32(state[channel]),
		})
	}
	return first
}

func (s *encodeStageTraceState) finishPreemphasis(first int, in []float32, frameSize, overlap int, state []celtSig) {
	if !s.enabled || first < 0 {
		return
	}
	channels := min(len(state), len(s.trace.Preemphasis)-first)
	stride := frameSize + overlap
	if channels <= 0 || stride < 0 || frameSize < 0 || overlap < 0 || len(in) < channels*stride {
		s.trace.StageOverflow = true
		return
	}
	for channel := range channels {
		trace := &s.trace.Preemphasis[first+channel]
		if trace.FrameSize > len(in[channel*stride:])-overlap {
			s.trace.StageOverflow = true
			return
		}
		trace.Output = copyStageFloat32(in[channel*stride+overlap : channel*stride+overlap+trace.FrameSize])
		trace.StateAfter = float32(state[channel])
	}
}

func (s *encodeStageTraceState) beginPrefilterComb(channel int, dst, src []celtSig, start, t0, t1, n int, gain0, gain1 float32, tapset0, tapset1 int, window []float32, overlap int) int {
	if !s.enabled {
		return -1
	}
	call := len(s.trace.PrefilterComb)
	if call >= 8 || start < combFilterMaxPeriod || n < 0 || start+n > len(src) || start+n > len(dst) {
		s.trace.StageOverflow = true
		return -1
	}
	historyStart := start - combFilterMaxPeriod
	trace := EncodePrefilterCombTrace{
		Channel: channel, Start: start, T0: int32(t0), T1: int32(t1), N: n, Gain0: gain0, Gain1: gain1,
		Tapset0: int32(tapset0), Tapset1: int32(tapset1), Overlap: overlap, WindowNil: window == nil,
		History: copyStageFloat32(src[historyStart:start]), Input: copyStageFloat32(src[start : start+n]),
	}
	if window != nil {
		if overlap < 0 || overlap > len(window) {
			s.trace.StageOverflow = true
			return -1
		}
		trace.Window = copyStageFloat32(window[:overlap])
	}
	s.trace.PrefilterComb = append(s.trace.PrefilterComb, trace)
	return len(s.trace.PrefilterComb) - 1
}

func (s *encodeStageTraceState) finishPrefilterComb(call int, dst []celtSig, start, n int) {
	if !s.enabled || call < 0 {
		return
	}
	if call >= len(s.trace.PrefilterComb) || start < 0 || n < 0 || start+n > len(dst) {
		s.trace.StageOverflow = true
		return
	}
	s.trace.PrefilterComb[call].Output = copyStageFloat32(dst[start : start+n])
}

func (s *encodeStageTraceState) recordPrefilterNoop(trace EncodePrefilterNoopTrace) {
	if !s.enabled {
		return
	}
	if len(s.trace.PrefilterNoop) >= 2 || trace.Channel < 0 || trace.Channel >= 2 ||
		trace.FrameSize <= 0 || trace.Overlap < 0 || trace.Offset < 0 ||
		trace.FrameSize != len(trace.Input) || trace.FrameSize != len(trace.Frame) || len(trace.History) == 0 || len(trace.Window) != trace.Overlap {
		s.trace.StageOverflow = true
		return
	}
	s.trace.PrefilterNoop = append(s.trace.PrefilterNoop, trace)
}

func (s *encodeStageTraceState) recordMDCTCall(call EncodeMDCTCallTrace) {
	if !s.enabled {
		return
	}
	if len(s.trace.MDCTCalls) >= encodeMDCTTraceMaxCalls {
		s.trace.MDCTOverflow = true
		return
	}
	call.Input = copyStageFloat32(call.Input)
	call.Window = copyStageFloat32(call.Window)
	call.Trig = copyStageFloat32(call.Trig)
	s.trace.MDCTCalls = append(s.trace.MDCTCalls, call)
}

func (e *Encoder) recordEncodeMDCTTrace(in []float32, frameSize, overlap, shortBlocks int) {
	if !e.encodeStageTrace.enabled {
		return
	}
	blockCount := max(shortBlocks, 1)
	if frameSize <= 0 || frameSize%blockCount != 0 {
		e.encodeStageTrace.trace.MDCTOverflow = true
		return
	}
	blockSize := frameSize / blockCount
	n2 := blockSize
	n4 := n2 / 2
	transformN := 2 * n2
	mode := e.modeConfig(frameSize)
	maxShift := e.modeMaxLM(mode.LM)
	shift := maxShift - mode.LM
	if blockCount > 1 {
		shift = maxShift
	}
	stride := frameSize + overlap
	if stride <= 0 || len(in) < stride*int(e.channels) {
		e.encodeStageTrace.trace.MDCTOverflow = true
		return
	}
	fftSize := n4
	// Use the same lookup/table selection as mdctForwardOverlapF32Scratch.
	lookup := e.scratch.mdctLookup(transformN)
	var trig, window []float32
	var fft *kissFFTState
	if lookup != nil {
		trig, window, fft = lookup.trig, lookup.window, lookup.fft
	} else {
		trig = getMDCTTrigF32(transformN)
		window = GetWindowBufferF32(overlap)
		fft = getKissFFTState(fftSize)
	}
	if fft == nil {
		e.encodeStageTrace.trace.MDCTOverflow = true
		return
	}
	for channel := range int(e.channels) {
		channelInput := in[channel*stride : (channel+1)*stride]
		for block := range blockCount {
			start := block * blockSize
			end := start + blockSize + overlap
			if end > len(channelInput) {
				e.encodeStageTrace.trace.MDCTOverflow = true
				return
			}
			e.encodeStageTrace.recordMDCTCall(EncodeMDCTCallTrace{
				Channel: channel, Block: block, LookupN: transformN << shift,
				MaxShift: maxShift, TransformN: transformN, Shift: shift,
				Stride: blockCount, Overlap: overlap, FFTSize: fftSize,
				// mdctForwardOverlapF32Scratch scales by float32(1)/float32(n4).
				FFTScale: float32(1) / float32(n4),
				Input:    channelInput[start:end], Window: window, Trig: trig,
			})
		}
	}
}

func (s *encodeStageTraceState) recordBandStage(spectrum []float32, amplitudes []CeltEner, logEnergy []CeltGLog, frameCoeffs, channels, bands, lm int) {
	if !s.enabled {
		return
	}
	s.trace.BandStages = append(s.trace.BandStages, EncodeBandStageTrace{
		FrameCoeffs:   frameCoeffs,
		Bands:         bands,
		Channels:      channels,
		LM:            lm,
		HasAmplitudes: amplitudes != nil,
		Spectrum:      copyStageFloat32(spectrum),
		Amplitudes:    copyStageFloat32(amplitudes),
		LogEnergy:     copyStageFloat32(logEnergy),
	})
}

func (s *encodeStageTraceState) recordNormalization(normL, normR []CeltNorm, bandEnergy []CeltEner, activeCoeffs, bands, channels int) {
	if !s.enabled {
		return
	}
	normalized := make([]float32, activeCoeffs*channels)
	copyStagePrefix(normalized, normL, activeCoeffs)
	if channels > 1 {
		copyStagePrefix(normalized[activeCoeffs:], normR, activeCoeffs)
	}
	s.trace.Normalizations = append(s.trace.Normalizations, EncodeNormalizationTrace{
		ActiveCoeffs: activeCoeffs,
		Bands:        bands,
		Channels:     channels,
		BandEnergy:   copyStageFloat32(bandEnergy),
		Normalized:   normalized,
	})
}

func (e *Encoder) recordEncodeNormalizationTrace(normL, normR []CeltNorm, bandEnergy []CeltEner, bands, lm, channels int) {
	activeCoeffs := e.modeEdges()[bands] * (1 << lm)
	e.encodeStageTrace.recordNormalization(normL, normR, bandEnergy, activeCoeffs, bands, channels)
}

func (s *encodeStageTraceState) recordCoarseInput(input []CeltGLog, bands, channels int, budgetBytes int32, re *rangecoding.Encoder) {
	if !s.enabled {
		return
	}
	s.recordCoderRange(encodeCoderRangeBeforeCoarse, re)
	s.trace.CoarseEnergy = append(s.trace.CoarseEnergy, EncodeCoarseEnergyTrace{
		Bands:       bands,
		Channels:    channels,
		BudgetBytes: int(budgetBytes),
		Input:       copyStageFloat32(input[:bands*channels]),
	})
}

func (s *encodeStageTraceState) recordCoarseOutput(quantized, errorValues []CeltGLog, re *rangecoding.Encoder) {
	if !s.enabled || len(s.trace.CoarseEnergy) == 0 {
		return
	}
	s.recordCoderRange(encodeCoderRangeAfterCoarse, re)
	stage := &s.trace.CoarseEnergy[len(s.trace.CoarseEnergy)-1]
	stage.Quantized = copyStageFloat32(quantized)
	stage.Error = copyStageFloat32(errorValues[:len(quantized)])
}

func (s *encodeStageTraceState) recordCoderRange(stage int32, re *rangecoding.Encoder) {
	if !s.enabled || s.coderRangeClosed {
		return
	}
	if re == nil || len(s.trace.CoderRanges) >= 3 {
		s.trace.StageOverflow = true
		return
	}
	s.trace.CoderRanges = append(s.trace.CoderRanges, EncodeCoderRangeTrace{
		Stage:    stage,
		TellFrac: int32(re.TellFrac()),
		Range:    re.Range(),
		Coder:    re,
	})
	if len(s.trace.CoderRanges) == 3 {
		s.coderRangeClosed = true
	}
}

func (s *encodeStageTraceState) recordQuantInput(normL, normR []CeltNorm, bandEnergy []CeltEner, activeCoeffs, bands, channels int) {
	if !s.enabled {
		return
	}
	input := make([]float32, activeCoeffs*channels)
	copyStagePrefix(input, normL, activeCoeffs)
	if channels > 1 {
		copyStagePrefix(input[activeCoeffs:], normR, activeCoeffs)
	}
	s.trace.BandQuantize = append(s.trace.BandQuantize, EncodeBandQuantizeTrace{
		ActiveCoeffs: activeCoeffs,
		Bands:        bands,
		Channels:     channels,
		BandEnergy:   copyStageFloat32(bandEnergy),
		Input:        input,
	})
}

func (e *Encoder) recordEncodeQuantInputTrace(normL, normR []CeltNorm, bandEnergy []CeltEner, bands, lm, channels int, re *rangecoding.Encoder) {
	activeCoeffs := e.modeEdges()[bands] * (1 << lm)
	e.encodeStageTrace.recordQuantInput(normL, normR, bandEnergy, activeCoeffs, bands, channels)
	e.encodeStageTrace.recordCoderRange(encodeCoderRangeBeforeQuant, re)
}

func (s *encodeStageTraceState) recordQuantOutput(normL, normR []CeltNorm) {
	if !s.enabled || len(s.trace.BandQuantize) == 0 {
		return
	}
	stage := &s.trace.BandQuantize[len(s.trace.BandQuantize)-1]
	stage.Output = make([]float32, stage.ActiveCoeffs*stage.Channels)
	copyStagePrefix(stage.Output, normL, stage.ActiveCoeffs)
	if stage.Channels > 1 {
		copyStagePrefix(stage.Output[stage.ActiveCoeffs:], normR, stage.ActiveCoeffs)
	}
}

func copyStageFloat32[T ~float32](src []T) []float32 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]float32, len(src))
	copyStagePrefix(dst, src, len(src))
	return dst
}

func copyStagePrefix[T ~float32](dst []float32, src []T, n int) {
	if n > len(src) {
		n = len(src)
	}
	for i := 0; i < n; i++ {
		dst[i] = float32(src[i])
	}
}
