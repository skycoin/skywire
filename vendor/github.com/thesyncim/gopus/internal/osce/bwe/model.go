// Package bwe implements the libopus OSCE blind-bandwidth-extension network
// (BBWENet), which extends 16 kHz decoded speech up to 48 kHz.
//
// LoadModel binds the model weights from a validated dnnblob.Blob into typed
// Go layers. The model dimensions and required weight-record names mirror
// libopus 1.6.1 dnn/bbwenet_data.{h,c} and dnn/nndsp.c.
//
// State.Process runs the BBWENet forward pass from libopus dnn/osce.c: the
// feature network produces per-subframe latent conditioning vectors, which
// drive a cascade of adaptive-convolution (AF1/AF2/AF3) and adaptive-shaping
// (TDShape) stages plus learned upsampling, transforming a 16 kHz input frame
// into a 48 kHz output frame.
package bwe

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// Layer dimensions are copied verbatim from libopus
// `dnn/bbwenet_data.h` and the `init_bbwenetlayers` call sites in
// `dnn/bbwenet_data.c`. Keeping them as named constants makes future runtime
// code self-documenting and lets the loader fail loudly if a future libopus
// upgrade changes a layer shape.
const (
	FeatureDim  = 114
	FrameSize16 = 80
	CondDim     = 128

	FNetConv1In  = 342 // BBWENET_FNET_CONV1_IN_SIZE * 3 (libopus conv1d_init)
	FNetConv1Out = 128
	FNetConv2In  = 384 // BBWENET_FNET_CONV2_IN_SIZE * 3
	FNetConv2Out = 128
	FNetGRUIn    = 128
	FNetGRUOut   = 128 // 3 * 128 outputs for gru-zrh
	FNetTConvIn  = 128
	FNetTConvOut = 256 // 2 * 128 (kernel_size 2)

	TDShape1Alpha1FIn  = 256
	TDShape1Alpha1FOut = 80
	TDShape1Alpha1TIn  = 42
	TDShape1Alpha1TOut = 80
	TDShape1Alpha2In   = 160
	TDShape1Alpha2Out  = 80

	TDShape2Alpha1FIn  = 256
	TDShape2Alpha1FOut = 120
	TDShape2Alpha1TIn  = 42
	TDShape2Alpha1TOut = 120
	TDShape2Alpha2In   = 240
	TDShape2Alpha2Out  = 120

	AF1KernelIn  = 128
	AF1KernelOut = 48
	AF1GainIn    = 128
	AF1GainOut   = 3

	AF2KernelIn  = 128
	AF2KernelOut = 288
	AF2GainIn    = 128
	AF2GainOut   = 3

	AF3KernelIn  = 128
	AF3KernelOut = 48
	AF3GainIn    = 128
	AF3GainOut   = 1
)

var errInvalidBWEModel = errors.New("osce/bwe: invalid model")

// LinearLayer is the typed Go projection of one libopus `LinearLayer` weight
// bundle. Bias/Subias/Scale always come as float32 records; the weight
// payload may be either int8 (with scale) or float32, matching the libopus
// `linear_init` selection rules.
type LinearLayer struct {
	Bias         dnnblob.Float32View
	Subias       dnnblob.Float32View
	Weights      dnnblob.Int8View
	FloatWeights dnnblob.Float32View
	Scale        dnnblob.Float32View
	NbInputs     int
	NbOutputs    int
}

// LinearLayerSpec mirrors the libopus `linear_init(...)` argument tuple, with
// optional record names left empty when the layer does not carry that
// component. The Name selects the destination field on the Model.
type LinearLayerSpec struct {
	Name         string
	Bias         string
	Subias       string
	Weights      string
	FloatWeights string
	Scale        string
	NbInputs     int
	NbOutputs    int
}

