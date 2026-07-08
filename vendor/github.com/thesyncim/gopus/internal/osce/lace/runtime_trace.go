//go:build gopus_osce

package lace

import "github.com/thesyncim/gopus/internal/opusmath"

// TraceStage identifies an opt-in LACE diagnostic checkpoint.
type TraceStage int

// LACE trace stages, numbered to match the C trace helper checkpoint enum.
// Each value names a point in lace_process_20ms_frame whose intermediate buffer
// ProcessTrace snapshots for differential debugging against libopus.
const (
	TraceStageInput TraceStage = iota + 1
	TraceStageFeatures
	TraceStageNumbits
	TraceStagePeriods
	TraceStagePreemph
	TraceStageFeatureNetConv1
	TraceStageFeatureNetConv2Input
	TraceStageFeatureNetConv2Linear
	TraceStageFeatureNetConv2
	TraceStageFeatureNetTConv
	TraceStageFeatureNetLatent
	TraceStagePostCF1
	TraceStagePostCF2
	TraceStagePostAF1
	TraceStageDeemph
	TraceStageCF1KernelRaw
	TraceStageCF1GainsRaw
	TraceStageCF1KernelScaled
	TraceStageCF1GainsScaled
)

// NoLACE-only trace stages. Numbered to match the C helper enum values 30..41.
const (
	TraceStageNLPreemph  TraceStage = 30
	TraceStageNLLatent   TraceStage = 31
	TraceStageNLPostCF1  TraceStage = 32
	TraceStageNLPostCF2  TraceStage = 33
	TraceStageNLPostAF1  TraceStage = 34
	TraceStageNLTDShape1 TraceStage = 35
	TraceStageNLPostAF2  TraceStage = 36
	TraceStageNLTDShape2 TraceStage = 37
	TraceStageNLPostAF3  TraceStage = 38
	TraceStageNLTDShape3 TraceStage = 39
	TraceStageNLPostAF4  TraceStage = 40
	TraceStageNLDeemph   TraceStage = 41
)

// TraceRecord captures one opt-in LACE diagnostic checkpoint. These records are
// intentionally allocated and are only built by ProcessTrace under the
// extra-controls build tag.
type TraceRecord struct {
	Stage             TraceStage
	Subframe          int
	Channels          int
	SamplesPerChannel int
	Values            []float32
}

