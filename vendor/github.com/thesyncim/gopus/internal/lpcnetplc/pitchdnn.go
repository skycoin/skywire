package lpcnetplc

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/opusmath"
)

const (
	pitchIFMaxFreq           = 30
	pitchIFFeatures          = 3*pitchIFMaxFreq - 2
	pitchXcorrFeatures       = PitchMaxPeriod - PitchMinPeriod
	pitchDenseIF1OutSize     = 64
	pitchDenseIF2OutSize     = 64
	pitchDenseDownsamplerIn  = pitchXcorrFeatures + pitchDenseIF2OutSize
	pitchDenseDownsamplerOut = 64
	pitchDenseFinalOutSize   = 192
	pitchGRUStateSize        = 64
	pitchConvKernelTime      = 3
	pitchConvKernelHeight    = 3
	pitchConvPaddedStride    = pitchXcorrFeatures + pitchConvKernelHeight - 1
	pitchConv1InChannels     = 1
	pitchConv1OutChannels    = 4
	pitchConv2InChannels     = 4
	pitchConv2OutChannels    = 1
	pitchConv1MemSize        = (pitchConvKernelTime - 1) * pitchConv1InChannels * pitchConvPaddedStride
	pitchConv2MemSize        = (pitchConvKernelTime - 1) * pitchConv2InChannels * pitchConvPaddedStride
	pitchConv2StateSize      = (pitchXcorrFeatures + 2) * 2 * 8
	pitchConv2DMaxInputs     = pitchConvKernelTime * pitchConv2InChannels * pitchConvPaddedStride
	pitchConvScratchWidth    = pitchConvPaddedStride * 8
	pitchPitchClassCount     = 180
)

var errInvalidPitchDNNModel = errors.New("lpcnetplc: invalid pitchdnn model")

// Conv2DLayer is the typed Go projection of one libopus Conv2dLayer weight
// bundle used by PitchDNN's cross-correlation conv stages. Weights are always
// float32; KTime and KHeight are the kernel extents over the time and feature
// axes, matching conv2d_init in dnn/nnet.h.
type Conv2DLayer struct {
	Bias         dnnblob.Float32View
	FloatWeights dnnblob.Float32View
	InChannels   int
	OutChannels  int
	KTime        int
	KHeight      int
}

// Conv2DLayerSpec names the blob records and dimensions for one Conv2DLayer,
// mirroring the libopus conv2d_init argument tuple.
type Conv2DLayerSpec struct {
	Name         string
	Bias         string
	FloatWeights string
	InChannels   int
	OutChannels  int
	KTime        int
	KHeight      int
}

// PitchDNNModel holds the PitchDNN weight layers: two dense IF-upsamplers, two
// conv2d cross-correlation stages, a downsampler, a GRU (input + recurrent) and
// a final dense layer. Field order and the bound record names mirror the
// PitchDNN struct and init_pitchdnn in dnn/pitchdnn_data.{h,c}.
type PitchDNNModel struct {
	DenseIFUpsampler1 LinearLayer
	DenseIFUpsampler2 LinearLayer
	DenseDownsampler  LinearLayer
	DenseFinal        LinearLayer
	Conv1             Conv2DLayer
	Conv2             Conv2DLayer
	GRUInput          LinearLayer
	GRURecurrent      LinearLayer
}

type pitchDNNState struct {
	gruState  [pitchGRUStateSize]float32
	xcorrMem1 [pitchConv1MemSize]float32
	xcorrMem2 [pitchConv2StateSize]float32
	xcorrMem3 [pitchConv2StateSize]float32
}

type pitchDNNScratch struct {
	base        predictorScratch
	if1Out      [pitchDenseIF1OutSize]float32
	downsampler [pitchDenseDownsamplerIn]float32
	downOut     [pitchDenseDownsamplerOut]float32
	conv1Tmp1   [pitchConvScratchWidth]float32
	conv1Tmp2   [pitchConvScratchWidth]float32
	output      [pitchDenseFinalOutSize]float32
	conv2dInBuf [pitchConv2DMaxInputs]float32
}

// PitchDNN mirrors libopus PitchDNNState and keeps all recurrent state/scratch
// caller-owned so repeated analysis stays allocation-free.
type PitchDNN struct {
	model   *PitchDNNModel
	state   pitchDNNState
	scratch pitchDNNScratch
}

