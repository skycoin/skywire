// Package rdovae implements the libopus RDOVAE (rate-distortion-optimized
// variational auto-encoder) used by Deep Redundancy (DRED).
//
// The encoder compresses 20-feature LPCNet frames into quantized latent and
// state vectors; the decoder reconstructs feature frames from those latents.
// Both the encoder (encoder_model.go / encoder_runtime.go) and decoder
// (model.go / runtime.go) bind their weights from a validated dnnblob.Blob and
// mirror libopus 1.6.1 dnn/dred_rdovae_{enc,dec}.c and dnn/dred_rdovae_*_data.c
// bit-for-bit, held in place by the package and decoder DRED parity tests.
package rdovae

import "github.com/thesyncim/gopus/internal/dnnblob"

// SparseBlockSize is the libopus block-sparse weight tile width
// (SPARSE_BLOCK_SIZE in dnn/parse_lpcnet_weights.c): block-sparse layers store
// nonzero weights in 32-output blocks.
const SparseBlockSize = 32

// RDOVAE model dimensions, copied verbatim from libopus 1.6.1
// dnn/dred_rdovae_constants.h and the dred_rdovae_{enc,dec}_data layer specs.
// They size the dense/GRU/conv stages of both the encoder and decoder and the
// quantized latent/state representation (LatentDim, StateDim and their padded
// widths, plus NumQuantLevels quantizer entries).
const (
	NumFeatures       = 20
	LatentDim         = 25
	StateDim          = 50
	PaddedLatentDim   = 32
	PaddedStateDim    = 56
	NumQuantLevels    = 16
	MaxRNNNeurons     = 64
	MaxConvInputs     = 128
	EncMaxRNNNeurons  = 128
	EncMaxConvInputs  = 128
	DecMaxRNNNeurons  = 64
	MaxProcessFrames  = 4
	Dense1OutSize     = 96
	HiddenInitOutSize = 128
	OutputOutSize     = 4 * NumFeatures
	ConvDenseOutSize  = 32
	GRUInitOutSize    = 320
	GRUOutSize        = 64
	GRUStateSize      = 64
	ConvOutSize       = 32
	ConvInSize        = 32
	ConvStateSize     = 32
	ProcessBufferSize = Dense1OutSize + 5*GRUOutSize + 5*ConvOutSize
)

const latentStride = LatentDim + 1

// LinearLayer mirrors the libopus LinearLayer metadata and tensors the DRED
// decoder runtime depends on.
type LinearLayer struct {
	Bias         dnnblob.Float32View
	Subias       dnnblob.Float32View
	Weights      dnnblob.Int8View
	FloatWeights dnnblob.Float32View
	WeightsIdx   dnnblob.Int32View
	Scale        dnnblob.Float32View
	NbInputs     int
	NbOutputs    int
}

// LinearLayerSpec mirrors one init_rdovaedec() linear_init(...) declaration.
type LinearLayerSpec struct {
	Name         string
	Bias         string
	Subias       string
	Weights      string
	FloatWeights string
	WeightsIdx   string
	Scale        string
	NbInputs     int
	NbOutputs    int
}

// Decoder mirrors the libopus RDOVAEDec model layout used by DRED decoding.
type Decoder struct {
	Dense1     LinearLayer
	GLU        [5]LinearLayer
	HiddenInit LinearLayer
	Output     LinearLayer
	ConvDense  [5]LinearLayer
	GRUInit    LinearLayer
	GRUInput   [5]LinearLayer
	GRURecur   [5]LinearLayer
	Conv       [5]LinearLayer
}

