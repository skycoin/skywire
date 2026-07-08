package silk

// FrameParams holds the decoded side-information parameters for a single SILK
// frame in a convenience, allocation-friendly form (the bit-exact decode path
// uses the internal decoderControl/indices structs instead). It collects the
// frame classification, per-subframe gains, LPC coefficients and, for voiced
// frames, the pitch and LTP parameters needed for synthesis filtering. The
// fields correspond to the indices decoded by libopus silk/decode_indices.c.
type FrameParams struct {
	// Frame classification
	SignalType  int // 0=inactive, 1=unvoiced, 2=voiced
	QuantOffset int // 0=low, 1=high quantization

	// Subframe parameters (4 subframes for 20ms, 2 for 10ms)
	NumSubframes int
	Gains        []int32 // Q16 format, one per subframe

	// LPC parameters
	LPCOrder  int
	LPCCoeffs []int16 // Q12 format

	// Pitch parameters (voiced only)
	PitchLags      []int32  // One per subframe
	LTPCoeffs      [][]int8 // Q7 format, [subframe][5 taps]
	LTPPeriodicity int      // 0, 1, or 2 (selects LTP codebook)
	LTPScaleIndex  int      // LTP scale index for gain adjustment

	// Excitation (filled by excitation decoder)
	Excitation []int32
}

// DecodeFrameType decodes a frame's signal type and quantization offset from the
// decoder's current range-decoder position, selecting the active or inactive
// iCDF by vadFlag (RFC 6716 Section 4.2.7.3). This is the same first step that
// silkDecodeIndices performs inline; it mirrors the frame-type decode at the top
// of libopus silk/decode_indices.c silk_decode_indices and is provided as a
// standalone helper.
//
// For inactive frames (vadFlag=false) it returns signal type in {0,1} with the
// low/high quantization offset; for active frames (vadFlag=true) signal type is
// 1 (unvoiced) or 2 (voiced).
func (d *Decoder) DecodeFrameType(vadFlag bool) (signalType, quantOffset int) {
	var idx int
	if vadFlag {
		// ICDFFrameTypeVADActive encodes 4 outcomes:
		// 0: unvoiced low, 1: unvoiced high, 2: voiced low, 3: voiced high
		idx = d.rangeDecoder.DecodeICDF16_8Unchecked(ICDFFrameTypeVADActive) + 2
	} else {
		// VAD inactive uses a 2-outcome table (typeOffset 0 or 1).
		idx = d.rangeDecoder.DecodeICDF16_8Unchecked(ICDFFrameTypeVADInactive)
	}
	signalType = idx >> 1
	quantOffset = idx & 1
	return
}