// Model holds every BBWENet weight layer libopus binds from a USE_WEIGHTS_FILE
// blob. Order matches the `BBWENETLayers` struct in dnn/bbwenet_data.h.
type Model struct {
	FNetConv1        LinearLayer
	FNetConv2        LinearLayer
	FNetGRUInput     LinearLayer
	FNetGRURecurrent LinearLayer
	FNetTConv        LinearLayer
	TDShape1Alpha1F  LinearLayer
	TDShape1Alpha1T  LinearLayer
	TDShape1Alpha2   LinearLayer
	TDShape2Alpha1F  LinearLayer
	TDShape2Alpha1T  LinearLayer
	TDShape2Alpha2   LinearLayer
	AF1Kernel        LinearLayer
	AF1Gain          LinearLayer
	AF2Kernel        LinearLayer
	AF2Gain          LinearLayer
	AF3Kernel        LinearLayer
	AF3Gain          LinearLayer
}

// modelLayerSpecs is a 1:1 translation of the `init_bbwenetlayers()` call
// sequence in libopus dnn/bbwenet_data.c (line 83528..83544 in the 1.6.1
// scalar tree). Each entry's nb_inputs/nb_outputs comes straight from the
// libopus call.
var modelLayerSpecs = []LinearLayerSpec{
	{
		Name:         "bbwenet_fnet_conv1",
		Bias:         "bbwenet_fnet_conv1_bias",
		FloatWeights: "bbwenet_fnet_conv1_weights_float",
		NbInputs:     FNetConv1In,
		NbOutputs:    FNetConv1Out,
	},
	{
		Name:         "bbwenet_fnet_conv2",
		Bias:         "bbwenet_fnet_conv2_bias",
		Subias:       "bbwenet_fnet_conv2_subias",
		Weights:      "bbwenet_fnet_conv2_weights_int8",
		FloatWeights: "bbwenet_fnet_conv2_weights_float",
		Scale:        "bbwenet_fnet_conv2_scale",
		NbInputs:     FNetConv2In,
		NbOutputs:    FNetConv2Out,
	},
	{
		Name:         "bbwenet_fnet_gru_input",
		Bias:         "bbwenet_fnet_gru_input_bias",
		Subias:       "bbwenet_fnet_gru_input_subias",
		Weights:      "bbwenet_fnet_gru_input_weights_int8",
		FloatWeights: "bbwenet_fnet_gru_input_weights_float",
		Scale:        "bbwenet_fnet_gru_input_scale",
		NbInputs:     FNetGRUIn,
		NbOutputs:    3 * FNetGRUOut,
	},
	{
		Name:         "bbwenet_fnet_gru_recurrent",
		Bias:         "bbwenet_fnet_gru_recurrent_bias",
		Subias:       "bbwenet_fnet_gru_recurrent_subias",
		Weights:      "bbwenet_fnet_gru_recurrent_weights_int8",
		FloatWeights: "bbwenet_fnet_gru_recurrent_weights_float",
		Scale:        "bbwenet_fnet_gru_recurrent_scale",
		NbInputs:     FNetGRUOut,
		NbOutputs:    3 * FNetGRUOut,
	},
	{
		Name:         "bbwenet_fnet_tconv",
		Bias:         "bbwenet_fnet_tconv_bias",
		Subias:       "bbwenet_fnet_tconv_subias",
		Weights:      "bbwenet_fnet_tconv_weights_int8",
		FloatWeights: "bbwenet_fnet_tconv_weights_float",
		Scale:        "bbwenet_fnet_tconv_scale",
		NbInputs:     FNetTConvIn,
		NbOutputs:    FNetTConvOut,
	},
	{
		Name:         "bbwenet_tdshape1_alpha1_f",
		Bias:         "bbwenet_tdshape1_alpha1_f_bias",
		Subias:       "bbwenet_tdshape1_alpha1_f_subias",
		Weights:      "bbwenet_tdshape1_alpha1_f_weights_int8",
		FloatWeights: "bbwenet_tdshape1_alpha1_f_weights_float",
		Scale:        "bbwenet_tdshape1_alpha1_f_scale",
		NbInputs:     TDShape1Alpha1FIn,
		NbOutputs:    TDShape1Alpha1FOut,
	},
	{
		Name:         "bbwenet_tdshape1_alpha1_t",
		Bias:         "bbwenet_tdshape1_alpha1_t_bias",
		FloatWeights: "bbwenet_tdshape1_alpha1_t_weights_float",
		NbInputs:     TDShape1Alpha1TIn,
		NbOutputs:    TDShape1Alpha1TOut,
	},
	{
		Name:         "bbwenet_tdshape1_alpha2",
		Bias:         "bbwenet_tdshape1_alpha2_bias",
		FloatWeights: "bbwenet_tdshape1_alpha2_weights_float",
		NbInputs:     TDShape1Alpha2In,
		NbOutputs:    TDShape1Alpha2Out,
	},
	{
		Name:         "bbwenet_tdshape2_alpha1_f",
		Bias:         "bbwenet_tdshape2_alpha1_f_bias",
		Subias:       "bbwenet_tdshape2_alpha1_f_subias",
		Weights:      "bbwenet_tdshape2_alpha1_f_weights_int8",
		FloatWeights: "bbwenet_tdshape2_alpha1_f_weights_float",
		Scale:        "bbwenet_tdshape2_alpha1_f_scale",
		NbInputs:     TDShape2Alpha1FIn,
		NbOutputs:    TDShape2Alpha1FOut,
	},
	{
		Name:         "bbwenet_tdshape2_alpha1_t",
		Bias:         "bbwenet_tdshape2_alpha1_t_bias",
		FloatWeights: "bbwenet_tdshape2_alpha1_t_weights_float",
		NbInputs:     TDShape2Alpha1TIn,
		NbOutputs:    TDShape2Alpha1TOut,
	},
	{
		Name:         "bbwenet_tdshape2_alpha2",
		Bias:         "bbwenet_tdshape2_alpha2_bias",
		FloatWeights: "bbwenet_tdshape2_alpha2_weights_float",
		NbInputs:     TDShape2Alpha2In,
		NbOutputs:    TDShape2Alpha2Out,
	},
	{
		Name:         "bbwenet_af1_kernel",
		Bias:         "bbwenet_af1_kernel_bias",
		Subias:       "bbwenet_af1_kernel_subias",
		Weights:      "bbwenet_af1_kernel_weights_int8",
		FloatWeights: "bbwenet_af1_kernel_weights_float",
		Scale:        "bbwenet_af1_kernel_scale",
		NbInputs:     AF1KernelIn,
		NbOutputs:    AF1KernelOut,
	},
	{
		Name:         "bbwenet_af1_gain",
		Bias:         "bbwenet_af1_gain_bias",
		FloatWeights: "bbwenet_af1_gain_weights_float",
		NbInputs:     AF1GainIn,
		NbOutputs:    AF1GainOut,
	},
	{
		Name:         "bbwenet_af2_kernel",
		Bias:         "bbwenet_af2_kernel_bias",
		Subias:       "bbwenet_af2_kernel_subias",
		Weights:      "bbwenet_af2_kernel_weights_int8",
		FloatWeights: "bbwenet_af2_kernel_weights_float",
		Scale:        "bbwenet_af2_kernel_scale",
		NbInputs:     AF2KernelIn,
		NbOutputs:    AF2KernelOut,
	},
	{
		Name:         "bbwenet_af2_gain",
		Bias:         "bbwenet_af2_gain_bias",
		FloatWeights: "bbwenet_af2_gain_weights_float",
		NbInputs:     AF2GainIn,
		NbOutputs:    AF2GainOut,
	},
	{
		Name:         "bbwenet_af3_kernel",
		Bias:         "bbwenet_af3_kernel_bias",
		Subias:       "bbwenet_af3_kernel_subias",
		Weights:      "bbwenet_af3_kernel_weights_int8",
		FloatWeights: "bbwenet_af3_kernel_weights_float",
		Scale:        "bbwenet_af3_kernel_scale",
		NbInputs:     AF3KernelIn,
		NbOutputs:    AF3KernelOut,
	},
	{
		Name:         "bbwenet_af3_gain",
		Bias:         "bbwenet_af3_gain_bias",
		FloatWeights: "bbwenet_af3_gain_weights_float",
		NbInputs:     AF3GainIn,
		NbOutputs:    AF3GainOut,
	},
}

