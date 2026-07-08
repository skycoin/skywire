package lace

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// LACE / NoLACE forward-pass constants are copied verbatim from libopus
// 1.6.1 dnn/lace_data.h and dnn/nolace_data.h. Both postfilters run on 20 ms
// frames at 16 kHz (320 samples in, 320 out -- postfilter, not upsampler)
// split into 4 subframes of 80 samples each.
//
// The forward pass is a Go translation of lace_process_20ms_frame and
// nolace_process_20ms_frame from dnn/osce.c: feature net, two AdaComb stages,
// the AdaConv chain, three AdaShape blocks and the NoLACE post-conv
// refinements. Output matches libopus, gated by the LACE / NoLACE forward-pass
// parity tests.
const (
	// Sample rates: LACE/NoLACE are 16 kHz postfilters.
	SampleRate = 16000

	// Common subframe geometry.
	subframeSize      = 80 // LACE_FRAME_SIZE / NOLACE_FRAME_SIZE
	subframesPerFrame = 4
	frame20msSize     = subframesPerFrame * subframeSize // 320

	// LACE feature net.
	laceNumFeatures         = 93  // LACE_NUM_FEATURES
	lacePitchEmbeddingDim   = 64  // LACE_PITCH_EMBEDDING_DIM
	laceNumbitsEmbeddingDim = 8   // LACE_NUMBITS_EMBEDDING_DIM
	laceHiddenFeatureDim    = 96  // LACE_HIDDEN_FEATURE_DIM
	laceCondDim             = 128 // LACE_COND_DIM
	lacePitchMax            = 300 // LACE_PITCH_MAX
	lacePreemph             = 0.85
	laceOverlapSize         = 40 // LACE_OVERLAP_SIZE

	// LACE numbits-embedding ranges and scales.
	laceNumbitsRangeLow  = 50.0
	laceNumbitsRangeHigh = 650.0

	// LACE CF1/CF2 (AdaComb) geometry.
	laceCF1KernelSize   = 16
	laceCF1LeftPadding  = 8
	laceCF1FilterGainA  = 0.690776
	laceCF1FilterGainB  = 0.000000
	laceCF1LogGainLimit = 1.151293
	laceCF2KernelSize   = 16
	laceCF2LeftPadding  = 8
	laceCF2FilterGainA  = 0.690776
	laceCF2FilterGainB  = 0.000000
	laceCF2LogGainLimit = 1.151293

	// LACE AF1 (AdaConv) geometry.
	laceAF1KernelSize  = 16
	laceAF1LeftPadding = 15
	laceAF1FilterGainA = 1.381551
	laceAF1FilterGainB = 0.000000
	laceAF1InChannels  = 1
	laceAF1OutChannels = 1

	// NoLACE feature net.
	nolaceNumFeatures         = 93
	nolacePitchEmbeddingDim   = 64
	nolaceNumbitsEmbeddingDim = 8
	nolaceHiddenFeatureDim    = 96
	nolaceCondDim             = 160
	nolacePitchMax            = 300
	nolacePreemph             = 0.85
	nolaceOverlapSize         = 40

	nolaceNumbitsRangeLow  = 50.0
	nolaceNumbitsRangeHigh = 650.0

	nolaceCF1KernelSize   = 16
	nolaceCF1LeftPadding  = 8
	nolaceCF1FilterGainA  = 0.690776
	nolaceCF1FilterGainB  = 0.000000
	nolaceCF1LogGainLimit = 1.151293
	nolaceCF2KernelSize   = 16
	nolaceCF2LeftPadding  = 8
	nolaceCF2FilterGainA  = 0.690776
	nolaceCF2FilterGainB  = 0.000000
	nolaceCF2LogGainLimit = 1.151293

	// AdaConv stages.
	nolaceAF1KernelSize  = 16
	nolaceAF1LeftPadding = 15
	nolaceAF1FilterGainA = 1.381551
	nolaceAF1FilterGainB = 0.000000
	nolaceAF1InChannels  = 1
	nolaceAF1OutChannels = 2

	nolaceAF2KernelSize  = 16
	nolaceAF2LeftPadding = 15
	nolaceAF2FilterGainA = 1.381551
	nolaceAF2FilterGainB = 0.000000
	nolaceAF2InChannels  = 2
	nolaceAF2OutChannels = 2

	nolaceAF3KernelSize  = 16
	nolaceAF3LeftPadding = 15
	nolaceAF3FilterGainA = 1.381551
	nolaceAF3FilterGainB = 0.000000
	nolaceAF3InChannels  = 2
	nolaceAF3OutChannels = 2

	nolaceAF4KernelSize  = 16
	nolaceAF4LeftPadding = 15
	nolaceAF4FilterGainA = 1.381551
	nolaceAF4FilterGainB = 0.000000
	nolaceAF4InChannels  = 2
	nolaceAF4OutChannels = 1

	// NoLACE TDShape (alpha pool / interpolate).
	nolaceTDShapeAvgPoolK  = 4
	nolaceTDShapeInterpolK = 1
)

// LACE numbits scales (LACE_NUMBITS_SCALE_0..7).
var laceNumbitsScales = [8]float32{
	1.0983514785766602, 2.0509142875671387, 3.5729939937591553, 4.478035926818848,
	5.926519393920898, 7.152282238006592, 8.277412414550781, 8.926830291748047,
}

// NoLACE numbits scales (NOLACE_NUMBITS_SCALE_0..7).
var nolaceNumbitsScales = [8]float32{
	1.0357311964035034, 1.735559105873108, 3.6004557609558105, 4.552478313446045,
	5.932559490203857, 7.176970481872559, 8.114998817443848, 8.77063274383545,
}

// Activation enum mirrors libopus dnn/nnet.h ACTIVATION_* values.
const (
	actLinear  = 0
	actSigmoid = 1
	actTanh    = 2
	actRelu    = 3
	actSoftmax = 4
	actExp     = 6
)

// LACE adacomb buffer constants from nndsp.h.
const (
	adaCombMaxLag        = 300
	adaCombMaxKernelSize = 16
	adaCombMaxFrameSize  = 80
	adaCombMaxOverlap    = 40
)

