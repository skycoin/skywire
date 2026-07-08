package bwe

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// Per-frame and per-subframe dimensions for the libopus 1.6.1 BBWENet
// pipeline, copied verbatim from the dnn/bbwenet_data.h layer geometry and
// the dnn/nndsp.c adaconv/adashape constants.
const (
	// Sample-rate ratios. The BBWENet pipeline is a 3x upsampler:
	// 16 kHz lowband -> 48 kHz wideband.
	InputSampleRate  = 16000
	OutputSampleRate = 48000

	// libopus xq16_len is restricted to 160 (10 ms) or 320 (20 ms).
	frameSize16    = 80  // BBWENET_AF1_FRAME_SIZE / BBWENET_FRAME_SIZE16
	frameSize32    = 160 // BBWENET_AF2_FRAME_SIZE / BBWENET_TDSHAPE1_FRAME_SIZE
	frameSize48    = 240 // BBWENET_AF3_FRAME_SIZE / BBWENET_TDSHAPE2_FRAME_SIZE
	subframesPerFr = 2

	// AdaConv geometry (matches libopus header constants).
	af1KernelSize  = 16
	af1OverlapSize = 40
	af1InChannels  = 1
	af1OutChannels = 3
	af1LeftPad     = af1KernelSize - 1
	af1FilterGainA = 1.381551

	af2KernelSize  = 32
	af2OverlapSize = 80
	af2InChannels  = 3
	af2OutChannels = 3
	af2LeftPad     = af2KernelSize - 1
	af2FilterGainA = 1.381551

	af3KernelSize  = 16
	af3OverlapSize = 120
	af3InChannels  = 3
	af3OutChannels = 1
	af3LeftPad     = af3KernelSize - 1
	af3FilterGainA = 1.381551

	// TDShape geometry.
	tdshape1AvgPoolK  = 8
	tdshape1InterpolK = 2
	tdshape1HiddenDim = frameSize32 / tdshape1InterpolK // 80
	tdshape1TenvSize  = frameSize32 / tdshape1AvgPoolK  // 20

	tdshape2AvgPoolK  = 12
	tdshape2InterpolK = 2
	tdshape2HiddenDim = frameSize48 / tdshape2InterpolK // 120
	tdshape2TenvSize  = frameSize48 / tdshape2AvgPoolK  // 20

	// Resampler delays (match libopus).
	resampDelaySamples = 8
	outputDelaySamples = 21

	// AdaShape conv1d sliding-history sizes are nb_inputs - input_size of
	// the corresponding LinearLayer (= (kernel_size - 1) * input_size).
	tdshape1Alpha1FStateSize = 128 // CondDim * (kernel_size - 1)
	tdshape1Alpha1TStateSize = 21
	tdshape1Alpha2StateSize  = 80
	tdshape2Alpha1FStateSize = 128
	tdshape2Alpha1TStateSize = 21
	tdshape2Alpha2StateSize  = 120
)

// Activation enum mirrors libopus dnn/nnet.h ACTIVATION_* values.
const (
	actLinear  = 0
	actSigmoid = 1
	actTanh    = 2
	actRelu    = 3
	actSoftmax = 4
	actExp     = 6
)

