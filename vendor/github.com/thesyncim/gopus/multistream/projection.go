// Projection (ambisonics family 3) public surface for gopus.
//
// gopus does not expose a separate OpusProjectionEncoder / OpusProjectionDecoder
// type as libopus does.  Projection encoding/decoding is fully supported through
// the standard Encoder and Decoder types using mapping family 3:
//
//   - NewEncoderAmbisonics(sampleRate, channels, 3) creates a projection encoder.
//   - NewDecoder + SetProjectionDemixingMatrix creates a projection decoder.
//
// # Supported ambisonics orders
//
// libopus 1.6.1 provides pre-computed mixing and demixing matrices for orders
// 1..5 only.  gopus follows the same boundary exactly.
//
// Supported channel counts for mapping family 3 (projection):
//
//	Order 1 (FOA):     4 channels, or  6 channels (+ non-diegetic stereo)
//	Order 2 (SOA):     9 channels, or 11 channels (+ non-diegetic stereo)
//	Order 3 (TOA):    16 channels, or 18 channels (+ non-diegetic stereo)
//	Order 4 (4thOA):  25 channels, or 27 channels (+ non-diegetic stereo)
//	Order 5 (5thOA):  36 channels, or 38 channels (+ non-diegetic stereo)
//
// Ambisonics orders 6-14 (channels 49-227) are valid ambisonics channel counts
// but are NOT supported by mapping family 3.  Use mapping family 2
// (NewEncoderAmbisonics with family=2) for those orders.
// ErrProjectionOrderUnsupported is returned for out-of-range orders.
//
// Reference: libopus src/mapping_matrix.c (mapping_matrix_foa…fifthoa tables),
// src/opus_projection_encoder.c:opus_projection_ambisonics_encoder_get_size
//
// # Projection matrix CTLs
//
// After creating a projection encoder, the demixing matrix can be retrieved via:
//
//	enc.GetDemixingMatrix()       // S16LE bytes, matches OPUS_PROJECTION_GET_DEMIXING_MATRIX
//	enc.DemixingMatrixSize()      // byte count, matches OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE
//	enc.DemixingMatrixGain()      // gain field, matches OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN
//
// The demixing matrix is passed to the decoder via SetProjectionDemixingMatrix.
//
// # Typical usage
//
//	// Encoder side:
//	enc, err := NewEncoderAmbisonics(48000, 4, 3) // FOA, family 3
//	demixMatrix := enc.GetDemixingMatrix()         // retrieve for decoder init
//	packet, err := enc.Encode(pcm, frameSize)
//
//	// Decoder side:
//	mapping := []byte{0, 1, 2, 3}
//	dec, err := NewDecoder(48000, 4, enc.Streams(), enc.CoupledStreams(), mapping)
//	err = dec.SetProjectionDemixingMatrix(demixMatrix)
//	pcmOut, err := dec.DecodeToFloat32(packet, frameSize)

package multistream

// NewProjectionEncoder creates a family-3 projection ambisonics encoder.
//
// This is a convenience alias for NewEncoderAmbisonics(sampleRate, channels, 3).
// Supported channel counts are: 4, 6, 9, 11, 16, 18, 25, 27, 36, 38
// (ambisonics orders 1-5, with or without non-diegetic stereo channels).
//
// Returns ErrProjectionOrderUnsupported for valid ambisonics channel counts outside
// the range supported by family 3 (orders 6-14 / channels 49-227).
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_ambisonics_encoder_create
func NewProjectionEncoder(sampleRate, channels int) (*Encoder, error) {
	return NewEncoderAmbisonics(sampleRate, channels, 3)
}

// NewProjectionDecoder creates a family-3 projection ambisonics decoder initialized
// with a demixing matrix obtained from a projection encoder.
//
// The demixingMatrix parameter must match the output of Encoder.GetDemixingMatrix()
// (S16LE-encoded, as returned by OPUS_PROJECTION_GET_DEMIXING_MATRIX).  Passing nil
// creates a plain multistream decoder without projection demixing.
//
// streams and coupledStreams must match the values from the corresponding encoder
// (enc.Streams(), enc.CoupledStreams()).  The mapping is always the trivial identity
// [0, 1, 2, ..., channels-1] for projection decoders.
//
// Reference: libopus src/opus_projection_decoder.c:opus_projection_decoder_create
func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, demixingMatrix []byte) (*Decoder, error) {
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