// LACEState is the per-stream LACE runtime state mirroring libopus
// `LACEState` (dnn/osce_structs.h). Persistent fields:
//   - feature_net_conv2_state: history for the conv2 layer (kernel 3, so
//     384 = 2 * 128 floats).
//   - feature_net_gru_state: 128 floats of GRU recurrent state.
//   - cf1_state, cf2_state: AdaComb persistent state.
//   - af1_state: AdaConv persistent state.
//   - preemph_mem / deemph_mem: pre/de-emphasis filter taps.
type LACEState struct {
	model *Model

	fnetConv2State [384]float32
	fnetGRUState   [laceCondDim]float32

	cf1History        [adaCombMaxKernelSize + adaCombMaxLag]float32
	cf1LastKernel     [adaCombMaxKernelSize]float32
	cf1LastGlobalGain float32
	cf1LastPitchLag   int

	cf2History        [adaCombMaxKernelSize + adaCombMaxLag]float32
	cf2LastKernel     [adaCombMaxKernelSize]float32
	cf2LastGlobalGain float32
	cf2LastPitchLag   int

	af1History    [laceAF1KernelSize * laceAF1InChannels]float32
	af1LastKernel [laceAF1KernelSize * laceAF1InChannels * laceAF1OutChannels]float32

	preempMem float32
	deempMem  float32

	windowInit bool
	window     [laceOverlapSize]float32
}

// NoLACEState is the per-stream NoLACE runtime state mirroring libopus
// `NoLACEState` (dnn/osce_structs.h). NoLACE carries the same feature-net
// scratchpads as LACE but at the wider 160-channel COND_DIM and adds five
// `post_*_state` slots plus three AdaShape blocks.
type NoLACEState struct {
	model *Model

	fnetConv2State [384]float32
	fnetGRUState   [nolaceCondDim]float32

	cf1History        [adaCombMaxKernelSize + adaCombMaxLag]float32
	cf1LastKernel     [adaCombMaxKernelSize]float32
	cf1LastGlobalGain float32
	cf1LastPitchLag   int

	cf2History        [adaCombMaxKernelSize + adaCombMaxLag]float32
	cf2LastKernel     [adaCombMaxKernelSize]float32
	cf2LastGlobalGain float32
	cf2LastPitchLag   int

	af1History    [nolaceAF1KernelSize * nolaceAF1InChannels]float32
	af1LastKernel [nolaceAF1KernelSize * nolaceAF1InChannels * nolaceAF1OutChannels]float32

	af2History    [nolaceAF2KernelSize * nolaceAF2InChannels]float32
	af2LastKernel [nolaceAF2KernelSize * nolaceAF2InChannels * nolaceAF2OutChannels]float32

	af3History    [nolaceAF3KernelSize * nolaceAF3InChannels]float32
	af3LastKernel [nolaceAF3KernelSize * nolaceAF3InChannels * nolaceAF3OutChannels]float32

	af4History    [nolaceAF4KernelSize * nolaceAF4InChannels]float32
	af4LastKernel [nolaceAF4KernelSize * nolaceAF4InChannels * nolaceAF4OutChannels]float32

	postCF1State [nolaceCondDim]float32
	postCF2State [nolaceCondDim]float32
	postAF1State [nolaceCondDim]float32
	postAF2State [nolaceCondDim]float32
	postAF3State [nolaceCondDim]float32

	tdshape1Alpha1FState [nolaceCondDim]float32
	tdshape1Alpha1TState [21]float32
	tdshape1Alpha2State  [subframeSize]float32
	tdshape1InterpState  float32

	tdshape2Alpha1FState [nolaceCondDim]float32
	tdshape2Alpha1TState [21]float32
	tdshape2Alpha2State  [subframeSize]float32
	tdshape2InterpState  float32

	tdshape3Alpha1FState [nolaceCondDim]float32
	tdshape3Alpha1TState [21]float32
	tdshape3Alpha2State  [subframeSize]float32
	tdshape3InterpState  float32

	preempMem float32
	deempMem  float32

	windowInit bool
	window     [nolaceOverlapSize]float32
}

// Errors returned by Process when the inputs do not match the libopus
// reference constraints.
var (
	errLACENoModel  = errors.New("osce/lace: no model bound")
	errLACEInLen    = errors.New("osce/lace: unsupported input length (expected 320 samples for 20 ms @ 16 kHz)")
	errLACEOutLen   = errors.New("osce/lace: output buffer too short (need 320 samples)")
	errLACEFeatures = errors.New("osce/lace: invalid features length (expected 4 * 93)")
	errLACEPeriods  = errors.New("osce/lace: invalid periods length (expected 4)")
	errLACEPeriod   = errors.New("osce/lace: invalid period value (expected 0..300)")
	errLACENumbits  = errors.New("osce/lace: invalid numbits length (expected 2)")
)

// SetModel binds (or clears) the LACE model on the runtime state.
func (s *LACEState) SetModel(model *Model) error {
	if s == nil {
		return errLACENoModel
	}
	s.model = model
	s.Reset()
	return nil
}

// Loaded reports whether the runtime has a valid model binding.
func (s *LACEState) Loaded() bool {
	return s != nil && s.model != nil && s.model.Loaded()
}

// Reset clears the per-stream working buffers libopus zero-initialises in
// `reset_lace_state`. The model binding survives.
func (s *LACEState) Reset() {
	if s == nil {
		return
	}
	s.fnetConv2State = [384]float32{}
	s.fnetGRUState = [laceCondDim]float32{}
	s.cf1History = [adaCombMaxKernelSize + adaCombMaxLag]float32{}
	s.cf1LastKernel = [adaCombMaxKernelSize]float32{}
	s.cf1LastGlobalGain = 0
	s.cf1LastPitchLag = 0
	s.cf2History = [adaCombMaxKernelSize + adaCombMaxLag]float32{}
	s.cf2LastKernel = [adaCombMaxKernelSize]float32{}
	s.cf2LastGlobalGain = 0
	s.cf2LastPitchLag = 0
	s.af1History = [laceAF1KernelSize * laceAF1InChannels]float32{}
	s.af1LastKernel = [laceAF1KernelSize * laceAF1InChannels * laceAF1OutChannels]float32{}
	s.preempMem = 0
	s.deempMem = 0
}

// SetModel binds (or clears) the NoLACE model on the runtime state.
func (s *NoLACEState) SetModel(model *Model) error {
	if s == nil {
		return errLACENoModel
	}
	s.model = model
	s.Reset()
	return nil
}

// Loaded reports whether the runtime has a valid model binding.
func (s *NoLACEState) Loaded() bool {
	return s != nil && s.model != nil && s.model.Loaded()
}