// State carries the persistent BBWENet runtime state libopus keeps inside
// BBWENetState (dnn/osce_structs.h): the bound model plus the conv / GRU /
// tdshape working buffers and overlap windows reused across forward passes.
type State struct {
	model *Model

	// Feature net history (conv1 has kernel 3, conv2 has kernel 3 -> each
	// keeps 2 frames of history matching libopus
	// BBWENET_FNET_CONV{1,2}_STATE_SIZE = 2 * channel_size).
	fnetConv1State [2 * FeatureDim]float32
	fnetConv2State [2 * FNetConv1Out]float32
	fnetGRUState   [FNetGRUOut]float32

	// AdaConv recurrent state. last_kernel sizes are
	//   AF1: kernel_size * in_channels * out_channels = 16*1*3 = 48
	//   AF2: 32*3*3 = 288
	//   AF3: 16*3*1 = 48
	// history sizes: AF1: 16*1=16, AF2: 32*3=96, AF3: 16*3=48
	af1History    [af1KernelSize * af1InChannels]float32
	af1LastKernel [af1KernelSize * af1InChannels * af1OutChannels]float32
	af2History    [af2KernelSize * af2InChannels]float32
	af2LastKernel [af2KernelSize * af2InChannels * af2OutChannels]float32
	af3History    [af3KernelSize * af3InChannels]float32
	af3LastKernel [af3KernelSize * af3InChannels * af3OutChannels]float32

	// AdaShape conv1d histories. alpha1f has kernel 1 (no history needed in
	// time), alpha1t kernel 2 over a 21-sample tenv. alpha2 has kernel 1.
	tdshape1Alpha1FState [tdshape1Alpha1FStateSize]float32
	tdshape1Alpha1TState [tdshape1Alpha1TStateSize]float32
	tdshape1Alpha2State  [tdshape1Alpha2StateSize]float32
	tdshape1InterpState  float32
	tdshape2Alpha1FState [tdshape2Alpha1FStateSize]float32
	tdshape2Alpha1TState [tdshape2Alpha1TStateSize]float32
	tdshape2Alpha2State  [tdshape2Alpha2StateSize]float32
	tdshape2InterpState  float32

	// Resampler state for three channels (upsamp_2x + interpol_3_2).
	resampUpEven   [3][3]float32 // S_even
	resampUpOdd    [3][3]float32 // S_odd
	resampInterpol [3][resampDelaySamples]float32

	// Output delay buffer (BBWENET_OSCE_BWE_OUTPUT_DELAY = 21 int16 samples).
	outputDelay [outputDelaySamples]int16

	// Pre-computed Hann overlap windows for the three AdaConv stages.
	// Caching them on the state avoids re-computing on every Process call.
	window16Init bool
	window16     [af1OverlapSize]float32
	window32     [af2OverlapSize]float32
	window48     [af3OverlapSize]float32
}

// SetModel binds (or clears) the BBWENet model on the runtime state. Passing
// a nil blob clears the binding so subsequent Loaded() calls return false.
func (s *State) SetModel(blob *dnnblob.Blob) error {
	if s == nil {
		return errInvalidBWEModel
	}
	if blob == nil {
		s.model = nil
		return nil
	}
	model, err := LoadModel(blob)
	if err != nil {
		s.model = nil
		return err
	}
	s.model = model
	s.Reset()
	return nil
}

// Model returns the bound BBWENet model, or nil when the runtime has not yet
// been loaded with a valid weights blob.
func (s *State) Model() *Model {
	if s == nil {
		return nil
	}
	return s.model
}

// Loaded reports whether the BBWENet runtime has a valid model binding.
func (s *State) Loaded() bool {
	return s != nil && s.model != nil
}

// Reset clears the per-stream working buffers libopus zero-initialises in
// `reset_bbwenet_state`. The model binding survives so libopus-style reset
// semantics are preserved (USE_WEIGHTS_FILE lifetime).
func (s *State) Reset() {
	if s == nil {
		return
	}
	s.fnetConv1State = [2 * FeatureDim]float32{}
	s.fnetConv2State = [2 * FNetConv1Out]float32{}
	s.fnetGRUState = [FNetGRUOut]float32{}

	s.af1History = [af1KernelSize * af1InChannels]float32{}
	s.af1LastKernel = [af1KernelSize * af1InChannels * af1OutChannels]float32{}
	s.af2History = [af2KernelSize * af2InChannels]float32{}
	s.af2LastKernel = [af2KernelSize * af2InChannels * af2OutChannels]float32{}
	s.af3History = [af3KernelSize * af3InChannels]float32{}
	s.af3LastKernel = [af3KernelSize * af3InChannels * af3OutChannels]float32{}

	s.tdshape1Alpha1FState = [tdshape1Alpha1FStateSize]float32{}
	s.tdshape1Alpha1TState = [tdshape1Alpha1TStateSize]float32{}
	s.tdshape1Alpha2State = [tdshape1Alpha2StateSize]float32{}
	s.tdshape1InterpState = 0
	s.tdshape2Alpha1FState = [tdshape2Alpha1FStateSize]float32{}
	s.tdshape2Alpha1TState = [tdshape2Alpha1TStateSize]float32{}
	s.tdshape2Alpha2State = [tdshape2Alpha2StateSize]float32{}
	s.tdshape2InterpState = 0

	s.resampUpEven = [3][3]float32{}
	s.resampUpOdd = [3][3]float32{}
	s.resampInterpol = [3][resampDelaySamples]float32{}
	s.outputDelay = [outputDelaySamples]int16{}
}

