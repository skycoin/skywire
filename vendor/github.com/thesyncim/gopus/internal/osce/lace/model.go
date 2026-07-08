// Package lace implements the libopus OSCE LACE and NoLACE neural postfilters
// that enhance decoded SILK speech.
//
// Load binds the model weights from a validated dnnblob.Blob into typed Go
// layers; the Model value exposes views over the underlying float / int8
// weight records so the forward pass runs without re-parsing the blob. The
// model dimensions and required weight-record names mirror libopus 1.6.1
// dnn/lace_data.{h,c} and dnn/nolace_data.{h,c}.
//
// LACEState.Process and NoLACEState.Process run the per-frame forward passes
// from libopus dnn/osce.c (lace_process_20ms_frame /
// nolace_process_20ms_frame) and the adaptive comb/shaping DSP from
// dnn/nndsp.c. Both operate on 16 kHz signal frames driven by per-subframe
// feature vectors, the encoded bit count and pitch periods, matching the
// inputs libopus derives from the SILK decoder state.
package lace

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// errInvalidLACEModel is returned by Load when the supplied blob does not
// satisfy the libopus LACE / NoLACE manifests (missing record, size
// mismatch, etc).
var errInvalidLACEModel = errors.New("osce/lace: invalid model")

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

// linearSpec mirrors the libopus `linear_init(...)` argument tuple, with
// optional record names left empty when the layer does not carry that
// component.
type linearSpec struct {
	name         string
	bias         string
	subias       string
	weights      string
	floatWeights string
	scale        string
	nbInputs     int
	nbOutputs    int
}

// LACEModel holds every LACE postfilter weight layer libopus binds from a
// USE_WEIGHTS_FILE blob. Field order matches the `LACELayers` struct in
// dnn/lace_data.h and the `init_lacelayers` call sequence.
type LACEModel struct {
	PitchEmbedding   LinearLayer
	FNetConv1        LinearLayer
	FNetConv2        LinearLayer
	FNetTConv        LinearLayer
	FNetGRUInput     LinearLayer
	FNetGRURecurrent LinearLayer
	CF1Kernel        LinearLayer
	CF1Gain          LinearLayer
	CF1GlobalGain    LinearLayer
	CF2Kernel        LinearLayer
	CF2Gain          LinearLayer
	CF2GlobalGain    LinearLayer
	AF1Kernel        LinearLayer
	AF1Gain          LinearLayer
}

// NoLACEModel holds every NoLACE postfilter weight layer libopus binds from a
// USE_WEIGHTS_FILE blob. Field order matches the `NOLACELayers` struct in
// dnn/nolace_data.h and the `init_nolacelayers` call sequence.
type NoLACEModel struct {
	PitchEmbedding   LinearLayer
	FNetConv1        LinearLayer
	FNetConv2        LinearLayer
	FNetTConv        LinearLayer
	FNetGRUInput     LinearLayer
	FNetGRURecurrent LinearLayer
	CF1Kernel        LinearLayer
	CF1Gain          LinearLayer
	CF1GlobalGain    LinearLayer
	CF2Kernel        LinearLayer
	CF2Gain          LinearLayer
	CF2GlobalGain    LinearLayer
	AF1Kernel        LinearLayer
	AF1Gain          LinearLayer
	TDShape1Alpha1F  LinearLayer
	TDShape1Alpha1T  LinearLayer
	TDShape1Alpha2   LinearLayer
	TDShape2Alpha1F  LinearLayer
	TDShape2Alpha1T  LinearLayer
	TDShape2Alpha2   LinearLayer
	TDShape3Alpha1F  LinearLayer
	TDShape3Alpha1T  LinearLayer
	TDShape3Alpha2   LinearLayer
	AF2Kernel        LinearLayer
	AF2Gain          LinearLayer
	AF3Kernel        LinearLayer
	AF3Gain          LinearLayer
	AF4Kernel        LinearLayer
	AF4Gain          LinearLayer
	PostCF1          LinearLayer
	PostCF2          LinearLayer
	PostAF1          LinearLayer
	PostAF2          LinearLayer
	PostAF3          LinearLayer
}