var decoderLayerSpecs = []LinearLayerSpec{
	{Name: "dec_dense1", Bias: "dec_dense1_bias", FloatWeights: "dec_dense1_weights_float", NbInputs: 26, NbOutputs: 96},
	{Name: "dec_glu1", Bias: "dec_glu1_bias", Subias: "dec_glu1_subias", Weights: "dec_glu1_weights_int8", FloatWeights: "dec_glu1_weights_float", Scale: "dec_glu1_scale", NbInputs: 64, NbOutputs: 64},
	{Name: "dec_glu2", Bias: "dec_glu2_bias", Subias: "dec_glu2_subias", Weights: "dec_glu2_weights_int8", FloatWeights: "dec_glu2_weights_float", Scale: "dec_glu2_scale", NbInputs: 64, NbOutputs: 64},
	{Name: "dec_glu3", Bias: "dec_glu3_bias", Subias: "dec_glu3_subias", Weights: "dec_glu3_weights_int8", FloatWeights: "dec_glu3_weights_float", Scale: "dec_glu3_scale", NbInputs: 64, NbOutputs: 64},
	{Name: "dec_glu4", Bias: "dec_glu4_bias", Subias: "dec_glu4_subias", Weights: "dec_glu4_weights_int8", FloatWeights: "dec_glu4_weights_float", Scale: "dec_glu4_scale", NbInputs: 64, NbOutputs: 64},
	{Name: "dec_glu5", Bias: "dec_glu5_bias", Subias: "dec_glu5_subias", Weights: "dec_glu5_weights_int8", FloatWeights: "dec_glu5_weights_float", Scale: "dec_glu5_scale", NbInputs: 64, NbOutputs: 64},
	{Name: "dec_hidden_init", Bias: "dec_hidden_init_bias", FloatWeights: "dec_hidden_init_weights_float", NbInputs: 50, NbOutputs: 128},
	{Name: "dec_output", Bias: "dec_output_bias", Subias: "dec_output_subias", Weights: "dec_output_weights_int8", FloatWeights: "dec_output_weights_float", WeightsIdx: "dec_output_weights_idx", Scale: "dec_output_scale", NbInputs: 576, NbOutputs: 80},
	{Name: "dec_conv_dense1", Bias: "dec_conv_dense1_bias", Subias: "dec_conv_dense1_subias", Weights: "dec_conv_dense1_weights_int8", FloatWeights: "dec_conv_dense1_weights_float", WeightsIdx: "dec_conv_dense1_weights_idx", Scale: "dec_conv_dense1_scale", NbInputs: 160, NbOutputs: 32},
	{Name: "dec_conv_dense2", Bias: "dec_conv_dense2_bias", Subias: "dec_conv_dense2_subias", Weights: "dec_conv_dense2_weights_int8", FloatWeights: "dec_conv_dense2_weights_float", WeightsIdx: "dec_conv_dense2_weights_idx", Scale: "dec_conv_dense2_scale", NbInputs: 256, NbOutputs: 32},
	{Name: "dec_conv_dense3", Bias: "dec_conv_dense3_bias", Subias: "dec_conv_dense3_subias", Weights: "dec_conv_dense3_weights_int8", FloatWeights: "dec_conv_dense3_weights_float", WeightsIdx: "dec_conv_dense3_weights_idx", Scale: "dec_conv_dense3_scale", NbInputs: 352, NbOutputs: 32},
	{Name: "dec_conv_dense4", Bias: "dec_conv_dense4_bias", Subias: "dec_conv_dense4_subias", Weights: "dec_conv_dense4_weights_int8", FloatWeights: "dec_conv_dense4_weights_float", WeightsIdx: "dec_conv_dense4_weights_idx", Scale: "dec_conv_dense4_scale", NbInputs: 448, NbOutputs: 32},
	{Name: "dec_conv_dense5", Bias: "dec_conv_dense5_bias", Subias: "dec_conv_dense5_subias", Weights: "dec_conv_dense5_weights_int8", FloatWeights: "dec_conv_dense5_weights_float", WeightsIdx: "dec_conv_dense5_weights_idx", Scale: "dec_conv_dense5_scale", NbInputs: 544, NbOutputs: 32},
	{Name: "dec_gru_init", Bias: "dec_gru_init_bias", Subias: "dec_gru_init_subias", Weights: "dec_gru_init_weights_int8", FloatWeights: "dec_gru_init_weights_float", WeightsIdx: "dec_gru_init_weights_idx", Scale: "dec_gru_init_scale", NbInputs: 128, NbOutputs: 320},
	{Name: "dec_gru1_input", Bias: "dec_gru1_input_bias", Subias: "dec_gru1_input_subias", Weights: "dec_gru1_input_weights_int8", FloatWeights: "dec_gru1_input_weights_float", WeightsIdx: "dec_gru1_input_weights_idx", Scale: "dec_gru1_input_scale", NbInputs: 96, NbOutputs: 192},
	{Name: "dec_gru1_recurrent", Bias: "dec_gru1_recurrent_bias", Subias: "dec_gru1_recurrent_subias", Weights: "dec_gru1_recurrent_weights_int8", FloatWeights: "dec_gru1_recurrent_weights_float", Scale: "dec_gru1_recurrent_scale", NbInputs: 64, NbOutputs: 192},
	{Name: "dec_gru2_input", Bias: "dec_gru2_input_bias", Subias: "dec_gru2_input_subias", Weights: "dec_gru2_input_weights_int8", FloatWeights: "dec_gru2_input_weights_float", WeightsIdx: "dec_gru2_input_weights_idx", Scale: "dec_gru2_input_scale", NbInputs: 192, NbOutputs: 192},
	{Name: "dec_gru2_recurrent", Bias: "dec_gru2_recurrent_bias", Subias: "dec_gru2_recurrent_subias", Weights: "dec_gru2_recurrent_weights_int8", FloatWeights: "dec_gru2_recurrent_weights_float", Scale: "dec_gru2_recurrent_scale", NbInputs: 64, NbOutputs: 192},
	{Name: "dec_gru3_input", Bias: "dec_gru3_input_bias", Subias: "dec_gru3_input_subias", Weights: "dec_gru3_input_weights_int8", FloatWeights: "dec_gru3_input_weights_float", WeightsIdx: "dec_gru3_input_weights_idx", Scale: "dec_gru3_input_scale", NbInputs: 288, NbOutputs: 192},
	{Name: "dec_gru3_recurrent", Bias: "dec_gru3_recurrent_bias", Subias: "dec_gru3_recurrent_subias", Weights: "dec_gru3_recurrent_weights_int8", FloatWeights: "dec_gru3_recurrent_weights_float", Scale: "dec_gru3_recurrent_scale", NbInputs: 64, NbOutputs: 192},
	{Name: "dec_gru4_input", Bias: "dec_gru4_input_bias", Subias: "dec_gru4_input_subias", Weights: "dec_gru4_input_weights_int8", FloatWeights: "dec_gru4_input_weights_float", WeightsIdx: "dec_gru4_input_weights_idx", Scale: "dec_gru4_input_scale", NbInputs: 384, NbOutputs: 192},
	{Name: "dec_gru4_recurrent", Bias: "dec_gru4_recurrent_bias", Subias: "dec_gru4_recurrent_subias", Weights: "dec_gru4_recurrent_weights_int8", FloatWeights: "dec_gru4_recurrent_weights_float", Scale: "dec_gru4_recurrent_scale", NbInputs: 64, NbOutputs: 192},
	{Name: "dec_gru5_input", Bias: "dec_gru5_input_bias", Subias: "dec_gru5_input_subias", Weights: "dec_gru5_input_weights_int8", FloatWeights: "dec_gru5_input_weights_float", WeightsIdx: "dec_gru5_input_weights_idx", Scale: "dec_gru5_input_scale", NbInputs: 480, NbOutputs: 192},
	{Name: "dec_gru5_recurrent", Bias: "dec_gru5_recurrent_bias", Subias: "dec_gru5_recurrent_subias", Weights: "dec_gru5_recurrent_weights_int8", FloatWeights: "dec_gru5_recurrent_weights_float", Scale: "dec_gru5_recurrent_scale", NbInputs: 64, NbOutputs: 192},
	{Name: "dec_conv1", Bias: "dec_conv1_bias", Subias: "dec_conv1_subias", Weights: "dec_conv1_weights_int8", FloatWeights: "dec_conv1_weights_float", Scale: "dec_conv1_scale", NbInputs: 64, NbOutputs: 32},
	{Name: "dec_conv2", Bias: "dec_conv2_bias", Subias: "dec_conv2_subias", Weights: "dec_conv2_weights_int8", FloatWeights: "dec_conv2_weights_float", Scale: "dec_conv2_scale", NbInputs: 64, NbOutputs: 32},
	{Name: "dec_conv3", Bias: "dec_conv3_bias", Subias: "dec_conv3_subias", Weights: "dec_conv3_weights_int8", FloatWeights: "dec_conv3_weights_float", Scale: "dec_conv3_scale", NbInputs: 64, NbOutputs: 32},
	{Name: "dec_conv4", Bias: "dec_conv4_bias", Subias: "dec_conv4_subias", Weights: "dec_conv4_weights_int8", FloatWeights: "dec_conv4_weights_float", Scale: "dec_conv4_scale", NbInputs: 64, NbOutputs: 32},
	{Name: "dec_conv5", Bias: "dec_conv5_bias", Subias: "dec_conv5_subias", Weights: "dec_conv5_weights_int8", FloatWeights: "dec_conv5_weights_float", Scale: "dec_conv5_scale", NbInputs: 64, NbOutputs: 32},
}

// DecoderLayerSpecs returns the libopus-shaped DRED decoder layer specs the
// pure-Go loader binds from a validated DNN blob. Callers must treat the
// returned slice as read-only.
func DecoderLayerSpecs() []LinearLayerSpec {
	return decoderLayerSpecs
}