var pitchDNNLinearLayerSpecs = []LinearLayerSpec{
	{
		Name:         "dense_if_upsampler_1",
		Bias:         "dense_if_upsampler_1_bias",
		Subias:       "dense_if_upsampler_1_subias",
		Weights:      "dense_if_upsampler_1_weights_int8",
		FloatWeights: "dense_if_upsampler_1_weights_float",
		Scale:        "dense_if_upsampler_1_scale",
		NbInputs:     pitchIFFeatures,
		NbOutputs:    pitchDenseIF1OutSize,
	},
	{
		Name:         "dense_if_upsampler_2",
		Bias:         "dense_if_upsampler_2_bias",
		Subias:       "dense_if_upsampler_2_subias",
		Weights:      "dense_if_upsampler_2_weights_int8",
		FloatWeights: "dense_if_upsampler_2_weights_float",
		Scale:        "dense_if_upsampler_2_scale",
		NbInputs:     pitchDenseIF1OutSize,
		NbOutputs:    pitchDenseIF2OutSize,
	},
	{
		Name:         "dense_downsampler",
		Bias:         "dense_downsampler_bias",
		Subias:       "dense_downsampler_subias",
		Weights:      "dense_downsampler_weights_int8",
		FloatWeights: "dense_downsampler_weights_float",
		Scale:        "dense_downsampler_scale",
		NbInputs:     pitchDenseDownsamplerIn,
		NbOutputs:    pitchDenseDownsamplerOut,
	},
	{
		Name:         "dense_final_upsampler",
		Bias:         "dense_final_upsampler_bias",
		Subias:       "dense_final_upsampler_subias",
		Weights:      "dense_final_upsampler_weights_int8",
		FloatWeights: "dense_final_upsampler_weights_float",
		Scale:        "dense_final_upsampler_scale",
		NbInputs:     pitchDenseDownsamplerOut,
		NbOutputs:    pitchDenseFinalOutSize,
	},
	{
		Name:         "gru_1_input",
		Bias:         "gru_1_input_bias",
		Subias:       "gru_1_input_subias",
		Weights:      "gru_1_input_weights_int8",
		FloatWeights: "gru_1_input_weights_float",
		Scale:        "gru_1_input_scale",
		NbInputs:     pitchDenseDownsamplerOut,
		NbOutputs:    3 * pitchGRUStateSize,
	},
	{
		Name:         "gru_1_recurrent",
		Bias:         "gru_1_recurrent_bias",
		Subias:       "gru_1_recurrent_subias",
		Weights:      "gru_1_recurrent_weights_int8",
		FloatWeights: "gru_1_recurrent_weights_float",
		Scale:        "gru_1_recurrent_scale",
		NbInputs:     pitchGRUStateSize,
		NbOutputs:    3 * pitchGRUStateSize,
	},
}

var pitchDNNConv2DLayerSpecs = []Conv2DLayerSpec{
	{
		Name:         "conv2d_1",
		Bias:         "conv2d_1_bias",
		FloatWeights: "conv2d_1_weight_float",
		InChannels:   pitchConv1InChannels,
		OutChannels:  pitchConv1OutChannels,
		KTime:        pitchConvKernelTime,
		KHeight:      pitchConvKernelHeight,
	},
	{
		Name:         "conv2d_2",
		Bias:         "conv2d_2_bias",
		FloatWeights: "conv2d_2_weight_float",
		InChannels:   pitchConv2InChannels,
		OutChannels:  pitchConv2OutChannels,
		KTime:        pitchConvKernelTime,
		KHeight:      pitchConvKernelHeight,
	},
}

// PitchDNNLinearLayerSpecs returns the libopus-shaped dense/GRU layer specs
// the pure-Go pitch runtime binds from a validated blob.
func PitchDNNLinearLayerSpecs() []LinearLayerSpec {
	return pitchDNNLinearLayerSpecs
}

// PitchDNNConv2DLayerSpecs returns the libopus-shaped conv layer specs the
// pure-Go pitch runtime binds from a validated blob.
func PitchDNNConv2DLayerSpecs() []Conv2DLayerSpec {
	return pitchDNNConv2DLayerSpecs
}

// LoadPitchDNNModel binds the libopus pitchdnn model family into reusable Go
// layers.
func LoadPitchDNNModel(blob *dnnblob.Blob) (*PitchDNNModel, error) {
	if blob == nil || !blob.SupportsPitchDNN() {
		return nil, errInvalidPitchDNNModel
	}
	var model PitchDNNModel
	for _, spec := range pitchDNNLinearLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidPitchDNNModel
		}
		switch spec.Name {
		case "dense_if_upsampler_1":
			model.DenseIFUpsampler1 = layer
		case "dense_if_upsampler_2":
			model.DenseIFUpsampler2 = layer
		case "dense_downsampler":
			model.DenseDownsampler = layer
		case "dense_final_upsampler":
			model.DenseFinal = layer
		case "gru_1_input":
			model.GRUInput = layer
		case "gru_1_recurrent":
			model.GRURecurrent = layer
		}
	}
	for _, spec := range pitchDNNConv2DLayerSpecs {
		layer, err := loadConv2DLayer(blob, spec)
		if err != nil {
			return nil, errInvalidPitchDNNModel
		}
		switch spec.Name {
		case "conv2d_1":
			model.Conv1 = layer
		case "conv2d_2":
			model.Conv2 = layer
		}
	}
	return &model, nil
}