// errBWERuntimeFrameSize reports an unsupported input length. The libopus
// reference only supports 160 (10 ms) or 320 (20 ms) at 16 kHz.
var errBWERuntimeFrameSize = errors.New("osce/bwe: unsupported input length (expected 160 or 320 samples @ 16 kHz)")

// errBWERuntimeOutBuf reports an out_buf too short for the upsampled signal.
var errBWERuntimeOutBuf = errors.New("osce/bwe: output buffer too short (need 3 * len(in))")

// errBWERuntimeFeatures reports a features slice of the wrong size.
var errBWERuntimeFeatures = errors.New("osce/bwe: invalid features length (expected num_frames * 114)")

// Process runs the BBWENet 16 kHz -> 48 kHz upsampler for one decoder frame.
// in16k must be 160 (10 ms) or 320 (20 ms) samples of normalised float32 PCM
// at 16 kHz; features must hold num_frames=len(in16k)/160 vectors of
// FeatureDim floats each. out48k must hold 3 * len(in16k) float32 samples.
//
// The forward pass mirrors libopus bbwe_feature_net / bbwenet_process_frames
// (dnn/osce.c): the feature net (conv1 / conv2 / tconv / GRU) produces the
// per-subframe conditioning, which drives three AdaConv stages, two AdaShape
// blocks, the interleaved upsamp_2x and interpol_3_2 resamplers and the Valin
// activation, in that order. Output agrees with libopus to within float32
// roundoff, gated by the BWE forward-pass parity tests.
func (s *State) Process(in16k, out48k, features []float32) error {
	if s == nil || s.model == nil {
		return errBWENoModel
	}
	if len(in16k) != 160 && len(in16k) != 320 {
		return errBWERuntimeFrameSize
	}
	if len(out48k) < 3*len(in16k) {
		return errBWERuntimeOutBuf
	}
	numFrames := len(in16k) / 160
	if len(features) < numFrames*FeatureDim {
		return errBWERuntimeFeatures
	}
	s.ensureWindows()

	numSubframes := 2 * numFrames

	// Feature net -> latent_features[num_subframes * CondDim].
	var latent [4 * CondDim]float32
	s.featureNet(latent[:numSubframes*CondDim], features[:numFrames*FeatureDim], numFrames)

	// Signal net buffers. Sized to fit the largest stage:
	//   af3 frame * af2 out_channels = 240 * 3 = 720 floats per subframe,
	//   max 4 subframes (for 20 ms input).
	var xBuf1 [4 * 3 * frameSize48]float32
	var xBuf2 [4 * 3 * frameSize48]float32

	// Stage 1: AF1 (per-subframe, 1->3 channels, 80 samples in -> 80 samples * 3 channels out).
	for i := range numSubframes {
		s.adaconvAF1(
			xBuf1[i*af1OutChannels*frameSize16:i*af1OutChannels*frameSize16+af1OutChannels*frameSize16],
			in16k[i*frameSize16:i*frameSize16+frameSize16],
			latent[i*CondDim:i*CondDim+CondDim],
		)
	}

	// Stage 2: 2x upsampling on each channel of xBuf1 -> xBuf2 (16k -> 32k),
	// then TDShape1 on channel 1 and Valin activation on channel 2.
	for i := range numSubframes {
		base1 := i * af1OutChannels * frameSize16
		base2 := i * af1OutChannels * frameSize32
		for c := range af1OutChannels {
			s.upsample2x(c,
				xBuf2[base2+c*frameSize32:base2+c*frameSize32+frameSize32],
				xBuf1[base1+c*frameSize16:base1+c*frameSize16+frameSize16],
			)
		}
		// TDShape1 on channel index 1 (in place).
		s.adashape1(
			xBuf2[base2+frameSize32:base2+2*frameSize32],
			xBuf2[base2+frameSize32:base2+2*frameSize32],
			latent[i*CondDim:i*CondDim+CondDim],
		)
		// Valin nonlinear activation on channel index 2 (in place).
		applyValinActivation(xBuf2[base2+2*frameSize32 : base2+3*frameSize32])
	}

	// Stage 3: AF2 mixing (3 -> 3 channels, 160 samples).
	for i := range numSubframes {
		s.adaconvAF2(
			xBuf1[i*af2OutChannels*frameSize32:i*af2OutChannels*frameSize32+af2OutChannels*frameSize32],
			xBuf2[i*af1OutChannels*frameSize32:i*af1OutChannels*frameSize32+af1OutChannels*frameSize32],
			latent[i*CondDim:i*CondDim+CondDim],
		)
	}

	// Stage 4: 1.5x interpolation on each channel (32k -> 48k), then TDShape2
	// on channel 1 and Valin activation on channel 2.
	for i := range numSubframes {
		base1 := i * af2OutChannels * frameSize32
		base2 := i * af2OutChannels * frameSize48
		for c := range af2OutChannels {
			s.interpol32(c,
				xBuf2[base2+c*frameSize48:base2+c*frameSize48+frameSize48],
				xBuf1[base1+c*frameSize32:base1+c*frameSize32+frameSize32],
			)
		}
		s.adashape2(
			xBuf2[base2+frameSize48:base2+2*frameSize48],
			xBuf2[base2+frameSize48:base2+2*frameSize48],
			latent[i*CondDim:i*CondDim+CondDim],
		)
		applyValinActivation(xBuf2[base2+2*frameSize48 : base2+3*frameSize48])
	}

	// Stage 5: AF3 final mixing (3 -> 1 channel, 240 samples).
	for i := range numSubframes {
		s.adaconvAF3(
			out48k[i*frameSize48:i*frameSize48+frameSize48],
			xBuf2[i*af3InChannels*frameSize48:i*af3InChannels*frameSize48+af3InChannels*frameSize48],
			latent[i*CondDim:i*CondDim+CondDim],
		)
	}

	return nil
}

