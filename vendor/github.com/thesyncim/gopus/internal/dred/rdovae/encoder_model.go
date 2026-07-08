package rdovae

// EncoderModel mirrors the libopus RDOVAEEnc model layout used by DRED
// encoding.
type EncoderModel struct {
	Dense1    LinearLayer
	ZDense    LinearLayer
	GDense2   LinearLayer
	GDense1   LinearLayer
	ConvDense [5]LinearLayer
	GRUInput  [5]LinearLayer
	GRURecur  [5]LinearLayer
	Conv      [5]LinearLayer
}

var encoderLayerSpecs = []LinearLayerSpec{
	{Name: "enc_dense1", Bias: "enc_dense1_bias", FloatWeights: "enc_dense1_weights_float", NbInputs: 40, NbOutputs: 64},
	{Name: "enc_zdense", Bias: "enc_zdense_bias", Subias: "enc_zdense_subias", Weights: "enc_zdense_weights_int8", FloatWeights: "enc_zdense_weights_float", Scale: "enc_zdense_scale", NbInputs: 544, NbOutputs: 32},
	{Name: "gdense2", Bias: "gdense2_bias", Subias: "gdense2_subias", Weights: "gdense2_weights_int8", FloatWeights: "gdense2_weights_float", Scale: "gdense2_scale", NbInputs: 128, NbOutputs: 56},
	{Name: "gdense1", Bias: "gdense1_bias", Subias: "gdense1_subias", Weights: "gdense1_weights_int8", FloatWeights: "gdense1_weights_float", WeightsIdx: "gdense1_weights_idx", Scale: "gdense1_scale", NbInputs: 544, NbOutputs: 128},
	{Name: "enc_conv_dense1", Bias: "enc_conv_dense1_bias", Subias: "enc_conv_dense1_subias", Weights: "enc_conv_dense1_weights_int8", FloatWeights: "enc_conv_dense1_weights_float", WeightsIdx: "enc_conv_dense1_weights_idx", Scale: "enc_conv_dense1_scale", NbInputs: 96, NbOutputs: 64},
	{Name: "enc_conv_dense2", Bias: "enc_conv_dense2_bias", Subias: "enc_conv_dense2_subias", Weights: "enc_conv_dense2_weights_int8", FloatWeights: "enc_conv_dense2_weights_float", WeightsIdx: "enc_conv_dense2_weights_idx", Scale: "enc_conv_dense2_scale", NbInputs: 192, NbOutputs: 64},
	{Name: "enc_conv_dense3", Bias: "enc_conv_dense3_bias", Subias: "enc_conv_dense3_subias", Weights: "enc_conv_dense3_weights_int8", FloatWeights: "enc_conv_dense3_weights_float", WeightsIdx: "enc_conv_dense3_weights_idx", Scale: "enc_conv_dense3_scale", NbInputs: 288, NbOutputs: 64},
	{Name: "enc_conv_dense4", Bias: "enc_conv_dense4_bias", Subias: "enc_conv_dense4_subias", Weights: "enc_conv_dense4_weights_int8", FloatWeights: "enc_conv_dense4_weights_float", WeightsIdx: "enc_conv_dense4_weights_idx", Scale: "enc_conv_dense4_scale", NbInputs: 384, NbOutputs: 64},
	{Name: "enc_conv_dense5", Bias: "enc_conv_dense5_bias", Subias: "enc_conv_dense5_subias", Weights: "enc_conv_dense5_weights_int8", FloatWeights: "enc_conv_dense5_weights_float", WeightsIdx: "enc_conv_dense5_weights_idx", Scale: "enc_conv_dense5_scale", NbInputs: 480, NbOutputs: 64},
	{Name: "enc_gru1_input", Bias: "enc_gru1_input_bias", Subias: "enc_gru1_input_subias", Weights: "enc_gru1_input_weights_int8", FloatWeights: "enc_gru1_input_weights_float", WeightsIdx: "enc_gru1_input_weights_idx", Scale: "enc_gru1_input_scale", NbInputs: 64, NbOutputs: 96},
	{Name: "enc_gru1_recurrent", Bias: "enc_gru1_recurrent_bias", Subias: "enc_gru1_recurrent_subias", Weights: "enc_gru1_recurrent_weights_int8", FloatWeights: "enc_gru1_recurrent_weights_float", Scale: "enc_gru1_recurrent_scale", NbInputs: 32, NbOutputs: 96},
	{Name: "enc_gru2_input", Bias: "enc_gru2_input_bias", Subias: "enc_gru2_input_subias", Weights: "enc_gru2_input_weights_int8", FloatWeights: "enc_gru2_input_weights_float", WeightsIdx: "enc_gru2_input_weights_idx", Scale: "enc_gru2_input_scale", NbInputs: 160, NbOutputs: 96},
	{Name: "enc_gru2_recurrent", Bias: "enc_gru2_recurrent_bias", Subias: "enc_gru2_recurrent_subias", Weights: "enc_gru2_recurrent_weights_int8", FloatWeights: "enc_gru2_recurrent_weights_float", Scale: "enc_gru2_recurrent_scale", NbInputs: 32, NbOutputs: 96},
	{Name: "enc_gru3_input", Bias: "enc_gru3_input_bias", Subias: "enc_gru3_input_subias", Weights: "enc_gru3_input_weights_int8", FloatWeights: "enc_gru3_input_weights_float", WeightsIdx: "enc_gru3_input_weights_idx", Scale: "enc_gru3_input_scale", NbInputs: 256, NbOutputs: 96},
	{Name: "enc_gru3_recurrent", Bias: "enc_gru3_recurrent_bias", Subias: "enc_gru3_recurrent_subias", Weights: "enc_gru3_recurrent_weights_int8", FloatWeights: "enc_gru3_recurrent_weights_float", Scale: "enc_gru3_recurrent_scale", NbInputs: 32, NbOutputs: 96},
	{Name: "enc_gru4_input", Bias: "enc_gru4_input_bias", Subias: "enc_gru4_input_subias", Weights: "enc_gru4_input_weights_int8", FloatWeights: "enc_gru4_input_weights_float", WeightsIdx: "enc_gru4_input_weights_idx", Scale: "enc_gru4_input_scale", NbInputs: 352, NbOutputs: 96},
	{Name: "enc_gru4_recurrent", Bias: "enc_gru4_recurrent_bias", Subias: "enc_gru4_recurrent_subias", Weights: "enc_gru4_recurrent_weights_int8", FloatWeights: "enc_gru4_recurrent_weights_float", Scale: "enc_gru4_recurrent_scale", NbInputs: 32, NbOutputs: 96},
	{Name: "enc_gru5_input", Bias: "enc_gru5_input_bias", Subias: "enc_gru5_input_subias", Weights: "enc_gru5_input_weights_int8", FloatWeights: "enc_gru5_input_weights_float", WeightsIdx: "enc_gru5_input_weights_idx", Scale: "enc_gru5_input_scale", NbInputs: 448, NbOutputs: 96},
	{Name: "enc_gru5_recurrent", Bias: "enc_gru5_recurrent_bias", Subias: "enc_gru5_recurrent_subias", Weights: "enc_gru5_recurrent_weights_int8", FloatWeights: "enc_gru5_recurrent_weights_float", Scale: "enc_gru5_recurrent_scale", NbInputs: 32, NbOutputs: 96},
	{Name: "enc_conv1", Bias: "enc_conv1_bias", Subias: "enc_conv1_subias", Weights: "enc_conv1_weights_int8", FloatWeights: "enc_conv1_weights_float", Scale: "enc_conv1_scale", NbInputs: 128, NbOutputs: 64},
	{Name: "enc_conv2", Bias: "enc_conv2_bias", Subias: "enc_conv2_subias", Weights: "enc_conv2_weights_int8", FloatWeights: "enc_conv2_weights_float", Scale: "enc_conv2_scale", NbInputs: 128, NbOutputs: 64},
	{Name: "enc_conv3", Bias: "enc_conv3_bias", Subias: "enc_conv3_subias", Weights: "enc_conv3_weights_int8", FloatWeights: "enc_conv3_weights_float", Scale: "enc_conv3_scale", NbInputs: 128, NbOutputs: 64},
	{Name: "enc_conv4", Bias: "enc_conv4_bias", Subias: "enc_conv4_subias", Weights: "enc_conv4_weights_int8", FloatWeights: "enc_conv4_weights_float", Scale: "enc_conv4_scale", NbInputs: 128, NbOutputs: 64},
	{Name: "enc_conv5", Bias: "enc_conv5_bias", Subias: "enc_conv5_subias", Weights: "enc_conv5_weights_int8", FloatWeights: "enc_conv5_weights_float", Scale: "enc_conv5_scale", NbInputs: 128, NbOutputs: 64},
}

// EncoderLayerSpecs returns the libopus-shaped DRED encoder layer specs the
// pure-Go loader binds from a validated DNN blob. Callers must treat the
// returned slice as read-only.
func EncoderLayerSpecs() []LinearLayerSpec {
	return encoderLayerSpecs
}
