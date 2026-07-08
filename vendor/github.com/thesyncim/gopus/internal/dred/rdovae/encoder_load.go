package rdovae

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

var errInvalidEncoderModel = errors.New("rdovae: invalid encoder model")

// LoadEncoder binds a validated libopus-style DRED encoder blob into typed Go
// RDOVAE encoder layers.
func LoadEncoder(blob *dnnblob.Blob) (*EncoderModel, error) {
	if blob == nil {
		return nil, errInvalidEncoderModel
	}
	var enc EncoderModel
	for _, spec := range encoderLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidEncoderModel
		}
		assignEncoderLayer(&enc, spec.Name, layer)
	}
	return &enc, nil
}

func assignEncoderLayer(enc *EncoderModel, name string, layer LinearLayer) {
	switch name {
	case "enc_dense1":
		enc.Dense1 = layer
	case "enc_zdense":
		enc.ZDense = layer
	case "gdense2":
		enc.GDense2 = layer
	case "gdense1":
		enc.GDense1 = layer
	case "enc_conv_dense1":
		enc.ConvDense[0] = layer
	case "enc_conv_dense2":
		enc.ConvDense[1] = layer
	case "enc_conv_dense3":
		enc.ConvDense[2] = layer
	case "enc_conv_dense4":
		enc.ConvDense[3] = layer
	case "enc_conv_dense5":
		enc.ConvDense[4] = layer
	case "enc_gru1_input":
		enc.GRUInput[0] = layer
	case "enc_gru1_recurrent":
		enc.GRURecur[0] = layer
	case "enc_gru2_input":
		enc.GRUInput[1] = layer
	case "enc_gru2_recurrent":
		enc.GRURecur[1] = layer
	case "enc_gru3_input":
		enc.GRUInput[2] = layer
	case "enc_gru3_recurrent":
		enc.GRURecur[2] = layer
	case "enc_gru4_input":
		enc.GRUInput[3] = layer
	case "enc_gru4_recurrent":
		enc.GRURecur[3] = layer
	case "enc_gru5_input":
		enc.GRUInput[4] = layer
	case "enc_gru5_recurrent":
		enc.GRURecur[4] = layer
	case "enc_conv1":
		enc.Conv[0] = layer
	case "enc_conv2":
		enc.Conv[1] = layer
	case "enc_conv3":
		enc.Conv[2] = layer
	case "enc_conv4":
		enc.Conv[3] = layer
	case "enc_conv5":
		enc.Conv[4] = layer
	}
}