// Model is the combined LACE + NoLACE binding. libopus carries both
// postfilters together (osce_init creates a LACE and a NoLACE state inside
// OSCEModel) and the upstream weights blob ships with both manifests present,
// so callers always get both when SupportsOSCE() is true.
type Model struct {
	LACE   LACEModel
	NoLACE NoLACEModel
}

// laceSpecs is a 1:1 translation of the `init_lacelayers()` call sequence
// in libopus dnn/lace_data.c. Each entry's nbInputs/nbOutputs comes straight
// from the libopus call.
var laceSpecs = []linearSpec{
	{name: "PitchEmbedding", bias: "lace_pitch_embedding_bias", floatWeights: "lace_pitch_embedding_weights_float", nbInputs: 301, nbOutputs: 64},
	{name: "FNetConv1", bias: "lace_fnet_conv1_bias", floatWeights: "lace_fnet_conv1_weights_float", nbInputs: 173, nbOutputs: 96},
	{name: "FNetConv2", bias: "lace_fnet_conv2_bias", subias: "lace_fnet_conv2_subias", weights: "lace_fnet_conv2_weights_int8", floatWeights: "lace_fnet_conv2_weights_float", scale: "lace_fnet_conv2_scale", nbInputs: 768, nbOutputs: 128},
	{name: "FNetTConv", bias: "lace_fnet_tconv_bias", subias: "lace_fnet_tconv_subias", weights: "lace_fnet_tconv_weights_int8", floatWeights: "lace_fnet_tconv_weights_float", scale: "lace_fnet_tconv_scale", nbInputs: 128, nbOutputs: 512},
	{name: "FNetGRUInput", bias: "lace_fnet_gru_input_bias", subias: "lace_fnet_gru_input_subias", weights: "lace_fnet_gru_input_weights_int8", floatWeights: "lace_fnet_gru_input_weights_float", scale: "lace_fnet_gru_input_scale", nbInputs: 128, nbOutputs: 384},
	{name: "FNetGRURecurrent", bias: "lace_fnet_gru_recurrent_bias", subias: "lace_fnet_gru_recurrent_subias", weights: "lace_fnet_gru_recurrent_weights_int8", floatWeights: "lace_fnet_gru_recurrent_weights_float", scale: "lace_fnet_gru_recurrent_scale", nbInputs: 128, nbOutputs: 384},
	{name: "CF1Kernel", bias: "lace_cf1_kernel_bias", subias: "lace_cf1_kernel_subias", weights: "lace_cf1_kernel_weights_int8", floatWeights: "lace_cf1_kernel_weights_float", scale: "lace_cf1_kernel_scale", nbInputs: 128, nbOutputs: 16},
	{name: "CF1Gain", bias: "lace_cf1_gain_bias", floatWeights: "lace_cf1_gain_weights_float", nbInputs: 128, nbOutputs: 1},
	{name: "CF1GlobalGain", bias: "lace_cf1_global_gain_bias", floatWeights: "lace_cf1_global_gain_weights_float", nbInputs: 128, nbOutputs: 1},
	{name: "CF2Kernel", bias: "lace_cf2_kernel_bias", subias: "lace_cf2_kernel_subias", weights: "lace_cf2_kernel_weights_int8", floatWeights: "lace_cf2_kernel_weights_float", scale: "lace_cf2_kernel_scale", nbInputs: 128, nbOutputs: 16},
	{name: "CF2Gain", bias: "lace_cf2_gain_bias", floatWeights: "lace_cf2_gain_weights_float", nbInputs: 128, nbOutputs: 1},
	{name: "CF2GlobalGain", bias: "lace_cf2_global_gain_bias", floatWeights: "lace_cf2_global_gain_weights_float", nbInputs: 128, nbOutputs: 1},
	{name: "AF1Kernel", bias: "lace_af1_kernel_bias", subias: "lace_af1_kernel_subias", weights: "lace_af1_kernel_weights_int8", floatWeights: "lace_af1_kernel_weights_float", scale: "lace_af1_kernel_scale", nbInputs: 128, nbOutputs: 16},
	{name: "AF1Gain", bias: "lace_af1_gain_bias", floatWeights: "lace_af1_gain_weights_float", nbInputs: 128, nbOutputs: 1},
}