// Reset clears the per-stream working buffers libopus zero-initialises in
// `reset_nolace_state`.
func (s *NoLACEState) Reset() {
	if s == nil {
		return
	}
	s.fnetConv2State = [384]float32{}
	s.fnetGRUState = [nolaceCondDim]float32{}
	s.cf1History = [adaCombMaxKernelSize + adaCombMaxLag]float32{}
	s.cf1LastKernel = [adaCombMaxKernelSize]float32{}
	s.cf1LastGlobalGain = 0
	s.cf1LastPitchLag = 0
	s.cf2History = [adaCombMaxKernelSize + adaCombMaxLag]float32{}
	s.cf2LastKernel = [adaCombMaxKernelSize]float32{}
	s.cf2LastGlobalGain = 0
	s.cf2LastPitchLag = 0
	s.af1History = [nolaceAF1KernelSize * nolaceAF1InChannels]float32{}
	s.af1LastKernel = [nolaceAF1KernelSize * nolaceAF1InChannels * nolaceAF1OutChannels]float32{}
	s.af2History = [nolaceAF2KernelSize * nolaceAF2InChannels]float32{}
	s.af2LastKernel = [nolaceAF2KernelSize * nolaceAF2InChannels * nolaceAF2OutChannels]float32{}
	s.af3History = [nolaceAF3KernelSize * nolaceAF3InChannels]float32{}
	s.af3LastKernel = [nolaceAF3KernelSize * nolaceAF3InChannels * nolaceAF3OutChannels]float32{}
	s.af4History = [nolaceAF4KernelSize * nolaceAF4InChannels]float32{}
	s.af4LastKernel = [nolaceAF4KernelSize * nolaceAF4InChannels * nolaceAF4OutChannels]float32{}
	s.postCF1State = [nolaceCondDim]float32{}
	s.postCF2State = [nolaceCondDim]float32{}
	s.postAF1State = [nolaceCondDim]float32{}
	s.postAF2State = [nolaceCondDim]float32{}
	s.postAF3State = [nolaceCondDim]float32{}
	s.tdshape1Alpha1FState = [nolaceCondDim]float32{}
	s.tdshape1Alpha1TState = [21]float32{}
	s.tdshape1Alpha2State = [subframeSize]float32{}
	s.tdshape1InterpState = 0
	s.tdshape2Alpha1FState = [nolaceCondDim]float32{}
	s.tdshape2Alpha1TState = [21]float32{}
	s.tdshape2Alpha2State = [subframeSize]float32{}
	s.tdshape2InterpState = 0
	s.tdshape3Alpha1FState = [nolaceCondDim]float32{}
	s.tdshape3Alpha1TState = [21]float32{}
	s.tdshape3Alpha2State = [subframeSize]float32{}
	s.tdshape3InterpState = 0
	s.preempMem = 0
	s.deempMem = 0
}