// SetModel binds a validated pitch model family and clears recurrent state.
func (p *PitchDNN) SetModel(blob *dnnblob.Blob) error {
	model, err := LoadPitchDNNModel(blob)
	if err != nil {
		p.model = nil
		p.Reset()
		return err
	}
	p.model = model
	p.Reset()
	return nil
}

// SetModelPreservingState replaces the bound model without clearing recurrent
// state, matching libopus USE_WEIGHTS_FILE reloads.
func (p *PitchDNN) SetModelPreservingState(blob *dnnblob.Blob) error {
	model, err := LoadPitchDNNModel(blob)
	if err != nil {
		return err
	}
	p.model = model
	return nil
}

// Loaded reports whether a pitch model is currently retained.
func (p *PitchDNN) Loaded() bool {
	return p != nil && p.model != nil
}

// Reset clears recurrent pitch state while preserving the bound model.
func (p *PitchDNN) Reset() {
	if p == nil {
		return
	}
	p.state = pitchDNNState{}
}

// Compute mirrors libopus compute_pitchdnn() and returns the normalized pitch
// class value used by LPCNet analysis.
func (p *PitchDNN) Compute(ifFeatures, xcorrFeatures []float32) float32 {
	if p == nil || p.model == nil || len(ifFeatures) < pitchIFFeatures || len(xcorrFeatures) < pitchXcorrFeatures {
		return 0
	}

	clear(p.scratch.downsampler[:])
	clear(p.scratch.conv1Tmp1[:])
	clear(p.scratch.conv1Tmp2[:])
	computeGenericDense(&p.model.DenseIFUpsampler1, p.scratch.if1Out[:], ifFeatures[:pitchIFFeatures], activationTanh, &p.scratch.base)
	computeGenericDense(&p.model.DenseIFUpsampler2, p.scratch.downsampler[pitchXcorrFeatures:], p.scratch.if1Out[:], activationTanh, &p.scratch.base)

	copy(p.scratch.conv1Tmp1[1:1+pitchXcorrFeatures], xcorrFeatures[:pitchXcorrFeatures])
	computeConv2D(&p.model.Conv1, p.scratch.conv1Tmp2[1:], p.state.xcorrMem1[:], p.scratch.conv1Tmp1[:pitchConvPaddedStride], pitchXcorrFeatures, pitchConvPaddedStride, activationTanh, &p.scratch)
	computeConv2D(&p.model.Conv2, p.scratch.downsampler[:pitchXcorrFeatures], p.state.xcorrMem2[:], p.scratch.conv1Tmp2[:pitchConv1OutChannels*pitchConvPaddedStride], pitchXcorrFeatures, pitchXcorrFeatures, activationTanh, &p.scratch)

	computeGenericDense(&p.model.DenseDownsampler, p.scratch.downOut[:], p.scratch.downsampler[:], activationTanh, &p.scratch.base)
	computeGenericGRU(&p.model.GRUInput, &p.model.GRURecurrent, p.state.gruState[:], p.scratch.downOut[:], &p.scratch.base)
	computeGenericDense(&p.model.DenseFinal, p.scratch.output[:], p.state.gruState[:], activationLinear, &p.scratch.base)

	pos := 0
	maxVal := float32(-1)
	for i := range pitchPitchClassCount {
		if p.scratch.output[i] > maxVal {
			pos = i
			maxVal = p.scratch.output[i]
		}
	}
	start := maxInt(0, pos-2)
	end := minInt(pitchPitchClassCount-1, pos+2)
	var sum float32
	var count float32
	// libopus dnn/pitchdnn.c:compute_pitchdnn accumulates "sum += p*i" and
	// "count += p" over the [pos-2,pos+2] window. On arm64 the pinned scalar
	// reference is built with clang -ffp-contract=on; disassembly of
	// compute_pitchdnn shows the loop unrolled by 4, where the leading groups of
	// four iterations compute each p*i as a separate rounded FMUL followed by a
	// plain FADD (the products are NOT fused into the accumulator), while the
	// scalar remainder iterations fuse "sum = fmadd(p, i, sum)". Go's arm64
	// backend would otherwise contract "sum += p*float32(i)" into an FMADD, so
	// force the rounded product with noFMA32Mul for the unrolled groups and use
	// the explicit fused fma32 only for the remainder.
	n := end - start + 1
	unrolled := start + (n/4)*4
	i := start
	for ; i < unrolled; i++ {
		v := opusmath.ExpF32(p.scratch.output[i])
		sum += noFMA32Mul(v, float32(i))
		count += v
	}
	for ; i <= end; i++ {
		v := opusmath.ExpF32(p.scratch.output[i])
		sum = fma32(v, float32(i), sum)
		count += v
	}
	if count == 0 {
		return 0
	}
	return (float32(1.0)/60)*(sum/count) - 1.5
}