// nolaceSpecs is a 1:1 translation of the `init_nolacelayers()` call
// sequence in libopus dnn/nolace_data.c.
var nolaceSpecs = []linearSpec{
	{name: "PitchEmbedding", bias: "nolace_pitch_embedding_bias", floatWeights: "nolace_pitch_embedding_weights_float", nbInputs: 301, nbOutputs: 64},
	{name: "FNetConv1", bias: "nolace_fnet_conv1_bias", floatWeights: "nolace_fnet_conv1_weights_float", nbInputs: 173, nbOutputs: 96},
	{name: "FNetConv2", bias: "nolace_fnet_conv2_bias", subias: "nolace_fnet_conv2_subias", weights: "nolace_fnet_conv2_weights_int8", floatWeights: "nolace_fnet_conv2_weights_float", scale: "nolace_fnet_conv2_scale", nbInputs: 768, nbOutputs: 160},
	{name: "FNetTConv", bias: "nolace_fnet_tconv_bias", subias: "nolace_fnet_tconv_subias", weights: "nolace_fnet_tconv_weights_int8", floatWeights: "nolace_fnet_tconv_weights_float", scale: "nolace_fnet_tconv_scale", nbInputs: 160, nbOutputs: 640},
	{name: "FNetGRUInput", bias: "nolace_fnet_gru_input_bias", subias: "nolace_fnet_gru_input_subias", weights: "nolace_fnet_gru_input_weights_int8", floatWeights: "nolace_fnet_gru_input_weights_float", scale: "nolace_fnet_gru_input_scale", nbInputs: 160, nbOutputs: 480},
	{name: "FNetGRURecurrent", bias: "nolace_fnet_gru_recurrent_bias", subias: "nolace_fnet_gru_recurrent_subias", weights: "nolace_fnet_gru_recurrent_weights_int8", floatWeights: "nolace_fnet_gru_recurrent_weights_float", scale: "nolace_fnet_gru_recurrent_scale", nbInputs: 160, nbOutputs: 480},
	{name: "CF1Kernel", bias: "nolace_cf1_kernel_bias", subias: "nolace_cf1_kernel_subias", weights: "nolace_cf1_kernel_weights_int8", floatWeights: "nolace_cf1_kernel_weights_float", scale: "nolace_cf1_kernel_scale", nbInputs: 160, nbOutputs: 16},
	{name: "CF1Gain", bias: "nolace_cf1_gain_bias", floatWeights: "nolace_cf1_gain_weights_float", nbInputs: 160, nbOutputs: 1},
	{name: "CF1GlobalGain", bias: "nolace_cf1_global_gain_bias", floatWeights: "nolace_cf1_global_gain_weights_float", nbInputs: 160, nbOutputs: 1},
	{name: "CF2Kernel", bias: "nolace_cf2_kernel_bias", subias: "nolace_cf2_kernel_subias", weights: "nolace_cf2_kernel_weights_int8", floatWeights: "nolace_cf2_kernel_weights_float", scale: "nolace_cf2_kernel_scale", nbInputs: 160, nbOutputs: 16},
	{name: "CF2Gain", bias: "nolace_cf2_gain_bias", floatWeights: "nolace_cf2_gain_weights_float", nbInputs: 160, nbOutputs: 1},
	{name: "CF2GlobalGain", bias: "nolace_cf2_global_gain_bias", floatWeights: "nolace_cf2_global_gain_weights_float", nbInputs: 160, nbOutputs: 1},
	{name: "AF1Kernel", bias: "nolace_af1_kernel_bias", subias: "nolace_af1_kernel_subias", weights: "nolace_af1_kernel_weights_int8", floatWeights: "nolace_af1_kernel_weights_float", scale: "nolace_af1_kernel_scale", nbInputs: 160, nbOutputs: 32},
	{name: "AF1Gain", bias: "nolace_af1_gain_bias", floatWeights: "nolace_af1_gain_weights_float", nbInputs: 160, nbOutputs: 2},
	{name: "TDShape1Alpha1F", bias: "nolace_tdshape1_alpha1_f_bias", subias: "nolace_tdshape1_alpha1_f_subias", weights: "nolace_tdshape1_alpha1_f_weights_int8", floatWeights: "nolace_tdshape1_alpha1_f_weights_float", scale: "nolace_tdshape1_alpha1_f_scale", nbInputs: 320, nbOutputs: 80},
	{name: "TDShape1Alpha1T", bias: "nolace_tdshape1_alpha1_t_bias", floatWeights: "nolace_tdshape1_alpha1_t_weights_float", nbInputs: 42, nbOutputs: 80},
	{name: "TDShape1Alpha2", bias: "nolace_tdshape1_alpha2_bias", floatWeights: "nolace_tdshape1_alpha2_weights_float", nbInputs: 160, nbOutputs: 80},
	{name: "TDShape2Alpha1F", bias: "nolace_tdshape2_alpha1_f_bias", subias: "nolace_tdshape2_alpha1_f_subias", weights: "nolace_tdshape2_alpha1_f_weights_int8", floatWeights: "nolace_tdshape2_alpha1_f_weights_float", scale: "nolace_tdshape2_alpha1_f_scale", nbInputs: 320, nbOutputs: 80},
	{name: "TDShape2Alpha1T", bias: "nolace_tdshape2_alpha1_t_bias", floatWeights: "nolace_tdshape2_alpha1_t_weights_float", nbInputs: 42, nbOutputs: 80},
	{name: "TDShape2Alpha2", bias: "nolace_tdshape2_alpha2_bias", floatWeights: "nolace_tdshape2_alpha2_weights_float", nbInputs: 160, nbOutputs: 80},
	{name: "TDShape3Alpha1F", bias: "nolace_tdshape3_alpha1_f_bias", subias: "nolace_tdshape3_alpha1_f_subias", weights: "nolace_tdshape3_alpha1_f_weights_int8", floatWeights: "nolace_tdshape3_alpha1_f_weights_float", scale: "nolace_tdshape3_alpha1_f_scale", nbInputs: 320, nbOutputs: 80},
	{name: "TDShape3Alpha1T", bias: "nolace_tdshape3_alpha1_t_bias", floatWeights: "nolace_tdshape3_alpha1_t_weights_float", nbInputs: 42, nbOutputs: 80},
	{name: "TDShape3Alpha2", bias: "nolace_tdshape3_alpha2_bias", floatWeights: "nolace_tdshape3_alpha2_weights_float", nbInputs: 160, nbOutputs: 80},
	{name: "AF2Kernel", bias: "nolace_af2_kernel_bias", subias: "nolace_af2_kernel_subias", weights: "nolace_af2_kernel_weights_int8", floatWeights: "nolace_af2_kernel_weights_float", scale: "nolace_af2_kernel_scale", nbInputs: 160, nbOutputs: 64},
	{name: "AF2Gain", bias: "nolace_af2_gain_bias", floatWeights: "nolace_af2_gain_weights_float", nbInputs: 160, nbOutputs: 2},
	{name: "AF3Kernel", bias: "nolace_af3_kernel_bias", subias: "nolace_af3_kernel_subias", weights: "nolace_af3_kernel_weights_int8", floatWeights: "nolace_af3_kernel_weights_float", scale: "nolace_af3_kernel_scale", nbInputs: 160, nbOutputs: 64},
	{name: "AF3Gain", bias: "nolace_af3_gain_bias", floatWeights: "nolace_af3_gain_weights_float", nbInputs: 160, nbOutputs: 2},
	{name: "AF4Kernel", bias: "nolace_af4_kernel_bias", subias: "nolace_af4_kernel_subias", weights: "nolace_af4_kernel_weights_int8", floatWeights: "nolace_af4_kernel_weights_float", scale: "nolace_af4_kernel_scale", nbInputs: 160, nbOutputs: 32},
	{name: "AF4Gain", bias: "nolace_af4_gain_bias", floatWeights: "nolace_af4_gain_weights_float", nbInputs: 160, nbOutputs: 1},
	{name: "PostCF1", bias: "nolace_post_cf1_bias", subias: "nolace_post_cf1_subias", weights: "nolace_post_cf1_weights_int8", floatWeights: "nolace_post_cf1_weights_float", scale: "nolace_post_cf1_scale", nbInputs: 320, nbOutputs: 160},
	{name: "PostCF2", bias: "nolace_post_cf2_bias", subias: "nolace_post_cf2_subias", weights: "nolace_post_cf2_weights_int8", floatWeights: "nolace_post_cf2_weights_float", scale: "nolace_post_cf2_scale", nbInputs: 320, nbOutputs: 160},
	{name: "PostAF1", bias: "nolace_post_af1_bias", subias: "nolace_post_af1_subias", weights: "nolace_post_af1_weights_int8", floatWeights: "nolace_post_af1_weights_float", scale: "nolace_post_af1_scale", nbInputs: 320, nbOutputs: 160},
	{name: "PostAF2", bias: "nolace_post_af2_bias", subias: "nolace_post_af2_subias", weights: "nolace_post_af2_weights_int8", floatWeights: "nolace_post_af2_weights_float", scale: "nolace_post_af2_scale", nbInputs: 320, nbOutputs: 160},
	{name: "PostAF3", bias: "nolace_post_af3_bias", subias: "nolace_post_af3_subias", weights: "nolace_post_af3_weights_int8", floatWeights: "nolace_post_af3_weights_float", scale: "nolace_post_af3_scale", nbInputs: 320, nbOutputs: 160},
}

