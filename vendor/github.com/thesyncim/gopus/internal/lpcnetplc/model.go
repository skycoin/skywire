package lpcnetplc

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// LPCNet PLC feature-prediction model dimensions, copied verbatim from libopus
// 1.6.1 dnn/plc_data.h (with NumBands from dnn/freq.h). This is the small GRU
// network that predicts the next LPCNet feature vector during packet loss;
// InputSize covers the band energies, the feature vector and the lost-flag, and
// the two GRU stages feed the output dense layer. The activation* values are
// the in-network activation selector shared with the runtime.
const (
	NumBands    = 18
	InputSize   = 2*NumBands + NumFeatures + 1
	DenseInSize = 128
	GRU1Size    = 192
	GRU2Size    = 192
	// Keep the shared quant scratch large enough for the biggest currently
	// bound libopus DNN layer input, including the PitchDNN downsampler.
	maxModelIn       = 288
	activationLinear = iota
	activationSigmoid
	activationTanh
)

var errInvalidModel = errors.New("lpcnetplc: invalid model")

// LinearLayer is the typed Go projection of one libopus LinearLayer weight
// bundle. Bias/Subias/Scale are float32 records; the weight payload is either
// int8 (paired with Scale) or float32, matching the libopus linear_init
// selection rules. The same shape is reused by every dense/GRU layer in the
// LPCNet PLC, FARGAN and PitchDNN models.
type LinearLayer struct {
	Bias         dnnblob.Float32View
	Subias       dnnblob.Float32View
	Weights      dnnblob.Int8View
	FloatWeights dnnblob.Float32View
	Scale        dnnblob.Float32View
	NbInputs     int
	NbOutputs    int
}

// LinearLayerSpec names the blob records that make up one LinearLayer and its
// input/output dimensions, mirroring the libopus linear_init argument tuple.
// Empty record names mark components a layer does not carry.
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

// Model holds the LPCNet PLC feature-prediction layers: the input dense layer,
// two GRU stages (input + recurrent weights each) and the output dense layer,
// matching the PLCModel struct in dnn/plc_data.h.
type Model struct {
	DenseIn  LinearLayer
	DenseOut LinearLayer
	GRU1In   LinearLayer
	GRU1Rec  LinearLayer
	GRU2In   LinearLayer
	GRU2Rec  LinearLayer
}

var modelLayerSpecs = []LinearLayerSpec{
	{
		Name:         "plc_dense_in",
		Bias:         "plc_dense_in_bias",
		FloatWeights: "plc_dense_in_weights_float",
		NbInputs:     InputSize,
		NbOutputs:    DenseInSize,
	},
	{
		Name:         "plc_dense_out",
		Bias:         "plc_dense_out_bias",
		FloatWeights: "plc_dense_out_weights_float",
		NbInputs:     GRU2Size,
		NbOutputs:    NumFeatures,
	},
	{
		Name:         "plc_gru1_input",
		Bias:         "plc_gru1_input_bias",
		Subias:       "plc_gru1_input_subias",
		Weights:      "plc_gru1_input_weights_int8",
		FloatWeights: "plc_gru1_input_weights_float",
		Scale:        "plc_gru1_input_scale",
		NbInputs:     DenseInSize,
		NbOutputs:    3 * GRU1Size,
	},
	{
		Name:         "plc_gru1_recurrent",
		Bias:         "plc_gru1_recurrent_bias",
		Subias:       "plc_gru1_recurrent_subias",
		Weights:      "plc_gru1_recurrent_weights_int8",
		FloatWeights: "plc_gru1_recurrent_weights_float",
		Scale:        "plc_gru1_recurrent_scale",
		NbInputs:     GRU1Size,
		NbOutputs:    3 * GRU1Size,
	},
	{
		Name:         "plc_gru2_input",
		Bias:         "plc_gru2_input_bias",
		Subias:       "plc_gru2_input_subias",
		Weights:      "plc_gru2_input_weights_int8",
		FloatWeights: "plc_gru2_input_weights_float",
		Scale:        "plc_gru2_input_scale",
		NbInputs:     GRU1Size,
		NbOutputs:    3 * GRU2Size,
	},
	{
		Name:         "plc_gru2_recurrent",
		Bias:         "plc_gru2_recurrent_bias",
		Subias:       "plc_gru2_recurrent_subias",
		Weights:      "plc_gru2_recurrent_weights_int8",
		FloatWeights: "plc_gru2_recurrent_weights_float",
		Scale:        "plc_gru2_recurrent_scale",
		NbInputs:     GRU2Size,
		NbOutputs:    3 * GRU2Size,
	},
}

// ModelLayerSpecs returns the libopus-shaped PLC model layer specs the
// pure-Go loader binds from a validated weights blob. Callers must treat the
// returned slice as read-only.
func ModelLayerSpecs() []LinearLayerSpec {
	return modelLayerSpecs
}

// LoadModel binds a libopus-style PLC model blob into typed Go layers.
func LoadModel(blob *dnnblob.Blob) (*Model, error) {
	if blob == nil {
		return nil, errInvalidModel
	}
	var model Model
	for _, spec := range modelLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidModel
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
		return LinearLayer{}, errInvalidModel
	}
	return layer, nil
}

func loadFloatRecord(blob *dnnblob.Blob, name string, count int) (dnnblob.Float32View, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Float32View{}, errInvalidModel
	}
	values, err := dnnblob.Float32ViewFromBytes(rec.Data, rec.Size)
	if err != nil || values.Len() != count {
		return dnnblob.Float32View{}, errInvalidModel
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
		return dnnblob.Float32View{}, errInvalidModel
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
		return dnnblob.Int8View{}, errInvalidModel
	}
	return values, nil
}

func assignLayer(model *Model, name string, layer LinearLayer) {
	switch name {
	case "plc_dense_in":
		model.DenseIn = layer
	case "plc_dense_out":
		model.DenseOut = layer
	case "plc_gru1_input":
		model.GRU1In = layer
	case "plc_gru1_recurrent":
		model.GRU1Rec = layer
	case "plc_gru2_input":
		model.GRU2In = layer
	case "plc_gru2_recurrent":
		model.GRU2Rec = layer
	}
}