// LoadModel binds a libopus-style OSCE BWE model blob into typed Go layers.
// The blob must satisfy `dnnblob.Blob.SupportsOSCEBWE` (i.e. every required
// `bbwenet_*` record name is present); missing records or size mismatches
// return errInvalidBWEModel.
func LoadModel(blob *dnnblob.Blob) (*Model, error) {
	if blob == nil {
		return nil, errInvalidBWEModel
	}
	var model Model
	for _, spec := range modelLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidBWEModel
		}
		assignLayer(&model, spec.Name, layer)
	}
	return &model, nil
}

func loadLinearLayer(blob *dnnblob.Blob, spec LinearLayerSpec) (LinearLayer, error) {
	layer := LinearLayer{
		NbInputs:  spec.NbInputs,
		NbOutputs: spec.NbOutputs,
	}

	var err error
	if spec.Bias != "" {
		layer.Bias, err = loadFloatRecord(blob, spec.Bias, spec.NbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.Subias != "" {
		layer.Subias, err = loadFloatRecord(blob, spec.Subias, spec.NbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.FloatWeights != "" {
		layer.FloatWeights, err = loadOptionalFloatRecord(blob, spec.FloatWeights, spec.NbInputs*spec.NbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.Weights != "" {
		layer.Weights, err = loadOptionalInt8Record(blob, spec.Weights, spec.NbInputs*spec.NbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
		if !layer.Weights.Empty() {
			layer.Scale, err = loadFloatRecord(blob, spec.Scale, spec.NbOutputs)
			if err != nil {
				return LinearLayer{}, err
			}
		}
	}
	if layer.FloatWeights.Empty() && layer.Weights.Empty() {
		return LinearLayer{}, errInvalidBWEModel
	}
	return layer, nil
}

func loadFloatRecord(blob *dnnblob.Blob, name string, count int) (dnnblob.Float32View, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Float32View{}, errInvalidBWEModel
	}
	values, err := dnnblob.Float32ViewFromBytes(rec.Data, rec.Size)
	if err != nil || values.Len() != count {
		return dnnblob.Float32View{}, errInvalidBWEModel
	}
	return values, nil
}

func loadOptionalFloatRecord(blob *dnnblob.Blob, name string, count int) (dnnblob.Float32View, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Float32View{}, nil
	}
	values, err := dnnblob.Float32ViewFromBytes(rec.Data, rec.Size)
	if err != nil || values.Len() != count {
		return dnnblob.Float32View{}, errInvalidBWEModel
	}
	return values, nil
}

func loadOptionalInt8Record(blob *dnnblob.Blob, name string, count int) (dnnblob.Int8View, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Int8View{}, nil
	}
	values, err := dnnblob.Int8ViewFromBytes(rec.Data, rec.Size)
	if err != nil || values.Len() != count {
		return dnnblob.Int8View{}, errInvalidBWEModel
	}
	return values, nil
}

func assignLayer(model *Model, name string, layer LinearLayer) {
	switch name {
	case "bbwenet_fnet_conv1":
		model.FNetConv1 = layer
	case "bbwenet_fnet_conv2":
		model.FNetConv2 = layer
	case "bbwenet_fnet_gru_input":
		model.FNetGRUInput = layer
	case "bbwenet_fnet_gru_recurrent":
		model.FNetGRURecurrent = layer
	case "bbwenet_fnet_tconv":
		model.FNetTConv = layer
	case "bbwenet_tdshape1_alpha1_f":
		model.TDShape1Alpha1F = layer
	case "bbwenet_tdshape1_alpha1_t":
		model.TDShape1Alpha1T = layer
	case "bbwenet_tdshape1_alpha2":
		model.TDShape1Alpha2 = layer
	case "bbwenet_tdshape2_alpha1_f":
		model.TDShape2Alpha1F = layer
	case "bbwenet_tdshape2_alpha1_t":
		model.TDShape2Alpha1T = layer
	case "bbwenet_tdshape2_alpha2":
		model.TDShape2Alpha2 = layer
	case "bbwenet_af1_kernel":
		model.AF1Kernel = layer
	case "bbwenet_af1_gain":
		model.AF1Gain = layer
	case "bbwenet_af2_kernel":
		model.AF2Kernel = layer
	case "bbwenet_af2_gain":
		model.AF2Gain = layer
	case "bbwenet_af3_kernel":
		model.AF3Kernel = layer
	case "bbwenet_af3_gain":
		model.AF3Gain = layer
	}
}