// ProcessDelayed runs the BBWENet forward pass and then applies libopus'
// public osce_bwe wrapper semantics: 21 samples of int16-domain output delay
// plus int16 scaling/clipping. The output remains normalised float32 PCM for
// the Go decoder, but its samples mirror the libopus int16 wrapper boundary.
func (s *State) ProcessDelayed(in16k, out48k, features []float32) error {
	if err := s.Process(in16k, out48k, features); err != nil {
		return err
	}
	numOut := 3 * len(in16k)
	var nextDelay [outputDelaySamples]int16
	for i := range outputDelaySamples {
		nextDelay[i] = bweFloatToInt16(out48k[numOut-outputDelaySamples+i])
	}
	for i := numOut - outputDelaySamples - 1; i >= 0; i-- {
		out48k[i+outputDelaySamples] = float32(bweFloatToInt16(out48k[i])) * (1.0 / 32768.0)
	}
	for i := range outputDelaySamples {
		out48k[i] = float32(s.outputDelay[i]) * (1.0 / 32768.0)
	}
	s.outputDelay = nextDelay
	return nil
}

func bweFloatToInt16(x float32) int16 {
	return opusmath.Float32ToInt16OSCEOutputScale(x)
}

// errBWENoModel is returned by Process when no model is bound.
var errBWENoModel = errors.New("osce/bwe: no model bound")