// Load binds a libopus-style OSCE LACE+NoLACE model blob into typed Go
// layers. The blob must satisfy both `dnnblob.Blob.SupportsOSCELACE` and
// `SupportsOSCENoLACE` (every required `lace_*` / `nolace_*` record name
// present with the expected size); missing records or size mismatches
// return errInvalidLACEModel.
func Load(blob *dnnblob.Blob) (*Model, error) {
	if blob == nil {
		return nil, errInvalidLACEModel
	}
	var model Model
	for _, spec := range laceSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidLACEModel
		}
		assignLACELayer(&model.LACE, spec.name, layer)
	}
	for _, spec := range nolaceSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidLACEModel
		}
		assignNoLACELayer(&model.NoLACE, spec.name, layer)
	}
	return &model, nil
}

func loadLinearLayer(blob *dnnblob.Blob, spec linearSpec) (LinearLayer, error) {
	layer := LinearLayer{
		NbInputs:  spec.nbInputs,
		NbOutputs: spec.nbOutputs,
	}

	var err error
	if spec.bias != "" {
		layer.Bias, err = loadFloatRecord(blob, spec.bias, spec.nbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.subias != "" {
		layer.Subias, err = loadFloatRecord(blob, spec.subias, spec.nbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.floatWeights != "" {
		layer.FloatWeights, err = loadOptionalFloatRecord(blob, spec.floatWeights, spec.nbInputs*spec.nbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.weights != "" {
		layer.Weights, err = loadOptionalInt8Record(blob, spec.weights, spec.nbInputs*spec.nbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
		if !layer.Weights.Empty() {
			layer.Scale, err = loadFloatRecord(blob, spec.scale, spec.nbOutputs)
			if err != nil {
				return LinearLayer{}, err
			}
		}
	}
	if layer.FloatWeights.Empty() && layer.Weights.Empty() {
		return LinearLayer{}, errInvalidLACEModel
	}
	return layer, nil
}

func loadFloatRecord(blob *dnnblob.Blob, name string, count int) (dnnblob.Float32View, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Float32View{}, errInvalidLACEModel
	}
	values, err := dnnblob.Float32ViewFromBytes(rec.Data, rec.Size)
	if err != nil || values.Len() != count {
		return dnnblob.Float32View{}, errInvalidLACEModel
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
		return dnnblob.Float32View{}, errInvalidLACEModel
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
		return dnnblob.Int8View{}, errInvalidLACEModel
	}
	return values, nil
}

func assignLACELayer(model *LACEModel, name string, layer LinearLayer) {
	switch name {
	case "PitchEmbedding":
		model.PitchEmbedding = layer
	case "FNetConv1":
		model.FNetConv1 = layer
	case "FNetConv2":
		model.FNetConv2 = layer
	case "FNetTConv":
		model.FNetTConv = layer
	case "FNetGRUInput":
		model.FNetGRUInput = layer
	case "FNetGRURecurrent":
		model.FNetGRURecurrent = layer
	case "CF1Kernel":
		model.CF1Kernel = layer
	case "CF1Gain":
		model.CF1Gain = layer
	case "CF1GlobalGain":
		model.CF1GlobalGain = layer
	case "CF2Kernel":
		model.CF2Kernel = layer
	case "CF2Gain":
		model.CF2Gain = layer
	case "CF2GlobalGain":
		model.CF2GlobalGain = layer
	case "AF1Kernel":
		model.AF1Kernel = layer
	case "AF1Gain":
		model.AF1Gain = layer
	}
}

func assignNoLACELayer(model *NoLACEModel, name string, layer LinearLayer) {
	switch name {
	case "PitchEmbedding":
		model.PitchEmbedding = layer
	case "FNetConv1":
		model.FNetConv1 = layer
	case "FNetConv2":
		model.FNetConv2 = layer
	case "FNetTConv":
		model.FNetTConv = layer
	case "FNetGRUInput":
		model.FNetGRUInput = layer
	case "FNetGRURecurrent":
		model.FNetGRURecurrent = layer
	case "CF1Kernel":
		model.CF1Kernel = layer
	case "CF1Gain":
		model.CF1Gain = layer
	case "CF1GlobalGain":
		model.CF1GlobalGain = layer
	case "CF2Kernel":
		model.CF2Kernel = layer
	case "CF2Gain":
		model.CF2Gain = layer
	case "CF2GlobalGain":
		model.CF2GlobalGain = layer
	case "AF1Kernel":
		model.AF1Kernel = layer
	case "AF1Gain":
		model.AF1Gain = layer
	case "TDShape1Alpha1F":
		model.TDShape1Alpha1F = layer
	case "TDShape1Alpha1T":
		model.TDShape1Alpha1T = layer
	case "TDShape1Alpha2":
		model.TDShape1Alpha2 = layer
	case "TDShape2Alpha1F":
		model.TDShape2Alpha1F = layer
	case "TDShape2Alpha1T":
		model.TDShape2Alpha1T = layer
	case "TDShape2Alpha2":
		model.TDShape2Alpha2 = layer
	case "TDShape3Alpha1F":
		model.TDShape3Alpha1F = layer
	case "TDShape3Alpha1T":
		model.TDShape3Alpha1T = layer
	case "TDShape3Alpha2":
		model.TDShape3Alpha2 = layer
	case "AF2Kernel":
		model.AF2Kernel = layer
	case "AF2Gain":
		model.AF2Gain = layer
	case "AF3Kernel":
		model.AF3Kernel = layer
	case "AF3Gain":
		model.AF3Gain = layer
	case "AF4Kernel":
		model.AF4Kernel = layer
	case "AF4Gain":
		model.AF4Gain = layer
	case "PostCF1":
		model.PostCF1 = layer
	case "PostCF2":
		model.PostCF2 = layer
	case "PostAF1":
		model.PostAF1 = layer
	case "PostAF2":
		model.PostAF2 = layer
	case "PostAF3":
		model.PostAF3 = layer
	}
}

// Loaded reports whether the model has been bound to a valid blob. A
// zero-valued *Model returns false; any layer field's NbInputs/NbOutputs
// stays zero in that case.
func (m *Model) Loaded() bool {
	if m == nil {
		return false
	}
	// Use a canonical layer's NbInputs as the loaded-sentinel. LACE's
	// pitch embedding layer is always present in a valid blob.
	return m.LACE.PitchEmbedding.NbInputs == 301 && m.NoLACE.PitchEmbedding.NbInputs == 301
}