func loadConv2DLayer(blob *dnnblob.Blob, spec Conv2DLayerSpec) (Conv2DLayer, error) {
	if blob == nil {
		return Conv2DLayer{}, errInvalidPitchDNNModel
	}
	layer := Conv2DLayer{
		InChannels:  spec.InChannels,
		OutChannels: spec.OutChannels,
		KTime:       spec.KTime,
		KHeight:     spec.KHeight,
	}
	var err error
	if spec.Bias != "" {
		layer.Bias, err = loadFloatRecord(blob, spec.Bias, spec.OutChannels)
		if err != nil {
			return Conv2DLayer{}, err
		}
	}
	count := spec.InChannels * spec.OutChannels * spec.KTime * spec.KHeight
	layer.FloatWeights, err = loadFloatRecord(blob, spec.FloatWeights, count)
	if err != nil {
		return Conv2DLayer{}, err
	}
	return layer, nil
}

func computeConv2D(layer *Conv2DLayer, out, mem, in []float32, height, hstride, activation int, scratch *pitchDNNScratch) {
	if layer == nil || layer.FloatWeights.Empty() || height <= 0 || hstride < height {
		return
	}
	timeStride := layer.InChannels * (height + layer.KHeight - 1)
	memSize := (layer.KTime - 1) * timeStride
	if len(mem) < memSize || len(in) < timeStride || len(out) < layer.OutChannels*hstride {
		return
	}
	inBuf := scratch.conv2dInBuf[:layer.KTime*timeStride]
	copy(inBuf[:memSize], mem[:memSize])
	copy(inBuf[memSize:memSize+timeStride], in[:timeStride])
	copy(mem[:memSize], inBuf[timeStride:timeStride+memSize])
	conv2D3x3Float(out, layer, inBuf, height, hstride)
	if !layer.Bias.Empty() {
		for i := 0; i < layer.OutChannels; i++ {
			base := i * hstride
			bias := layer.Bias.At(i)
			for j := range height {
				out[base+j] += bias
			}
		}
	}
	for i := 0; i < layer.OutChannels; i++ {
		base := i * hstride
		computeActivation(out[base:base+height], out[base:base+height], height, activation)
	}
}

func conv2D3x3Float(out []float32, layer *Conv2DLayer, in []float32, height, hstride int) {
	inStride := height + layer.KHeight - 1
	weights := layer.FloatWeights
	for i := 0; i < layer.OutChannels; i++ {
		baseOut := i * hstride
		clear(out[baseOut : baseOut+height])
		for m := 0; m < layer.InChannels; m++ {
			wBase := i*layer.InChannels*layer.KTime*layer.KHeight + m*layer.KTime*layer.KHeight
			in0 := 0*layer.InChannels*inStride + m*inStride
			in1 := 1*layer.InChannels*inStride + m*inStride
			in2 := 2*layer.InChannels*inStride + m*inStride
			w00 := weights.At(wBase + 0)
			w01 := weights.At(wBase + 1)
			w02 := weights.At(wBase + 2)
			w10 := weights.At(wBase + 3)
			w11 := weights.At(wBase + 4)
			w12 := weights.At(wBase + 5)
			w20 := weights.At(wBase + 6)
			w21 := weights.At(wBase + 7)
			w22 := weights.At(wBase + 8)
			for j := range height {
				out[baseOut+j] += conv3x3Acc9(
					w00, w01, w02, w10, w11, w12, w20, w21, w22,
					in[in0+j+0], in[in0+j+1], in[in0+j+2],
					in[in1+j+0], in[in1+j+1], in[in1+j+2],
					in[in2+j+0], in[in2+j+1], in[in2+j+2])
			}
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