// ensureWindows lazily computes the Hann overlap windows used by all three
// AdaConv stages (matches libopus compute_overlap_window).
func (s *State) ensureWindows() {
	if s.window16Init {
		return
	}
	computeOverlapWindow(s.window16[:], af1OverlapSize)
	computeOverlapWindow(s.window32[:], af2OverlapSize)
	computeOverlapWindow(s.window48[:], af3OverlapSize)
	s.window16Init = true
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

// featureNet runs the libopus bbwe_feature_net forward path.
func (s *State) featureNet(out, features []float32, numFrames int) {
	var inputBuf, outputBuf [4 * FNetGRUOut]float32

	// First conv layer (kernel size 3, input 114, output 128, tanh).
	for i := range numFrames {
		computeGenericConv1D(
			&s.model.FNetConv1,
			outputBuf[i*FNetConv1Out:i*FNetConv1Out+FNetConv1Out],
			s.fnetConv1State[:],
			features[i*FeatureDim:i*FeatureDim+FeatureDim],
			FeatureDim,
			actTanh,
		)
	}
	copy(inputBuf[:numFrames*FNetConv1Out], outputBuf[:numFrames*FNetConv1Out])

	// Second conv layer (kernel size 3, input 128, output 128, tanh).
	for i := range numFrames {
		computeGenericConv1D(
			&s.model.FNetConv2,
			outputBuf[i*FNetConv2Out:i*FNetConv2Out+FNetConv2Out],
			s.fnetConv2State[:],
			inputBuf[i*FNetConv1Out:i*FNetConv1Out+FNetConv1Out],
			FNetConv1Out,
			actTanh,
		)
	}
	copy(inputBuf[:numFrames*FNetConv2Out], outputBuf[:numFrames*FNetConv2Out])

	// Transpose conv via a single dense layer producing 2*128 samples per
	// frame (stride 2 expansion, tanh activation).
	for i := range numFrames {
		computeGenericDense(
			&s.model.FNetTConv,
			outputBuf[i*FNetTConvOut:i*FNetTConvOut+FNetTConvOut],
			inputBuf[i*FNetConv2Out:i*FNetConv2Out+FNetConv2Out],
			actTanh,
		)
	}
	copy(inputBuf[:numFrames*FNetTConvOut], outputBuf[:numFrames*FNetTConvOut])

	// GRU runs once per (sub)frame (stride 2 * num_frames). Output written
	// directly to the latent buffer.
	numSub := 2 * numFrames
	for i := range numSub {
		computeGenericGRU(
			&s.model.FNetGRUInput,
			&s.model.FNetGRURecurrent,
			s.fnetGRUState[:],
			inputBuf[i*FNetGRUOut:i*FNetGRUOut+FNetGRUOut],
		)
		copy(out[i*FNetGRUOut:i*FNetGRUOut+FNetGRUOut], s.fnetGRUState[:])
	}
}

// adaconvAF1 runs the first adaptive-conv stage (1 input channel -> 3 output
// channels, kernel size 16, frame size 80, overlap size 40).
func (s *State) adaconvAF1(xOut, xIn, features []float32) {
	adaconvProcessFrame(
		s.af1History[:], s.af1LastKernel[:],
		xOut, xIn, features,
		&s.model.AF1Kernel, &s.model.AF1Gain,
		frameSize16, af1OverlapSize, af1InChannels, af1OutChannels,
		af1KernelSize, af1LeftPad, af1FilterGainA, 0.0, 1.0,
		s.window16[:],
	)
}

// adaconvAF2 runs the second adaptive-conv stage (3 -> 3 channels,
// kernel 32, frame size 160, overlap 80).
func (s *State) adaconvAF2(xOut, xIn, features []float32) {
	adaconvProcessFrame(
		s.af2History[:], s.af2LastKernel[:],
		xOut, xIn, features,
		&s.model.AF2Kernel, &s.model.AF2Gain,
		frameSize32, af2OverlapSize, af2InChannels, af2OutChannels,
		af2KernelSize, af2LeftPad, af2FilterGainA, 0.0, 1.0,
		s.window32[:],
	)
}

// adaconvAF3 runs the third adaptive-conv stage (3 -> 1 channels,
// kernel 16, frame size 240, overlap 120).
func (s *State) adaconvAF3(xOut, xIn, features []float32) {
	adaconvProcessFrame(
		s.af3History[:], s.af3LastKernel[:],
		xOut, xIn, features,
		&s.model.AF3Kernel, &s.model.AF3Gain,
		frameSize48, af3OverlapSize, af3InChannels, af3OutChannels,
		af3KernelSize, af3LeftPad, af3FilterGainA, 0.0, 1.0,
		s.window48[:],
	)
}

// adashape1 runs the first time-domain shaping block (frame 160, avg_pool 8,
// interpolate_k 2).
func (s *State) adashape1(xOut, xIn, features []float32) {
	adashapeProcessFrame(
		s.tdshape1Alpha1FState[:], s.tdshape1Alpha1TState[:], s.tdshape1Alpha2State[:],
		&s.tdshape1InterpState,
		xOut, xIn, features,
		&s.model.TDShape1Alpha1F, &s.model.TDShape1Alpha1T, &s.model.TDShape1Alpha2,
		CondDim, frameSize32, tdshape1AvgPoolK, tdshape1InterpolK,
	)
}

// adashape2 runs the second time-domain shaping block (frame 240, avg_pool 12).
func (s *State) adashape2(xOut, xIn, features []float32) {
	adashapeProcessFrame(
		s.tdshape2Alpha1FState[:], s.tdshape2Alpha1TState[:], s.tdshape2Alpha2State[:],
		&s.tdshape2InterpState,
		xOut, xIn, features,
		&s.model.TDShape2Alpha1F, &s.model.TDShape2Alpha1T, &s.model.TDShape2Alpha2,
		CondDim, frameSize48, tdshape2AvgPoolK, tdshape2InterpolK,
	)
}

// upsample2x runs the 3-pass cascaded allpass HQ 2x upsampler from libopus
// `upsamp_2x` for a single channel.
func (s *State) upsample2x(ch int, xOut, xIn []float32) {
	sEven := &s.resampUpEven[ch]
	sOdd := &s.resampUpOdd[ch]
	const (
		he0 = float32(0.026641845703125)
		he1 = float32(0.228668212890625)
		he2 = float32(-0.4036407470703125)
		ho0 = float32(0.104583740234375)
		ho1 = float32(0.3932037353515625)
		ho2 = float32(-0.152496337890625)
	)
	for k := range xIn {
		x := xIn[k]

		// even sample, pass 1
		y := x - sEven[0]
		X := y * he0
		tmp1 := sEven[0] + X
		sEven[0] = x + X
		// pass 2
		y = tmp1 - sEven[1]
		X = y * he1
		tmp2 := sEven[1] + X
		sEven[1] = tmp1 + X
		// pass 3
		y = tmp2 - sEven[2]
		X = y * (1 + he2)
		tmp3 := sEven[2] + X
		sEven[2] = tmp2 + X
		xOut[2*k] = tmp3

		// odd sample, pass 1
		y = x - sOdd[0]
		X = y * ho0
		tmp1 = sOdd[0] + X
		sOdd[0] = x + X
		// pass 2
		y = tmp1 - sOdd[1]
		X = y * ho1
		tmp2 = sOdd[1] + X
		sOdd[1] = tmp1 + X
		// pass 3
		y = tmp2 - sOdd[2]
		X = y * (1 + ho2)
		tmp3 = sOdd[2] + X
		sOdd[2] = tmp2 + X
		xOut[2*k+1] = tmp3
	}
}

// frac_{01,17,09}_24 mirror the 24-phase interpolator taps in libopus
// `interpol_3_2`.
var (
	frac01_24 = [8]float32{0.00576782, -0.01831055, 0.01882935, 0.9328308,
		0.09143066, -0.04196167, 0.01296997, -0.00140381}
	frac17_24 = [8]float32{-3.14331055e-03, 2.73437500e-02, -1.06414795e-01, 3.64685059e-01,
		8.03863525e-01, -1.02233887e-01, 1.61437988e-02, -1.22070312e-04}
	frac09_24 = [8]float32{-0.00146484, 0.02313232, -0.12072754, 0.7315979,
		0.4621277, -0.12075806, 0.0295105, -0.00326538}
)

// interpol32 performs 3:2 polyphase interpolation, mirroring libopus
// `interpol_3_2`. Input is `numSamples` 32 kHz samples, output is
// `3/2 * numSamples` 48 kHz samples.
func (s *State) interpol32(ch int, xOut, xIn []float32) {
	numSamples := len(xIn)
	var buffer [3 * frameSize48]float32
	mem := &s.resampInterpol[ch]
	copy(buffer[:resampDelaySamples], mem[:])
	copy(buffer[resampDelaySamples:resampDelaySamples+numSamples], xIn)

	iOut := 0
	for k := 0; k < numSamples; k += 2 {
		var v0, v1, v2 float32
		for j := range 8 {
			v0 += buffer[k+j] * frac01_24[j]
			v1 += buffer[k+j] * frac17_24[j]
			v2 += buffer[k+1+j] * frac09_24[j]
		}
		xOut[iOut] = v0
		xOut[iOut+1] = v1
		xOut[iOut+2] = v2
		iOut += 3
	}
	copy(mem[:], buffer[numSamples:numSamples+resampDelaySamples])
}

// applyValinActivation mirrors libopus `apply_valin_activation`:
//
//	x[i] *= sin(log(|x[i]| + 1e-6)).
func applyValinActivation(x []float32) {
	for i := range x {
		v := x[i]
		if v < 0 {
			v = -v
		}
		y := dnnmath.CeltLog(v + 1e-6)
		x[i] *= dnnmath.CeltSin(y)
	}
}

// ----------------------------------------------------------------------------
// Generic linear/conv/gru helpers (mirrors libopus dnn/nnet.c).
// ----------------------------------------------------------------------------

// computeLinear evaluates a LinearLayer: out = W^T * in + bias. Mirrors
// libopus `compute_linear_c` (dnn/nnet_arch.h): the float-weight path runs
// `sgemv` (column-major W[j*N+i]), the int8 path runs `cgemv8x4` -- a packed
// 8x4 block kernel where weights are laid out as consecutive 32-element
// (8 rows by 4 cols) tiles iterating rows in groups of 8 then cols in groups
// of 4. The int8 path quantises the input to signed int8 via
// floor(0.5 + 127*x), accumulates as float multiplied by the int8 weight,
// then scales each output row by `scale[i]` (per-row dequantisation).
func computeLinear(layer *LinearLayer, out, in []float32) {
	n := layer.NbOutputs
	m := layer.NbInputs
	bias := layer.Bias
	switch {
	case !layer.FloatWeights.Empty():
		// Weight layout: rows = n outputs, cols = m inputs, col-major:
		// weight(row, col) = w[col*n + row]. Mirrors libopus sgemv layout.
		for i := range n {
			var sum float32
			for j := range m {
				sum += layer.FloatWeights.At(j*n+i) * in[j]
			}
			out[i] = sum
		}
	case !layer.Weights.Empty():
		// Quantised int8 path; mirrors libopus `cgemv8x4` from vec.h.
		// USE_SU_BIAS is only enabled on AVX/AVX2 builds; the pure-Go
		// reference matches the default scalar libopus build (signed
		// int8 input quantisation, plain `bias`).
		cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m])
	default:
		for i := range n {
			out[i] = 0
		}
	}
	if !bias.Empty() {
		for i := range n {
			out[i] += bias.At(i)
		}
	}
}