// Process runs one 20 ms LACE forward pass.
//
//	in       320 float32 samples (16 kHz, normalised to [-1, 1])
//	out      320 float32 samples; may alias `in`.
//	features 4 * 93 float32 OSCE features (per-subframe)
//	numbits  2 float32 numbits values
//	periods  4 int pitch periods (one per subframe)
func (s *LACEState) Process(in, out, features []float32, numbits []float32, periods []int) error {
	if !s.Loaded() {
		return errLACENoModel
	}
	if len(in) != frame20msSize {
		return errLACEInLen
	}
	if len(out) < frame20msSize {
		return errLACEOutLen
	}
	if len(features) < subframesPerFrame*laceNumFeatures {
		return errLACEFeatures
	}
	if len(periods) < subframesPerFrame {
		return errLACEPeriods
	}
	if len(numbits) < 2 {
		return errLACENumbits
	}
	if err := validatePeriods(periods, lacePitchMax); err != nil {
		return err
	}
	s.ensureWindow()
	m := &s.model.LACE

	// Output scratch (per-sample, single channel).
	var outputBuf [frame20msSize]float32

	// Pre-emphasis.
	for i := range frame20msSize {
		outputBuf[i] = in[i] - lacePreemph*s.preempMem
		s.preempMem = in[i]
	}

	// Feature net -> latent_features[4 * COND_DIM].
	var latent [subframesPerFrame * laceCondDim]float32
	s.featureNet(latent[:], features, numbits, periods)

	// CF1 (1st AdaComb stage).
	for sf := range subframesPerFrame {
		base := sf * subframeSize
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

	// CF2 (2nd AdaComb stage).
	for sf := range subframesPerFrame {
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

	// AF1 (single AdaConv stage, 1 -> 1 channel, kernel 16).
	for sf := range subframesPerFrame {
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

	// De-emphasis.
	for i := range frame20msSize {
		out[i] = outputBuf[i] + lacePreemph*s.deempMem
		s.deempMem = out[i]
	}
	return nil
}

// Process runs one 20 ms NoLACE forward pass.
//
//	in       320 float32 samples (16 kHz, normalised to [-1, 1])
//	out      320 float32 samples; may alias `in`.
//	features 4 * 93 float32 OSCE features (per-subframe)
//	numbits  2 float32 numbits values
//	periods  4 int pitch periods (one per subframe)
func (s *NoLACEState) Process(in, out, features []float32, numbits []float32, periods []int) error {
	if !s.Loaded() {
		return errLACENoModel
	}
	if len(in) != frame20msSize {
		return errLACEInLen
	}
	if len(out) < frame20msSize {
		return errLACEOutLen
	}
	if len(features) < subframesPerFrame*nolaceNumFeatures {
		return errLACEFeatures
	}
	if len(periods) < subframesPerFrame {
		return errLACEPeriods
	}
	if len(numbits) < 2 {
		return errLACENumbits
	}
	if err := validatePeriods(periods, nolacePitchMax); err != nil {
		return err
	}
	s.ensureWindow()
	m := &s.model.NoLACE

	// Signal buffers. The NoLACE pipeline writes 2-channel signals after AF1,
	// so we allocate 2 * frame_size per subframe (4 * 2 * 80 = 640 floats).
	var xBuf1 [subframesPerFrame * 2 * subframeSize]float32
	var xBuf2 [subframesPerFrame * 2 * subframeSize]float32

	// Pre-emphasis.
	for i := range frame20msSize {
		xBuf1[i] = in[i] - nolacePreemph*s.preempMem
		s.preempMem = in[i]
	}

	// Feature net -> feature_buffer[4 * COND_DIM].
	var featureBuf [subframesPerFrame * nolaceCondDim]float32
	var featureTransformBuf [subframesPerFrame * nolaceCondDim]float32
	s.featureNet(featureBuf[:], features, numbits, periods)

	// 1st AdaComb stage + post-CF1 conv1d.
	for sf := range subframesPerFrame {
		base := sf * subframeSize
		adacombProcessFrame(
			s.cf1History[:], s.cf1LastKernel[:], &s.cf1LastGlobalGain, &s.cf1LastPitchLag,
			xBuf1[base:base+subframeSize], xBuf1[base:base+subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.CF1Kernel, &m.CF1Gain, &m.CF1GlobalGain,
			periods[sf],
			subframeSize, nolaceOverlapSize,
			nolaceCF1KernelSize, nolaceCF1LeftPadding,
			nolaceCF1FilterGainA, nolaceCF1FilterGainB, nolaceCF1LogGainLimit,
			s.window[:],
		)

		computeGenericConv1D(
			&m.PostCF1,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postCF1State[:],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh,
		)
	}
	copy(featureBuf[:], featureTransformBuf[:])

	// 2nd AdaComb stage + post-CF2 conv1d.
	for sf := range subframesPerFrame {
		base := sf * subframeSize
		adacombProcessFrame(
			s.cf2History[:], s.cf2LastKernel[:], &s.cf2LastGlobalGain, &s.cf2LastPitchLag,
			xBuf1[base:base+subframeSize], xBuf1[base:base+subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.CF2Kernel, &m.CF2Gain, &m.CF2GlobalGain,
			periods[sf],
			subframeSize, nolaceOverlapSize,
			nolaceCF2KernelSize, nolaceCF2LeftPadding,
			nolaceCF2FilterGainA, nolaceCF2FilterGainB, nolaceCF2LogGainLimit,
			s.window[:],
		)

		computeGenericConv1D(
			&m.PostCF2,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postCF2State[:],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh,
		)
	}
	copy(featureBuf[:], featureTransformBuf[:])

	// AF1: 1 -> 2 channels. Output written to xBuf2 (2 * subframe_size per subframe).
	for sf := range subframesPerFrame {
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

		computeGenericConv1D(
			&m.PostAF1,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postAF1State[:],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh,
		)
	}
	copy(featureBuf[:], featureTransformBuf[:])

	// First shape-mix round: tdshape1 on channel 1 of xBuf2 (in-place),
	// then AF2 (2 -> 2 channels) into xBuf1, post-AF2 conv1d on features.
	for sf := range subframesPerFrame {
		base2 := sf * nolaceAF1OutChannels * subframeSize
		base1 := sf * nolaceAF2OutChannels * subframeSize

		// tdshape1: modifies the second channel (offset +subframeSize) in place.
		adashapeProcessFrame(
			s.tdshape1Alpha1FState[:], s.tdshape1Alpha1TState[:], s.tdshape1Alpha2State[:],
			&s.tdshape1InterpState,
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.TDShape1Alpha1F, &m.TDShape1Alpha1T, &m.TDShape1Alpha2,
			nolaceCondDim, subframeSize, nolaceTDShapeAvgPoolK, nolaceTDShapeInterpolK,
		)

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

		computeGenericConv1D(
			&m.PostAF2,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postAF2State[:],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh,
		)
	}
	copy(featureBuf[:], featureTransformBuf[:])

	// Second shape-mix round: tdshape2 on channel 1 of xBuf1, AF3 to xBuf2,
	// post-AF3 conv1d.
	for sf := range subframesPerFrame {
		base1 := sf * nolaceAF2OutChannels * subframeSize
		base2 := sf * nolaceAF3OutChannels * subframeSize

		adashapeProcessFrame(
			s.tdshape2Alpha1FState[:], s.tdshape2Alpha1TState[:], s.tdshape2Alpha2State[:],
			&s.tdshape2InterpState,
			xBuf1[base1+subframeSize:base1+2*subframeSize],
			xBuf1[base1+subframeSize:base1+2*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.TDShape2Alpha1F, &m.TDShape2Alpha1T, &m.TDShape2Alpha2,
			nolaceCondDim, subframeSize, nolaceTDShapeAvgPoolK, nolaceTDShapeInterpolK,
		)

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

		computeGenericConv1D(
			&m.PostAF3,
			featureTransformBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			s.postAF3State[:],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			nolaceCondDim, actTanh,
		)
	}
	copy(featureBuf[:], featureTransformBuf[:])

	// Third shape-mix round: tdshape3 on channel 1 of xBuf2, AF4 (2 -> 1) to xBuf1.
	for sf := range subframesPerFrame {
		base2 := sf * nolaceAF3OutChannels * subframeSize
		base1 := sf * nolaceAF4OutChannels * subframeSize

		adashapeProcessFrame(
			s.tdshape3Alpha1FState[:], s.tdshape3Alpha1TState[:], s.tdshape3Alpha2State[:],
			&s.tdshape3InterpState,
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			xBuf2[base2+subframeSize:base2+2*subframeSize],
			featureBuf[sf*nolaceCondDim:sf*nolaceCondDim+nolaceCondDim],
			&m.TDShape3Alpha1F, &m.TDShape3Alpha1T, &m.TDShape3Alpha2,
			nolaceCondDim, subframeSize, nolaceTDShapeAvgPoolK, nolaceTDShapeInterpolK,
		)

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

	// De-emphasis (xBuf1 channel 0 -> out).
	for i := range frame20msSize {
		out[i] = xBuf1[i] + nolacePreemph*s.deempMem
		s.deempMem = out[i]
	}
	return nil
}

// ensureWindow lazily computes the overlap window used by all AdaComb /
// AdaConv stages.
func (s *LACEState) ensureWindow() {
	if s.windowInit {
		return
	}
	computeOverlapWindow(s.window[:], laceOverlapSize)
	s.windowInit = true
}

func (s *NoLACEState) ensureWindow() {
	if s.windowInit {
		return
	}
	computeOverlapWindow(s.window[:], nolaceOverlapSize)
	s.windowInit = true
}

func validatePeriods(periods []int, maxPeriod int) error {
	if len(periods) < subframesPerFrame {
		return errLACEPeriods
	}
	for sf := range subframesPerFrame {
		if periods[sf] < 0 || periods[sf] > maxPeriod {
			return errLACEPeriod
		}
	}
	return nil
}

func computeOverlapWindow(window []float32, overlapSize int) {
	for i := range overlapSize {
		// libopus dnn/nndsp.c:compute_overlap_window evaluates cos(M_PI * ...)
		// as a C double expression, then stores the result in a float window.
		arg := math.Pi * float64(float32(i)+0.5) / float64(overlapSize)
		window[i] = float32(0.5 + 0.5*math.Cos(arg))
	}
}

func scaleKernelInvNorm(norm float32) float32 {
	// libopus dnn/nndsp.c:scale_kernel assigns
	// 1.f / (1e-6f + sqrt(norm)) back to float after sqrt()'s C double result.
	return float32(1.0 / (float64(float32(1e-6)) + math.Sqrt(float64(norm))))
}

func adashapeLeakyRelu(v float32) float32 {
	if v >= 0 {
		return v
	}
	// libopus dnn/nndsp.c:adashape_process_frame uses unsuffixed 0.2, so
	// the negative branch narrows only after the multiply.
	return float32(0.2 * float64(v))
}

// computeNumbitsEmbedding mirrors libopus
// `compute_{lace,nolace}_numbits_embedding`: an 8-element sin-of-scaled-log
// fingerprint of the encoder bit budget. The upstream OSCE CLIP macro only
// caps values above maxVal; values below minVal are left unchanged.
func computeNumbitsEmbedding(emb []float32, numbits, minVal, maxVal float32, scales [8]float32) {
	logN := opusmath.LogF32(numbits)
	c := logN
	if c > maxVal {
		c = maxVal
	}
	x := c - (maxVal+minVal)*0.5
	for i := range 8 {
		emb[i] = opusmath.SinF32(x*scales[i] - 0.5)
	}
}

// featureNet runs the libopus `lace_feature_net` forward path.
//
// Pipeline:
//
//	conv1 (per subframe): 173 -> 96
//	conv2 (single call):  4*96 = 384 -> 128 (with 384-sample history)
//	tconv (dense):        128 -> 4*128 = 512
//	GRU (4 iterations):   128 -> 128 (writes 4*128 = 512 latent features)
func (s *LACEState) featureNet(out, features []float32, numbits []float32, periods []int) {
	m := &s.model.LACE
	const condDim = laceCondDim
	const hiddenDim = laceHiddenFeatureDim
	const numFeat = laceNumFeatures
	const pitchEmbDim = lacePitchEmbeddingDim
	const numbitsEmbDim = laceNumbitsEmbeddingDim
	const concatDim = numFeat + pitchEmbDim + 2*numbitsEmbDim // 173

	var numbitsEmbedded [2 * numbitsEmbDim]float32
	low := opusmath.LogF32(laceNumbitsRangeLow)
	high := opusmath.LogF32(laceNumbitsRangeHigh)
	computeNumbitsEmbedding(numbitsEmbedded[:numbitsEmbDim], numbits[0], low, high, laceNumbitsScales)
	computeNumbitsEmbedding(numbitsEmbedded[numbitsEmbDim:], numbits[1], low, high, laceNumbitsScales)

	var conv1Out [subframesPerFrame * hiddenDim]float32
	var conv1In [concatDim]float32
	// Scaling and dimensionality reduction per subframe.
	for sf := range subframesPerFrame {
		copy(conv1In[:numFeat], features[sf*numFeat:sf*numFeat+numFeat])
		period := periods[sf]
		if !m.PitchEmbedding.FloatWeights.Empty() {
			for j := range pitchEmbDim {
				conv1In[numFeat+j] = m.PitchEmbedding.FloatWeights.At(period*pitchEmbDim + j)
			}
		}
		copy(conv1In[numFeat+pitchEmbDim:], numbitsEmbedded[:])

		computeGenericConv1D(
			&m.FNetConv1,
			conv1Out[sf*hiddenDim:sf*hiddenDim+hiddenDim],
			nil, // conv1 has no history (input_size == nb_inputs == 173).
			conv1In[:concatDim],
			concatDim, actTanh,
		)
	}

	// Subframe accumulation: a single conv2 call across all four subframes.
	const accInputSize = subframesPerFrame * hiddenDim // 384
	var conv2Out [condDim]float32
	computeGenericConv1D(
		&m.FNetConv2,
		conv2Out[:],
		s.fnetConv2State[:m.FNetConv2.NbInputs-accInputSize],
		conv1Out[:],
		accInputSize, actTanh,
	)

	// TConv (single dense layer expanding 128 -> 4*128).
	var tconvOut [subframesPerFrame * condDim]float32
	computeGenericDense(
		&m.FNetTConv,
		tconvOut[:],
		conv2Out[:],
		actTanh,
	)

	// GRU: one iteration per subframe, writing the new state as that
	// subframe's latent feature vector.
	for sf := range subframesPerFrame {
		computeGenericGRU(
			&m.FNetGRUInput, &m.FNetGRURecurrent,
			s.fnetGRUState[:],
			tconvOut[sf*condDim:sf*condDim+condDim],
		)
		copy(out[sf*condDim:sf*condDim+condDim], s.fnetGRUState[:])
	}
}

// featureNet runs the libopus `nolace_feature_net` forward path. Identical
// topology to LACE's feature net but uses NOLACE_COND_DIM=160 instead of 128.
func (s *NoLACEState) featureNet(out, features []float32, numbits []float32, periods []int) {
	m := &s.model.NoLACE
	const condDim = nolaceCondDim
	const hiddenDim = nolaceHiddenFeatureDim
	const numFeat = nolaceNumFeatures
	const pitchEmbDim = nolacePitchEmbeddingDim
	const numbitsEmbDim = nolaceNumbitsEmbeddingDim
	const concatDim = numFeat + pitchEmbDim + 2*numbitsEmbDim // 173

	var numbitsEmbedded [2 * numbitsEmbDim]float32
	low := opusmath.LogF32(nolaceNumbitsRangeLow)
	high := opusmath.LogF32(nolaceNumbitsRangeHigh)
	computeNumbitsEmbedding(numbitsEmbedded[:numbitsEmbDim], numbits[0], low, high, nolaceNumbitsScales)
	computeNumbitsEmbedding(numbitsEmbedded[numbitsEmbDim:], numbits[1], low, high, nolaceNumbitsScales)

	var conv1Out [subframesPerFrame * hiddenDim]float32
	var conv1In [concatDim]float32
	for sf := range subframesPerFrame {
		copy(conv1In[:numFeat], features[sf*numFeat:sf*numFeat+numFeat])
		period := periods[sf]
		if !m.PitchEmbedding.FloatWeights.Empty() {
			for j := range pitchEmbDim {
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

	const accInputSize = subframesPerFrame * hiddenDim // 384
	var conv2Out [condDim]float32
	computeGenericConv1D(
		&m.FNetConv2,
		conv2Out[:],
		s.fnetConv2State[:m.FNetConv2.NbInputs-accInputSize],
		conv1Out[:],
		accInputSize, actTanh,
	)

	var tconvOut [subframesPerFrame * condDim]float32
	computeGenericDense(
		&m.FNetTConv,
		tconvOut[:],
		conv2Out[:],
		actTanh,
	)

	for sf := range subframesPerFrame {
		computeGenericGRU(
			&m.FNetGRUInput, &m.FNetGRURecurrent,
			s.fnetGRUState[:],
			tconvOut[sf*condDim:sf*condDim+condDim],
		)
		copy(out[sf*condDim:sf*condDim+condDim], s.fnetGRUState[:])
	}
}

// ----------------------------------------------------------------------------
// Generic linear / conv / GRU helpers (mirrors libopus dnn/nnet.c).
// ----------------------------------------------------------------------------

// computeLinear evaluates a LinearLayer: out = W^T * in + bias. Mirrors
// libopus `compute_linear_c` (dnn/nnet_arch.h), which routes float weights
// through `sgemv` (dnn/vec.h).
func computeLinear(layer *LinearLayer, out, in []float32) {
	n := layer.NbOutputs
	m := layer.NbInputs
	switch {
	case !layer.FloatWeights.Empty():
		sgemvFloat(out[:n], layer.FloatWeights, n, m, in[:m])
	case !layer.Weights.Empty():
		cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m])
	default:
		for i := range n {
			out[i] = 0
		}
	}
	if !layer.Bias.Empty() {
		for i := range n {
			out[i] += layer.Bias.At(i)
		}
	}
}

// sgemvFloat mirrors libopus `sgemv` (dnn/vec.h), as inlined into
// `compute_linear_c` (dnn/nnet_arch.h). Whether each `out[i] += w*x` row is
// contracted into an FMA depends on the row count (see sgemvFused), so the
// fused vs rounded boundary is pinned to match the prebuilt OSCE object.
func sgemvFloat(out []float32, w dnnblob.Float32View, rows, cols int, x []float32) {
	fused := sgemvFused(rows)
	for i := range rows {
		var sum float32
		if fused {
			for j := range cols {
				sum += w.At(j*rows+i) * x[j]
			}
		} else {
			for j := range cols {
				sum += roundMul32(w.At(j*rows+i), x[j])
			}
		}
		out[i] = sum
	}
}

// sgemvFused reports whether libopus' compiled sgemv contracts the
// `out[i] += w*x` accumulation into an FMA for the given row count.
//
//   - rows % 8 == 0 (sgemv16x1 / sgemv8x1): 16/8 register accumulators, fused.
//   - rows == 1 (AdaComb/AdaConv single-output gain + global-gain layers): the
//     scalar fallback accumulates through a single `out[0]` cell that the
//     compiled object does NOT contract -- verified bit-for-bit (LACE stays a
//     bit-exact oracle only when this row stays rounded).
//   - rows == 2 (the NoLACE `nolace_af1_gain` 2-output gain layer): the
//     compiled scalar fallback emits fused FMADD for the two accumulators
//     (verified by disassembling the prebuilt `compute_linear_c` and against
//     the reference helper's AF1 gain, which is 2 ULP off without the fuse).
func sgemvFused(rows int) bool {
	return rows%8 == 0 || rows == 2
}

// cgemv8x4 mirrors the scalar fallback in libopus dnn/vec.h. Requires
// rows % 8 == 0 and cols % 4 == 0 (verified at model load time).
func cgemv8x4(out []float32, weights dnnblob.Int8View, scale dnnblob.Float32View, rows, cols int, x []float32) {
	const maxCols = 1024
	var q [maxCols]int8
	if cols > maxCols {
		for i := range rows {
			out[i] = 0
		}
		return
	}
	for i := range cols {
		q[i] = dnnmath.Cgemv8x4QuantizeInputScalar(x[i])
	}
	for i := range rows {
		out[i] = 0
	}
	wOffset := 0
	for row := 0; row < rows; row += 8 {
		var acc [8]int32
		for col := 0; col < cols; col += 4 {
			x0 := int32(q[col])
			x1 := int32(q[col+1])
			x2 := int32(q[col+2])
			x3 := int32(q[col+3])
			for r := range 8 {
				base := wOffset + r*4
				acc[r] += int32(weights.At(base))*x0 +
					int32(weights.At(base+1))*x1 +
					int32(weights.At(base+2))*x2 +
					int32(weights.At(base+3))*x3
			}
			wOffset += 32
		}
		for r := range 8 {
			out[row+r] = float32(acc[r]) * scale.At(row+r)
		}
	}
}

// computeActivation applies one of the libopus ACTIVATION_* nonlinearities in
// place / between two buffers.
func computeActivation(out, in []float32, n, activation int) {
	switch activation {
	case actLinear:
		if len(out) == 0 || len(in) == 0 || &out[0] != &in[0] {
			copy(out[:n], in[:n])
		}
	case actSigmoid:
		dnnmath.SigmoidVectorScalarApprox(out, in, n)
	case actTanh:
		dnnmath.TanhVectorScalarApprox(out, in, n)
	case actRelu:
		for i := range n {
			v := in[i]
			if v < 0 {
				v = 0
			}
			out[i] = v
		}
	case actSoftmax:
		dnnmath.SoftmaxApprox(out, in, n)
	case actExp:
		dnnmath.ExpVectorScalarApprox(out, in, n)
	default:
		copy(out[:n], in[:n])
	}
}

// computeGenericDense mirrors libopus `compute_generic_dense`.
func computeGenericDense(layer *LinearLayer, out, in []float32, activation int) {
	computeLinear(layer, out, in)
	computeActivation(out, out, layer.NbOutputs, activation)
}

// computeGenericConv1D mirrors libopus `compute_generic_conv1d`. The layer's
// nb_inputs accommodates the full kernel window; `mem` is the sliding history
// of size nb_inputs-input_size. Pass `mem == nil` for layers whose state size
// is zero (e.g. LACE FNetConv1, which uses input_size == nb_inputs).
func computeGenericConv1D(layer *LinearLayer, out, mem, in []float32, inputSize, activation int) {
	nbInputs := layer.NbInputs
	if nbInputs <= 0 {
		return
	}
	const maxTmp = 1024
	var tmp [maxTmp]float32
	if nbInputs > maxTmp {
		return
	}
	memLen := nbInputs - inputSize
	if memLen > 0 && mem != nil {
		copy(tmp[:memLen], mem[:memLen])
	}
	copy(tmp[memLen:nbInputs], in[:inputSize])
	computeLinear(layer, out, tmp[:nbInputs])
	computeActivation(out, out, layer.NbOutputs, activation)
	if memLen > 0 && mem != nil {
		copy(mem[:memLen], tmp[inputSize:nbInputs])
	}
}

// computeGenericGRU mirrors libopus `compute_generic_gru`.
func computeGenericGRU(inputW, recurrentW *LinearLayer, state, in []float32) {
	n := recurrentW.NbInputs
	const maxN = 256
	var zrh, recur [3 * maxN]float32
	if 3*n > 3*maxN {
		return
	}
	z := zrh[0:n]
	r := zrh[n : 2*n]
	h := zrh[2*n : 3*n]

	computeLinear(inputW, zrh[:3*n], in[:inputW.NbInputs])
	computeLinear(recurrentW, recur[:3*n], state[:n])
	for i := 0; i < 2*n; i++ {
		zrh[i] += recur[i]
	}
	computeActivation(zrh[:2*n], zrh[:2*n], 2*n, actSigmoid)
	for i := range n {
		h[i] = fma32(recur[2*n+i], r[i], h[i])
	}
	computeActivation(h, h, n, actTanh)
	for i := range n {
		// libopus dnn/nnet.c:compute_generic_gru: h[i] = z[i]*state[i] + (1-z[i])*h[i]
		// as one C statement. clang -ffp-contract=on contracts the leftmost
		// product z*state into an FMA while the trailing product (1-z)*h rounds
		// first. To match that rounding bit-for-bit, gruFMA32 pins the z*state
		// contraction (hardware FMADDS on arm64) and roundMul32 keeps (1-z)*h
		// rounded as the FMA addend.
		h[i] = gruFMA32(z[i], state[i], roundMul32(1-z[i], h[i]))
		state[i] = h[i]
	}
}

func fma32(a, b, c float32) float32 {
	return a*b + c
}

// round32 forces x to float32 precision. Go's arm64 backend may contract a*b+c
// into a single FMADD (one rounding), which diverges from scalar libopus (two
// roundings); wrapping the product as round32(a*b) materializes it at float32
// precision so the surrounding add/sub cannot fuse, matching the scalar
// reference on every build. It is the cheap barrier — an FMUL+FADD pair rather
// than the FMUL+FMOV+FMOV+FADD of a Float32bits round-trip — and a no-op on
// amd64 and the purego oracle, which do not contract FP. Keep this tiny; its
// fusion-defeating codegen is guarded by the package parity tests.
func round32(x float32) float32 {
	return float32(x)
}

// roundMul32 returns a*b rounded to float32 with the multiply isolated from any
// fused-multiply-add contraction by the Go arm64 backend, matching a reference
// build that does not contract this particular product.
func roundMul32(a, b float32) float32 {
	return round32(a * b)
}

// ----------------------------------------------------------------------------
// AdaComb (adaptive comb-filter) primitives -- mirrors libopus
// `adacomb_process_frame` from dnn/nndsp.c.
// ----------------------------------------------------------------------------

func adacombProcessFrame(
	history, lastKernel []float32, lastGlobalGain *float32, lastPitchLag *int,
	xOut, xIn, features []float32,
	kernelLayer, gainLayer, globalGainLayer *LinearLayer,
	pitchLag int,
	frameSize, overlapSize, kernelSize, leftPadding int,
	filterGainA, filterGainB, logGainLimit float32,
	window []float32,
) {
	var (
		outputBuf        [adaCombMaxFrameSize]float32
		outputBufLast    [adaCombMaxFrameSize]float32
		kernelBuf        [adaCombMaxKernelSize]float32
		inputBuf         [adaCombMaxFrameSize + adaCombMaxLag + adaCombMaxKernelSize]float32
		gain, globalGain float32
	)

	// Prepare input buffer: history (kernel_size + ADACOMB_MAX_LAG samples)
	// + new frame.
	copy(inputBuf[:kernelSize+adaCombMaxLag], history[:kernelSize+adaCombMaxLag])
	copy(inputBuf[kernelSize+adaCombMaxLag:], xIn[:frameSize])
	pInputOffset := kernelSize + adaCombMaxLag

	// Run kernel + gain layers (single-output dense layers).
	computeGenericDense(kernelLayer, kernelBuf[:kernelSize], features, actLinear)
	var gainTmp [1]float32
	computeGenericDense(gainLayer, gainTmp[:], features, actRelu)
	gain = gainTmp[0]
	var globalGainTmp [1]float32
	computeGenericDense(globalGainLayer, globalGainTmp[:], features, actTanh)
	globalGain = globalGainTmp[0]

	gain = opusmath.ExpF32(logGainLimit - gain)
	globalGain = opusmath.ExpF32(filterGainA*globalGain + filterGainB)

	// scale_kernel (1 in / 1 out): p-norm normalisation with gain.
	// libopus dnn/nndsp.c:scale_kernel accumulates `norm += kernel*kernel` in a
	// single statement, but its prebuilt object squares each tap with a plain
	// (rounded) multiply -- the compiler emits `fmul.4s` for the square, NOT a
	// fused multiply-add into the running sum (verified bit-for-bit against the
	// reference helper's scaled kernel). The Go arm64 backend would otherwise
	// fuse `norm += k*k` into FMADDS and drift the kernel norm by 1 ULP, which
	// the CF2/AF gain cascade then amplifies. roundMul32 isolates the square as
	// a rounded float32 so the running sum matches libopus.
	var norm float32
	for k := range kernelSize {
		norm += roundMul32(kernelBuf[k], kernelBuf[k])
	}
	invNorm := scaleKernelInvNorm(norm)
	scale := invNorm * gain
	for k := range kernelSize {
		kernelBuf[k] *= scale
	}

	// Output for last kernel over overlap_size samples (uses last_pitch_lag).
	lastK := *lastPitchLag
	for i := range overlapSize {
		var sum float32
		idx := pInputOffset + i - leftPadding - lastK
		for k := range kernelSize {
			sum += lastKernel[k] * inputBuf[idx+k]
		}
		outputBufLast[i] = sum
	}

	// Output for new kernel over frame_size samples (uses pitch_lag).
	for i := range frameSize {
		var sum float32
		idx := pInputOffset + i - leftPadding - pitchLag
		for k := range kernelSize {
			sum += kernelBuf[k] * inputBuf[idx+k]
		}
		outputBuf[i] = sum
	}

	// Overlap mix. libopus dnn/nndsp.c:adacomb_process_frame:
	//   output_buffer[i] = last_global_gain*window[i]*output_buffer_last[i]
	//                      + global_gain*(1-window[i])*output_buffer[i];
	// clang -ffp-contract=on contracts the leftmost product into the add while
	// the trailing product rounds; the Go arm64 backend contracts the trailing
	// one instead, so pin the libopus boundary with gruFMA32 + roundMul32.
	lastGlobalG := *lastGlobalGain
	for i := range overlapSize {
		outputBuf[i] = gruFMA32(
			roundMul32(lastGlobalG, window[i]), outputBufLast[i],
			roundMul32(roundMul32(globalGain, 1.0-window[i]), outputBuf[i]),
		)
	}

	// Add weighted input. libopus:
	//   output_buffer[i] += (window[i]*last_global_gain + (1-window[i])*global_gain) * p_input[i];
	// The inner weight is an a*b + c*d expression clang contracts on its left
	// product; pin that boundary so the Go arm64 backend does not contract the
	// trailing one instead.
	for i := range overlapSize {
		weight := gruFMA32(window[i], lastGlobalG, roundMul32(1.0-window[i], globalGain))
		outputBuf[i] += weight * inputBuf[pInputOffset+i]
	}
	for i := overlapSize; i < frameSize; i++ {
		outputBuf[i] = globalGain * (outputBuf[i] + inputBuf[pInputOffset+i])
	}
	copy(xOut[:frameSize], outputBuf[:frameSize])

	// Buffer update.
	copy(lastKernel[:kernelSize], kernelBuf[:kernelSize])
	copy(history[:kernelSize+adaCombMaxLag],
		inputBuf[frameSize+(kernelSize+adaCombMaxLag)-(kernelSize+adaCombMaxLag):frameSize+(kernelSize+adaCombMaxLag)])
	*lastPitchLag = pitchLag
	*lastGlobalGain = globalGain
}

// ----------------------------------------------------------------------------
// AdaConv (adaptive convolution) primitives -- mirrors libopus
// `adaconv_process_frame` from dnn/nndsp.c.
// ----------------------------------------------------------------------------

func adaconvProcessFrame(
	history, lastKernel []float32,
	xOut, xIn, features []float32,
	kernelLayer, gainLayer *LinearLayer,
	frameSize, overlapSize, inChannels, outChannels, kernelSize, leftPadding int,
	filterGainA, filterGainB, shapeGain float32,
	window []float32,
) {
	const maxKernel = 32
	const maxFrame = 240
	const maxIn = 3
	const maxOut = 3
	var (
		outputBuf [maxFrame * maxOut]float32
		kernelBuf [maxKernel * maxIn * maxOut]float32
		inputBuf  [maxIn * (maxFrame + maxKernel)]float32
		gainBuf   [maxOut]float32
	)
	_ = shapeGain

	// Prepare input: prepend per-channel history.
	for c := range inChannels {
		copy(inputBuf[c*(kernelSize+frameSize):c*(kernelSize+frameSize)+kernelSize],
			history[c*kernelSize:c*kernelSize+kernelSize])
		copy(inputBuf[c*(kernelSize+frameSize)+kernelSize:c*(kernelSize+frameSize)+kernelSize+frameSize],
			xIn[c*frameSize:c*frameSize+frameSize])
	}

	// Compute new kernel/gain.
	computeGenericDense(kernelLayer, kernelBuf[:inChannels*outChannels*kernelSize], features, actLinear)
	computeGenericDense(gainLayer, gainBuf[:outChannels], features, actTanh)

	// transform_gains.
	for i := range outChannels {
		gainBuf[i] = opusmath.ExpF32(filterGainA*gainBuf[i] + filterGainB)
	}

	// scale_kernel. As in the AdaComb path, libopus squares each tap with a
	// rounded multiply (the prebuilt object emits `fmul`, not a fused MAC into
	// the running norm), so roundMul32 keeps the Go arm64 backend from fusing
	// `norm += v*v` into FMADDS and drifting by 1 ULP.
	for o := range outChannels {
		var norm float32
		for ic := range inChannels {
			for k := range kernelSize {
				idx := (o*inChannels+ic)*kernelSize + k
				v := kernelBuf[idx]
				norm += roundMul32(v, v)
			}
		}
		invNorm := scaleKernelInvNorm(norm)
		scale := invNorm * gainBuf[o]
		for ic := range inChannels {
			for k := range kernelSize {
				idx := (o*inChannels+ic)*kernelSize + k
				kernelBuf[idx] *= scale
			}
		}
	}

	// Cross-correlation.
	for o := range outChannels {
		for i := range frameSize {
			outputBuf[o*frameSize+i] = 0
		}
		for ic := range inChannels {
			base := ic*(frameSize+kernelSize) + kernelSize
			kernelOld := lastKernel[(o*inChannels+ic)*kernelSize : (o*inChannels+ic)*kernelSize+kernelSize]
			kernelNew := kernelBuf[(o*inChannels+ic)*kernelSize : (o*inChannels+ic)*kernelSize+kernelSize]
			for i := range frameSize {
				var sumNew float32
				for k := range kernelSize {
					sumNew += kernelNew[k] * inputBuf[base+i+k-leftPadding]
				}
				if i < overlapSize {
					var sumOld float32
					for k := range kernelSize {
						sumOld += kernelOld[k] * inputBuf[base+i+k-leftPadding]
					}
					outputBuf[o*frameSize+i] += window[i] * sumOld
					outputBuf[o*frameSize+i] += (1.0 - window[i]) * sumNew
				} else {
					outputBuf[o*frameSize+i] += sumNew
				}
			}
		}
	}

	copy(xOut[:outChannels*frameSize], outputBuf[:outChannels*frameSize])

	// Buffer update.
	for c := range inChannels {
		copy(history[c*kernelSize:c*kernelSize+kernelSize],
			inputBuf[c*(frameSize+kernelSize)+frameSize:c*(frameSize+kernelSize)+frameSize+kernelSize])
	}
	copy(lastKernel[:kernelSize*inChannels*outChannels], kernelBuf[:kernelSize*inChannels*outChannels])
}

// ----------------------------------------------------------------------------
// AdaShape (adaptive shaping) primitives -- mirrors libopus
// `adashape_process_frame` from dnn/nndsp.c.
// ----------------------------------------------------------------------------

func adashapeProcessFrame(
	alpha1FState, alpha1TState, alpha2State []float32,
	interpState *float32,
	xOut, xIn, features []float32,
	alpha1F, alpha1T, alpha2 *LinearLayer,
	featureDim, frameSize, avgPoolK, interpolateK int,
) {
	const maxIn = 512
	const maxFrame = 240
	var (
		inBuf  [maxIn + maxFrame]float32
		outBuf [maxFrame]float32
		tmpBuf [maxFrame]float32
	)
	tenvSize := frameSize / avgPoolK
	hiddenDim := frameSize / interpolateK

	copy(inBuf[:featureDim], features[:featureDim])
	tenv := inBuf[featureDim : featureDim+tenvSize+1]
	for i := range tenv {
		tenv[i] = 0
	}

	var mean float32
	f := 1.0 / float32(avgPoolK)
	for i := range tenvSize {
		var sum float32
		for k := range avgPoolK {
			v := xIn[i*avgPoolK+k]
			if v < 0 {
				v = -v
			}
			sum += v
		}
		tenv[i] = dnnmath.CeltLog(sum*f + 1.52587890625e-05)
		mean += tenv[i]
	}
	mean /= float32(tenvSize)
	for i := range tenvSize {
		tenv[i] -= mean
	}
	tenv[tenvSize] = mean

	computeGenericConv1D(alpha1F, outBuf[:alpha1F.NbOutputs], alpha1FState, inBuf[:featureDim], featureDim, actLinear)
	computeGenericConv1D(alpha1T, tmpBuf[:alpha1T.NbOutputs], alpha1TState, tenv, tenvSize+1, actLinear)

	for i := range hiddenDim {
		v := outBuf[i] + tmpBuf[i]
		inBuf[i] = adashapeLeakyRelu(v)
	}

	computeGenericConv1D(alpha2, tmpBuf[:alpha2.NbOutputs], alpha2State, inBuf[:hiddenDim], hiddenDim, actLinear)

	for i := range hiddenDim {
		for k := range interpolateK {
			alpha := float32(k+1) / float32(interpolateK)
			outBuf[i*interpolateK+k] = alpha*tmpBuf[i] + (1.0-alpha)*(*interpState)
		}
		*interpState = tmpBuf[i]
	}

	dnnmath.ExpVectorScalarApprox(outBuf[:frameSize], outBuf[:frameSize], frameSize)
	for i := range frameSize {
		xOut[i] = outBuf[i] * xIn[i]
	}
}
