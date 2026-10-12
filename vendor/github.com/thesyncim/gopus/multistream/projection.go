// Projection helpers provide family 3 ambisonics encoding and decoding.
// Projection matrices are available for ambisonics orders 1–5, with channel
// counts 4, 6, 9, 11, 16, 18, 25, 27, 36, and 38. Family 2 supports the higher
// valid ambisonics orders without projection matrices.
//
// Encoder.GetDemixingMatrix returns the S16LE demixing matrix for a projection
// encoder; NewProjectionDecoder accepts that matrix for decoding.

package multistream

// NewProjectionEncoder returns a family 3 projection encoder for ambisonics
// orders 1–5. Supported channel counts are 4, 6, 9, 11, 16, 18, 25, 27, 36,
// and 38; unsupported projection orders return ErrProjectionOrderUnsupported.
func NewProjectionEncoder(sampleRate, channels int) (*Encoder, error) {
	return NewEncoderAmbisonics(sampleRate, channels, 3)
}

// NewProjectionDecoder returns a decoder with identity channel mapping and an
// optional projection demixing matrix. The matrix uses signed 16-bit little-
// endian coefficients in column-major order, with channels rows and
// streams+coupledStreams columns; its length must be 2*channels*(streams+
// coupledStreams) bytes. Encoder.GetDemixingMatrix returns a compatible matrix.
// An empty matrix disables demixing. The matrix is copied. An invalid matrix
// returns ErrInvalidProjectionMatrix; invalid dimensions return the corresponding
// constructor error.
func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, demixingMatrix []byte) (*Decoder, error) {
	if channels < 1 || channels > 255 {
		return nil, ErrInvalidChannels
	}
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	dec, err := NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}
	if len(demixingMatrix) > 0 {
		if err := dec.SetProjectionDemixingMatrix(demixingMatrix); err != nil {
			return nil, err
		}
	}
	return dec, nil
}