// EvaluateLayerInt8 runs the supplied LinearLayer using only its int8
// quantised weight path (cgemv8x4 + scale). Used by parity tests to verify
// the int8 kernel agrees with a reference computation on real libopus
// weights. Writes zeros to `out` when the layer has no int8 weights.
func EvaluateLayerInt8(layer *LinearLayer, out, in []float32) {
	if layer == nil || layer.Weights.Empty() {
		for i := range out {
			out[i] = 0
		}
		return
	}
	n := layer.NbOutputs
	m := layer.NbInputs
	cgemv8x4(out[:n], layer.Weights, layer.Scale, n, m, in[:m])
	if !layer.Bias.Empty() {
		for i := range n {
			out[i] += layer.Bias.At(i)
		}
	}
}

// cgemv8x4 mirrors the scalar fallback in libopus dnn/vec.h. The weight
// matrix is stored as a sequence of 8-row by 4-col tiles (32 int8 values per
// tile) iterating cols of 4 within each row-of-8, then rows of 8. After
// integer accumulation each row is multiplied by `scale[row]` to recover the
// float-domain product. The input is symmetrically quantised to int8 via
// floor(0.5 + 127*x) so the dynamic range matches the trained kernel.
//
// Requires rows % 8 == 0 and cols % 4 == 0 (verified at model load time by
// the libopus 1.6.1 BBWENet layer dimensions: every int8 layer satisfies
// these constraints).
func cgemv8x4(out []float32, weights dnnblob.Int8View, scale dnnblob.Float32View, rows, cols int, x []float32) {
	const maxCols = 512
	var q [maxCols]int8
	if cols > maxCols {
		// Should never happen for BBWENet (max int8 cols is 384).
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
// nb_inputs accommodates the full kernel window (e.g. ksize=3, input_size=114
// -> 342 inputs); `mem` is the sliding history of size nb_inputs-input_size.
func computeGenericConv1D(layer *LinearLayer, out, mem, in []float32, inputSize, activation int) {
	nbInputs := layer.NbInputs
	var tmp [1024]float32
	if nbInputs > 1024 {
		// Caller passes already-sized layers; this fallback prevents an OOB.
		return
	}
	if nbInputs != inputSize {
		copy(tmp[:nbInputs-inputSize], mem[:nbInputs-inputSize])
	}
	copy(tmp[nbInputs-inputSize:nbInputs], in[:inputSize])
	computeLinear(layer, out, tmp[:nbInputs])
	computeActivation(out, out, layer.NbOutputs, activation)
	if nbInputs != inputSize {
		copy(mem[:nbInputs-inputSize], tmp[inputSize:nbInputs])
	}
}

// computeGenericGRU mirrors libopus `compute_generic_gru`.
func computeGenericGRU(inputW, recurrentW *LinearLayer, state, in []float32) {
	n := recurrentW.NbInputs
	var zrh, recur [3 * FNetGRUOut]float32
	if 3*n > 3*FNetGRUOut {
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
		h[i] += recur[2*n+i] * r[i]
	}
	computeActivation(h, h, n, actTanh)
	for i := range n {
		h[i] = z[i]*state[i] + (1-z[i])*h[i]
		state[i] = h[i]
	}
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
	var outputBuf [maxFrame * maxOut]float32
	var kernelBuf [maxKernel * maxIn * maxOut]float32
	var inputBuf [maxIn * (maxFrame + maxKernel)]float32
	var gainBuf [maxOut]float32

	_ = shapeGain // libopus only supports shape_gain == 1, no actual scaling

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

	// transform_gains: gain = exp(a*g + b).
	for i := range outChannels {
		gainBuf[i] = opusmath.ExpF32(filterGainA*gainBuf[i] + filterGainB)
	}

	// scale_kernel: p-norm normalise and apply gain per output channel.
	for o := range outChannels {
		var norm float32
		for ic := range inChannels {
			for k := range kernelSize {
				idx := (o*inChannels+ic)*kernelSize + k
				v := kernelBuf[idx]
				norm += v * v
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

	// Compute output: cross-correlation of kernel0/kernel1 with the input
	// signal. For sample i: y[i] = sum_k kernel[k] * input[i + k - leftPadding].
	for o := range outChannels {
		for ic := range inChannels {
			// pInput points into inputBuf at p_input[i_in_channels * (frame_size+kernel_size)] +
			// kernel_size (the start of the new frame). Apply left padding.
			base := ic*(frameSize+kernelSize) + kernelSize
			// Old kernel (kernel0 from previous frame's last_kernel).
			kernelOld := lastKernel[(o*inChannels+ic)*kernelSize : (o*inChannels+ic)*kernelSize+kernelSize]
			kernelNew := kernelBuf[(o*inChannels+ic)*kernelSize : (o*inChannels+ic)*kernelSize+kernelSize]
			for i := range frameSize {
				// Compute new-frame contribution.
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

	// Output.
	copy(xOut[:outChannels*frameSize], outputBuf[:outChannels*frameSize])

	// Buffer update.
	for c := range inChannels {
		copy(history[c*kernelSize:c*kernelSize+kernelSize],
			inputBuf[c*(frameSize+kernelSize)+frameSize:c*(frameSize+kernelSize)+frameSize+kernelSize])
	}
	copy(lastKernel[:kernelSize*inChannels*outChannels], kernelBuf[:kernelSize*inChannels*outChannels])
}

// adashapeProcessFrame mirrors libopus `adashape_process_frame` from
// dnn/nndsp.c.
func adashapeProcessFrame(
	alpha1FState, alpha1TState, alpha2State []float32,
	interpState *float32,
	xOut, xIn, features []float32,
	alpha1F, alpha1T, alpha2 *LinearLayer,
	featureDim, frameSize, avgPoolK, interpolateK int,
) {
	const maxIn = 512
	const maxFrame = 240
	var inBuf [maxIn + maxFrame]float32
	var outBuf [maxFrame]float32
	var tmpBuf [maxFrame]float32
	tenvSize := frameSize / avgPoolK
	hiddenDim := frameSize / interpolateK

	copy(inBuf[:featureDim], features[:featureDim])
	tenv := inBuf[featureDim : featureDim+tenvSize+1]
	for i := range tenv {
		tenv[i] = 0
	}

	// Temporal envelope.
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

	// alpha1: combine feature-domain conv + time-domain conv.
	computeGenericConv1D(alpha1F, outBuf[:alpha1F.NbOutputs], alpha1FState, inBuf[:featureDim], featureDim, actLinear)
	computeGenericConv1D(alpha1T, tmpBuf[:alpha1T.NbOutputs], alpha1TState, tenv, tenvSize+1, actLinear)

	// Leaky ReLU(out + tmp), slope 0.2.
	for i := range hiddenDim {
		v := outBuf[i] + tmpBuf[i]
		inBuf[i] = adashapeLeakyRelu(v)
	}

	// alpha2: hidden -> hidden.
	computeGenericConv1D(alpha2, tmpBuf[:alpha2.NbOutputs], alpha2State, inBuf[:hiddenDim], hiddenDim, actLinear)

	// Upsample by linear interpolation (interpolate_k taps).
	for i := range hiddenDim {
		for k := range interpolateK {
			alpha := float32(k+1) / float32(interpolateK)
			outBuf[i*interpolateK+k] = alpha*tmpBuf[i] + (1.0-alpha)*(*interpState)
		}
		*interpState = tmpBuf[i]
	}

	// Apply exp activation in place, then modulate.
	dnnmath.ExpVectorScalarApprox(outBuf[:frameSize], outBuf[:frameSize], frameSize)
	for i := range frameSize {
		xOut[i] = outBuf[i] * xIn[i]
	}
}
