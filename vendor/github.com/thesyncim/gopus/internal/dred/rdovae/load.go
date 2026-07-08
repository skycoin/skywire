package rdovae

import (
	"errors"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

var errInvalidModel = errors.New("rdovae: invalid decoder model")

// LoadDecoder binds a validated libopus-style DRED decoder blob into typed Go
// RDOVAE decoder layers.
func LoadDecoder(blob *dnnblob.Blob) (*Decoder, error) {
	if blob == nil {
		return nil, errInvalidModel
	}
	var dec Decoder
	for _, spec := range decoderLayerSpecs {
		layer, err := loadLinearLayer(blob, spec)
		if err != nil {
			return nil, errInvalidModel
		}
		assignDecoderLayer(&dec, spec.Name, layer)
	}
	return &dec, nil
}

func loadLinearLayer(blob *dnnblob.Blob, spec LinearLayerSpec) (LinearLayer, error) {
	layer := LinearLayer{
		NbInputs:  spec.NbInputs,
		NbOutputs: spec.NbOutputs,
	}

	var totalBlocks int
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
	if spec.WeightsIdx != "" {
		layer.WeightsIdx, totalBlocks, err = loadSparseIndexRecord(blob, spec.WeightsIdx, spec.NbInputs, spec.NbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.Weights != "" {
		expected := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			expected = SparseBlockSize * totalBlocks
		}
		layer.Weights, err = loadInt8Record(blob, spec.Weights, expected)
		if err != nil {
			return LinearLayer{}, err
		}
		layer.Scale, err = loadFloatRecord(blob, spec.Scale, spec.NbOutputs)
		if err != nil {
			return LinearLayer{}, err
		}
	}
	if spec.FloatWeights != "" {
		expected := spec.NbInputs * spec.NbOutputs
		if totalBlocks > 0 {
			expected = SparseBlockSize * totalBlocks
		}
		layer.FloatWeights, err = loadOptionalFloatRecord(blob, spec.FloatWeights, expected)
		if err != nil {
			return LinearLayer{}, err
		}
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

func loadInt8Record(blob *dnnblob.Blob, name string, count int) (dnnblob.Int8View, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Int8View{}, errInvalidModel
	}
	values, err := dnnblob.Int8ViewFromBytes(rec.Data, rec.Size)
	if err != nil || values.Len() != count {
		return dnnblob.Int8View{}, errInvalidModel
	}
	return values, nil
}

func loadSparseIndexRecord(blob *dnnblob.Blob, name string, nbInputs, nbOutputs int) (dnnblob.Int32View, int, error) {
	rec, ok := blob.Record(name)
	if !ok {
		return dnnblob.Int32View{}, 0, errInvalidModel
	}
	values, err := dnnblob.Int32ViewFromBytes(rec.Data, rec.Size)
	if err != nil {
		return dnnblob.Int32View{}, 0, errInvalidModel
	}
	remain := values.Len()
	idx := 0
	totalBlocks := 0
	out := nbOutputs
	for remain > 0 {
		nbBlocks := int(values.At(idx))
		idx++
		if nbBlocks < 0 || remain < nbBlocks+1 {
			return dnnblob.Int32View{}, 0, errInvalidModel
		}
		for range nbBlocks {
			pos := int(values.At(idx))
			idx++
			if pos+3 >= nbInputs || (pos&0x3) != 0 {
				return dnnblob.Int32View{}, 0, errInvalidModel
			}
		}
		out -= 8
		remain -= nbBlocks + 1
		totalBlocks += nbBlocks
	}
	if out != 0 {
		return dnnblob.Int32View{}, 0, errInvalidModel
	}
	return values, totalBlocks, nil
}

func assignDecoderLayer(dec *Decoder, name string, layer LinearLayer) {
	switch name {
	case "dec_dense1":
		dec.Dense1 = layer
	case "dec_glu1":
		dec.GLU[0] = layer
	case "dec_glu2":
		dec.GLU[1] = layer
	case "dec_glu3":
		dec.GLU[2] = layer
	case "dec_glu4":
		dec.GLU[3] = layer
	case "dec_glu5":
		dec.GLU[4] = layer
	case "dec_hidden_init":
		dec.HiddenInit = layer
	case "dec_output":
		dec.Output = layer
	case "dec_conv_dense1":
		dec.ConvDense[0] = layer
	case "dec_conv_dense2":
		dec.ConvDense[1] = layer
	case "dec_conv_dense3":
		dec.ConvDense[2] = layer
	case "dec_conv_dense4":
		dec.ConvDense[3] = layer
	case "dec_conv_dense5":
		dec.ConvDense[4] = layer
	case "dec_gru_init":
		dec.GRUInit = layer
	case "dec_gru1_input":
		dec.GRUInput[0] = layer
	case "dec_gru1_recurrent":
		dec.GRURecur[0] = layer
	case "dec_gru2_input":
		dec.GRUInput[1] = layer
	case "dec_gru2_recurrent":
		dec.GRURecur[1] = layer
	case "dec_gru3_input":
		dec.GRUInput[2] = layer
	case "dec_gru3_recurrent":
		dec.GRURecur[2] = layer
	case "dec_gru4_input":
		dec.GRUInput[3] = layer
	case "dec_gru4_recurrent":
		dec.GRURecur[3] = layer
	case "dec_gru5_input":
		dec.GRUInput[4] = layer
	case "dec_gru5_recurrent":
		dec.GRURecur[4] = layer
	case "dec_conv1":
		dec.Conv[0] = layer
	case "dec_conv2":
		dec.Conv[1] = layer
	case "dec_conv3":
		dec.Conv[2] = layer
	case "dec_conv4":
		dec.Conv[3] = layer
	case "dec_conv5":
		dec.Conv[4] = layer
	}
}