// ProcessTrace mirrors Process for one 20 ms LACE frame while snapshotting the
// same checkpoints emitted by the libopus trace helper. Normal Process remains
// branch-free and allocation-free.
func (s *LACEState) ProcessTrace(in, out, features []float32, numbits []float32, periods []int) ([]TraceRecord, error) {
	if !s.Loaded() {
		return nil, errLACENoModel
	}
	if len(in) != frame20msSize {
		return nil, errLACEInLen
	}
	if len(out) < frame20msSize {
		return nil, errLACEOutLen
	}
	if len(features) < subframesPerFrame*laceNumFeatures {
		return nil, errLACEFeatures
	}
	if len(periods) < subframesPerFrame {
		return nil, errLACEPeriods
	}
	if len(numbits) < 2 {
		return nil, errLACENumbits
	}
	if err := validatePeriods(periods, lacePitchMax); err != nil {
		return nil, err
	}
	s.ensureWindow()
	m := &s.model.LACE

	records := make([]TraceRecord, 0, 31)
	appendTraceRecord(&records, TraceStageInput, -1, 1, frame20msSize, in[:frame20msSize])
	appendTraceRecord(&records, TraceStageFeatures, -1, 1, subframesPerFrame*laceNumFeatures, features[:subframesPerFrame*laceNumFeatures])
	appendTraceRecord(&records, TraceStageNumbits, -1, 1, 2, numbits[:2])
	appendTraceRecord(&records, TraceStagePeriods, -1, 1, subframesPerFrame, periodsAsFloat32(periods[:subframesPerFrame]))

	var outputBuf [frame20msSize]float32
	for i := 0; i < frame20msSize; i++ {
		outputBuf[i] = in[i] - lacePreemph*s.preempMem
		s.preempMem = in[i]
	}
	appendTraceRecord(&records, TraceStagePreemph, -1, 1, frame20msSize, outputBuf[:])

	var latent [subframesPerFrame * laceCondDim]float32
	s.featureNetTrace(latent[:], features, numbits, periods, &records)

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		traceAdaCombParams(
			&records, sf,
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.CF1Kernel, &m.CF1Gain, &m.CF1GlobalGain,
			laceCF1KernelSize,
			laceCF1FilterGainA, laceCF1FilterGainB, laceCF1LogGainLimit,
		)
		adacombProcessFrame(
			s.cf1History[:], s.cf1LastKernel[:], &s.cf1LastGlobalGain, &s.cf1LastPitchLag,
			outputBuf[base:base+subframeSize], outputBuf[base:base+subframeSize],
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.CF1Kernel, &m.CF1Gain, &m.CF1GlobalGain,
			periods[sf],
			subframeSize, laceOverlapSize,
			laceCF1KernelSize, laceCF1LeftPadding,
			laceCF1FilterGainA, laceCF1FilterGainB, laceCF1LogGainLimit,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStagePostCF1, -1, 1, frame20msSize, outputBuf[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		adacombProcessFrame(
			s.cf2History[:], s.cf2LastKernel[:], &s.cf2LastGlobalGain, &s.cf2LastPitchLag,
			outputBuf[base:base+subframeSize], outputBuf[base:base+subframeSize],
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.CF2Kernel, &m.CF2Gain, &m.CF2GlobalGain,
			periods[sf],
			subframeSize, laceOverlapSize,
			laceCF2KernelSize, laceCF2LeftPadding,
			laceCF2FilterGainA, laceCF2FilterGainB, laceCF2LogGainLimit,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStagePostCF2, -1, 1, frame20msSize, outputBuf[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		adaconvProcessFrame(
			s.af1History[:], s.af1LastKernel[:],
			outputBuf[base:base+subframeSize], outputBuf[base:base+subframeSize],
			latent[sf*laceCondDim:sf*laceCondDim+laceCondDim],
			&m.AF1Kernel, &m.AF1Gain,
			subframeSize, laceOverlapSize,
			laceAF1InChannels, laceAF1OutChannels,
			laceAF1KernelSize, laceAF1LeftPadding,
			laceAF1FilterGainA, laceAF1FilterGainB, 1.0,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStagePostAF1, -1, 1, frame20msSize, outputBuf[:])

	for i := 0; i < frame20msSize; i++ {
		out[i] = outputBuf[i] + lacePreemph*s.deempMem
		s.deempMem = out[i]
	}
	appendTraceRecord(&records, TraceStageDeemph, -1, 1, frame20msSize, out[:frame20msSize])

	return records, nil
}

func (s *LACEState) featureNetTrace(out, features []float32, numbits []float32, periods []int, records *[]TraceRecord) {
	m := &s.model.LACE
	const condDim = laceCondDim
	const hiddenDim = laceHiddenFeatureDim
	const numFeat = laceNumFeatures
	const pitchEmbDim = lacePitchEmbeddingDim
	const numbitsEmbDim = laceNumbitsEmbeddingDim
	const concatDim = numFeat + pitchEmbDim + 2*numbitsEmbDim

	var numbitsEmbedded [2 * numbitsEmbDim]float32
	low := opusmath.LogF32(laceNumbitsRangeLow)
	high := opusmath.LogF32(laceNumbitsRangeHigh)
	computeNumbitsEmbedding(numbitsEmbedded[:numbitsEmbDim], numbits[0], low, high, laceNumbitsScales)
	computeNumbitsEmbedding(numbitsEmbedded[numbitsEmbDim:], numbits[1], low, high, laceNumbitsScales)

	var conv1Out [subframesPerFrame * hiddenDim]float32
	var conv1In [concatDim]float32
	for sf := 0; sf < subframesPerFrame; sf++ {
		copy(conv1In[:numFeat], features[sf*numFeat:sf*numFeat+numFeat])
		period := periods[sf]
		if !m.PitchEmbedding.FloatWeights.Empty() {
			for j := 0; j < pitchEmbDim; j++ {
				conv1In[numFeat+j] = m.PitchEmbedding.FloatWeights.At(period*pitchEmbDim + j)
			}
		}
		copy(conv1In[numFeat+pitchEmbDim:], numbitsEmbedded[:])

		computeGenericConv1D(
			&m.FNetConv1,
			conv1Out[sf*hiddenDim:sf*hiddenDim+hiddenDim],
			nil,
			conv1In[:concatDim],
			concatDim, actTanh,
		)
	}
	appendTraceRecord(records, TraceStageFeatureNetConv1, -1, 1, subframesPerFrame*hiddenDim, conv1Out[:])

	const accInputSize = subframesPerFrame * hiddenDim
	memLen := m.FNetConv2.NbInputs - accInputSize
	var conv2Input [laceHiddenFeatureDim * subframesPerFrame * 2]float32
	if memLen > 0 {
		copy(conv2Input[:memLen], s.fnetConv2State[:memLen])
	}
	copy(conv2Input[memLen:m.FNetConv2.NbInputs], conv1Out[:])
	appendTraceRecord(records, TraceStageFeatureNetConv2Input, -1, 1, m.FNetConv2.NbInputs, conv2Input[:m.FNetConv2.NbInputs])

	var conv2Out [condDim]float32
	computeLinear(
		&m.FNetConv2,
		conv2Out[:],
		conv2Input[:m.FNetConv2.NbInputs],
	)
	appendTraceRecord(records, TraceStageFeatureNetConv2Linear, -1, 1, condDim, conv2Out[:])
	computeActivation(conv2Out[:], conv2Out[:], m.FNetConv2.NbOutputs, actTanh)
	if memLen > 0 {
		copy(s.fnetConv2State[:memLen], conv2Input[accInputSize:m.FNetConv2.NbInputs])
	}
	appendTraceRecord(records, TraceStageFeatureNetConv2, -1, 1, condDim, conv2Out[:])

	var tconvOut [subframesPerFrame * condDim]float32
	computeGenericDense(
		&m.FNetTConv,
		tconvOut[:],
		conv2Out[:],
		actTanh,
	)
	appendTraceRecord(records, TraceStageFeatureNetTConv, -1, 1, subframesPerFrame*condDim, tconvOut[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		computeGenericGRU(
			&m.FNetGRUInput, &m.FNetGRURecurrent,
			s.fnetGRUState[:],
			tconvOut[sf*condDim:sf*condDim+condDim],
		)
		copy(out[sf*condDim:sf*condDim+condDim], s.fnetGRUState[:])
	}
	appendTraceRecord(records, TraceStageFeatureNetLatent, -1, 1, subframesPerFrame*condDim, out[:subframesPerFrame*condDim])
}

func traceAdaCombParams(
	records *[]TraceRecord,
	subframe int,
	features []float32,
	kernelLayer, gainLayer, globalGainLayer *LinearLayer,
	kernelSize int,
	filterGainA, filterGainB, logGainLimit float32,
) {
	var kernel [adaCombMaxKernelSize]float32
	var gains [2]float32
	computeGenericDense(kernelLayer, kernel[:kernelSize], features, actLinear)
	computeGenericDense(gainLayer, gains[:1], features, actRelu)
	computeGenericDense(globalGainLayer, gains[1:2], features, actTanh)
	appendTraceRecord(records, TraceStageCF1KernelRaw, subframe, 1, kernelSize, kernel[:kernelSize])
	appendTraceRecord(records, TraceStageCF1GainsRaw, subframe, 1, len(gains), gains[:])

	gains[0] = opusmath.ExpF32(logGainLimit - gains[0])
	gains[1] = opusmath.ExpF32(filterGainA*gains[1] + filterGainB)
	var norm float32
	for k := 0; k < kernelSize; k++ {
		norm += roundMul32(kernel[k], kernel[k])
	}
	invNorm := scaleKernelInvNorm(norm)
	scale := invNorm * gains[0]
	for k := 0; k < kernelSize; k++ {
		kernel[k] *= scale
	}
	appendTraceRecord(records, TraceStageCF1KernelScaled, subframe, 1, kernelSize, kernel[:kernelSize])
	appendTraceRecord(records, TraceStageCF1GainsScaled, subframe, 1, len(gains), gains[:])
}

func appendTraceRecord(records *[]TraceRecord, stage TraceStage, subframe, channels, samplesPerChannel int, values []float32) {
	snapshot := append([]float32(nil), values...)
	*records = append(*records, TraceRecord{
		Stage:             stage,
		Subframe:          subframe,
		Channels:          channels,
		SamplesPerChannel: samplesPerChannel,
		Values:            snapshot,
	})
}

func periodsAsFloat32(periods []int) []float32 {
	out := make([]float32, len(periods))
	for i, period := range periods {
		out[i] = float32(period)
	}
	return out
}

// ProcessTrace mirrors NoLACEState.Process for one 20 ms frame while
// snapshotting the per-stage checkpoints emitted by the libopus NoLACE trace
// helper (trace_nolace_process_20ms_frame). The arithmetic path is identical to
// Process; only the additional record snapshots are inserted.
func (s *NoLACEState) ProcessTrace(in, out, features []float32, numbits []float32, periods []int) ([]TraceRecord, error) {
	if !s.Loaded() {
		return nil, errLACENoModel
	}
	if len(in) != frame20msSize {
		return nil, errLACEInLen
	}
	if len(out) < frame20msSize {
		return nil, errLACEOutLen
	}
	if len(features) < subframesPerFrame*nolaceNumFeatures {
		return nil, errLACEFeatures
	}
	if len(periods) < subframesPerFrame {
		return nil, errLACEPeriods
	}
	if len(numbits) < 2 {
		return nil, errLACENumbits
	}
	if err := validatePeriods(periods, nolacePitchMax); err != nil {
		return nil, err
	}
	s.ensureWindow()
	m := &s.model.NoLACE

	records := make([]TraceRecord, 0, 16)
	appendTraceRecord(&records, TraceStageInput, -1, 1, frame20msSize, in[:frame20msSize])
	appendTraceRecord(&records, TraceStageFeatures, -1, 1, subframesPerFrame*nolaceNumFeatures, features[:subframesPerFrame*nolaceNumFeatures])
	appendTraceRecord(&records, TraceStageNumbits, -1, 1, 2, numbits[:2])
	appendTraceRecord(&records, TraceStagePeriods, -1, 1, subframesPerFrame, periodsAsFloat32(periods[:subframesPerFrame]))

	var xBuf1 [subframesPerFrame * 2 * subframeSize]float32
	var xBuf2 [subframesPerFrame * 2 * subframeSize]float32

	for i := 0; i < frame20msSize; i++ {
		xBuf1[i] = in[i] - nolacePreemph*s.preempMem
		s.preempMem = in[i]
	}
	appendTraceRecord(&records, TraceStageNLPreemph, -1, 1, frame20msSize, xBuf1[:frame20msSize])

	var featureBuf [subframesPerFrame * nolaceCondDim]float32
	var featureTransformBuf [subframesPerFrame * nolaceCondDim]float32
	s.featureNet(featureBuf[:], features, numbits, periods)
	appendTraceRecord(&records, TraceStageNLLatent, -1, 1, subframesPerFrame*nolaceCondDim, featureBuf[:])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		adacombProcessFrame(
			s.cf1History[:], s.cf1LastKernel[:], &s.cf1LastGlobalGain, &s.cf1LastPitchLag,
			xBuf1[base:base+subframeSize], xBuf1[base:base+subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.CF1Kernel, &m.CF1Gain, &m.CF1GlobalGain,
			periods[sf], subframeSize, nolaceOverlapSize,
			nolaceCF1KernelSize, nolaceCF1LeftPadding,
			nolaceCF1FilterGainA, nolaceCF1FilterGainB, nolaceCF1LogGainLimit,
			s.window[:],
		)
		computeGenericConv1D(&m.PostCF1,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postCF1State[:], featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh)
	}
	copy(featureBuf[:], featureTransformBuf[:])
	appendTraceRecord(&records, TraceStageNLPostCF1, -1, 1, frame20msSize, xBuf1[:frame20msSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base := sf * subframeSize
		adacombProcessFrame(
			s.cf2History[:], s.cf2LastKernel[:], &s.cf2LastGlobalGain, &s.cf2LastPitchLag,
			xBuf1[base:base+subframeSize], xBuf1[base:base+subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.CF2Kernel, &m.CF2Gain, &m.CF2GlobalGain,
			periods[sf], subframeSize, nolaceOverlapSize,
			nolaceCF2KernelSize, nolaceCF2LeftPadding,
			nolaceCF2FilterGainA, nolaceCF2FilterGainB, nolaceCF2LogGainLimit,
			s.window[:],
		)
		computeGenericConv1D(&m.PostCF2,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postCF2State[:], featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh)
	}
	copy(featureBuf[:], featureTransformBuf[:])
	appendTraceRecord(&records, TraceStageNLPostCF2, -1, 1, frame20msSize, xBuf1[:frame20msSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		inBase := sf * subframeSize
		outBase := sf * nolaceAF1OutChannels * subframeSize
		adaconvProcessFrame(
			s.af1History[:], s.af1LastKernel[:],
			xBuf2[outBase:outBase+nolaceAF1OutChannels*subframeSize],
			xBuf1[inBase:inBase+subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.AF1Kernel, &m.AF1Gain,
			subframeSize, nolaceOverlapSize,
			nolaceAF1InChannels, nolaceAF1OutChannels,
			nolaceAF1KernelSize, nolaceAF1LeftPadding,
			nolaceAF1FilterGainA, nolaceAF1FilterGainB, 1.0,
			s.window[:],
		)
		computeGenericConv1D(&m.PostAF1,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postAF1State[:], featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh)
	}
	copy(featureBuf[:], featureTransformBuf[:])
	appendTraceRecord(&records, TraceStageNLPostAF1, -1, 1, subframesPerFrame*nolaceAF1OutChannels*subframeSize, xBuf2[:subframesPerFrame*nolaceAF1OutChannels*subframeSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base2 := sf * nolaceAF1OutChannels * subframeSize
		adashapeProcessFrame(
			s.tdshape1Alpha1FState[:], s.tdshape1Alpha1TState[:], s.tdshape1Alpha2State[:],
			&s.tdshape1InterpState,
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.TDShape1Alpha1F, &m.TDShape1Alpha1T, &m.TDShape1Alpha2,
			nolaceCondDim, subframeSize, nolaceTDShapeAvgPoolK, nolaceTDShapeInterpolK,
		)
	}
	appendTraceRecord(&records, TraceStageNLTDShape1, -1, 1, subframesPerFrame*nolaceAF1OutChannels*subframeSize, xBuf2[:subframesPerFrame*nolaceAF1OutChannels*subframeSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base2 := sf * nolaceAF1OutChannels * subframeSize
		base1 := sf * nolaceAF2OutChannels * subframeSize
		adaconvProcessFrame(
			s.af2History[:], s.af2LastKernel[:],
			xBuf1[base1:base1+nolaceAF2OutChannels*subframeSize],
			xBuf2[base2:base2+nolaceAF2InChannels*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.AF2Kernel, &m.AF2Gain,
			subframeSize, nolaceOverlapSize,
			nolaceAF2InChannels, nolaceAF2OutChannels,
			nolaceAF2KernelSize, nolaceAF2LeftPadding,
			nolaceAF2FilterGainA, nolaceAF2FilterGainB, 1.0,
			s.window[:],
		)
		computeGenericConv1D(&m.PostAF2,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postAF2State[:], featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh)
	}
	copy(featureBuf[:], featureTransformBuf[:])
	appendTraceRecord(&records, TraceStageNLPostAF2, -1, 1, subframesPerFrame*nolaceAF2OutChannels*subframeSize, xBuf1[:subframesPerFrame*nolaceAF2OutChannels*subframeSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base1 := sf * nolaceAF2OutChannels * subframeSize
		adashapeProcessFrame(
			s.tdshape2Alpha1FState[:], s.tdshape2Alpha1TState[:], s.tdshape2Alpha2State[:],
			&s.tdshape2InterpState,
			xBuf1[base1+subframeSize:base1+2*subframeSize],
			xBuf1[base1+subframeSize:base1+2*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.TDShape2Alpha1F, &m.TDShape2Alpha1T, &m.TDShape2Alpha2,
			nolaceCondDim, subframeSize, nolaceTDShapeAvgPoolK, nolaceTDShapeInterpolK,
		)
	}
	appendTraceRecord(&records, TraceStageNLTDShape2, -1, 1, subframesPerFrame*nolaceAF2OutChannels*subframeSize, xBuf1[:subframesPerFrame*nolaceAF2OutChannels*subframeSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base1 := sf * nolaceAF2OutChannels * subframeSize
		base2 := sf * nolaceAF3OutChannels * subframeSize
		adaconvProcessFrame(
			s.af3History[:], s.af3LastKernel[:],
			xBuf2[base2:base2+nolaceAF3OutChannels*subframeSize],
			xBuf1[base1:base1+nolaceAF3InChannels*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.AF3Kernel, &m.AF3Gain,
			subframeSize, nolaceOverlapSize,
			nolaceAF3InChannels, nolaceAF3OutChannels,
			nolaceAF3KernelSize, nolaceAF3LeftPadding,
			nolaceAF3FilterGainA, nolaceAF3FilterGainB, 1.0,
			s.window[:],
		)
		computeGenericConv1D(&m.PostAF3,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postAF3State[:], featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh)
	}
	copy(featureBuf[:], featureTransformBuf[:])
	appendTraceRecord(&records, TraceStageNLPostAF3, -1, 1, subframesPerFrame*nolaceAF3OutChannels*subframeSize, xBuf2[:subframesPerFrame*nolaceAF3OutChannels*subframeSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base2 := sf * nolaceAF3OutChannels * subframeSize
		adashapeProcessFrame(
			s.tdshape3Alpha1FState[:], s.tdshape3Alpha1TState[:], s.tdshape3Alpha2State[:],
			&s.tdshape3InterpState,
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.TDShape3Alpha1F, &m.TDShape3Alpha1T, &m.TDShape3Alpha2,
			nolaceCondDim, subframeSize, nolaceTDShapeAvgPoolK, nolaceTDShapeInterpolK,
		)
	}
	appendTraceRecord(&records, TraceStageNLTDShape3, -1, 1, subframesPerFrame*nolaceAF3OutChannels*subframeSize, xBuf2[:subframesPerFrame*nolaceAF3OutChannels*subframeSize])

	for sf := 0; sf < subframesPerFrame; sf++ {
		base2 := sf * nolaceAF3OutChannels * subframeSize
		base1 := sf * nolaceAF4OutChannels * subframeSize
		adaconvProcessFrame(
			s.af4History[:], s.af4LastKernel[:],
			xBuf1[base1:base1+nolaceAF4OutChannels*subframeSize],
			xBuf2[base2:base2+nolaceAF4InChannels*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.AF4Kernel, &m.AF4Gain,
			subframeSize, nolaceOverlapSize,
			nolaceAF4InChannels, nolaceAF4OutChannels,
			nolaceAF4KernelSize, nolaceAF4LeftPadding,
			nolaceAF4FilterGainA, nolaceAF4FilterGainB, 1.0,
			s.window[:],
		)
	}
	appendTraceRecord(&records, TraceStageNLPostAF4, -1, 1, subframesPerFrame*nolaceAF4OutChannels*subframeSize, xBuf1[:subframesPerFrame*nolaceAF4OutChannels*subframeSize])

	for i := 0; i < frame20msSize; i++ {
		out[i] = xBuf1[i] + nolacePreemph*s.deempMem
		s.deempMem = out[i]
	}
	appendTraceRecord(&records, TraceStageNLDeemph, -1, 1, frame20msSize, out[:frame20msSize])

	return records, nil
}
