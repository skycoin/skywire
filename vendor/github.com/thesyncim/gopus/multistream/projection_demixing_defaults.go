package multistream

// defaultProjectionDemixingMatrixBytes returns the demixing matrix for a family-3 encoder
// as a serialized S16LE byte slice, matching the output of
// opus_projection_encoder_ctl(OPUS_PROJECTION_GET_DEMIXING_MATRIX).
//
// The extraction mirrors libopus opus_projection_encoder_ctl case
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST:
//
//	nb_input_streams = streams + coupled_streams
//	nb_output_streams = channels
//	for i in 0..nb_input_streams-1:
//	  for j in 0..nb_output_streams-1:
//	    k = demixing_matrix->rows * i + j
//	    out[2*l]   = low  byte of matrix_data[k]
//	    out[2*l+1] = high byte of matrix_data[k]
//	    l++
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
func defaultProjectionDemixingMatrixBytes(channels, streams, coupled int) ([]byte, bool) {
	def, ok := projectionDemixingDefaults[channels]
	if !ok {
		return nil, false
	}

	nbInputStreams := streams + coupled
	nbOutputStreams := channels

	// Validate that the stored matrix is large enough.
	if nbInputStreams > def.cols || nbOutputStreams > def.rows {
		return nil, false
	}

	out := make([]byte, 2*nbInputStreams*nbOutputStreams)
	l := 0
	for i := range nbInputStreams {
		for j := range nbOutputStreams {
			k := def.rows*i + j
			v := def.matrix[k]
			out[2*l] = byte(v)
			out[2*l+1] = byte(uint16(v) >> 8)
			l++
		}
	}
	return out, true
}

// ProjectionDemixingMatrixSize returns the byte size of the demixing matrix for a
// family-3 projection encoder with the given dimensions.
//
// This mirrors OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE:
//
//	size = channels * (streams + coupled_streams) * sizeof(opus_int16)
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
func ProjectionDemixingMatrixSize(channels, streams, coupled int) int {
	return channels * (streams + coupled) * 2
}
