//go:build gopus_fixed_point

package encoder

// EncodeShortMixedResWithAnalysisMaxBytes encodes the exact FIXED_POINT
// opus_res values from a projection short-input callback. pcm is their float32
// analysis view; rawRes retains the source integer values through the fixed
// high-pass and CELT paths (src/opus_projection_encoder.c,
// src/mapping_matrix.c:opus_projection_copy_channel_in_short).
func (e *Encoder) EncodeShortMixedResWithAnalysisMaxBytes(pcm []float32, rawRes []int32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	channels := int(e.channels)
	expectedLen := frameSize * channels
	if frameSize <= 0 || len(pcm) != expectedLen || len(rawRes) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	if len(analysisPCM) < expectedLen || len(analysisPCM)%channels != 0 {
		return nil, ErrInvalidFrameSize
	}
	inputPCM := e.prepareOpusResInput(pcm)
	e.prepareFixedInputRes(pcm)
	copy(e.fixedRawRes, rawRes)
	defer e.clearFixedInputRes()
	configuredDepth := e.lsbDepth
	if e.lsbDepth > 16 {
		e.lsbDepth = 16
	}
	if e.analyzer != nil {
		e.analyzer.SetLSBDepth(int(e.lsbDepth))
	}
	defer func() {
		e.lsbDepth = configuredDepth
		if e.analyzer != nil {
			e.analyzer.SetLSBDepth(int(configuredDepth))
		}
	}()
	e.ClearFloatInputFrame()
	return e.encodeOpusResWithAnalysisMaxBytes(inputPCM, frameSize, maxDataBytes, func() {
		e.refreshFrameAnalysisF32(analysisPCM, frameSize)
	})
}
