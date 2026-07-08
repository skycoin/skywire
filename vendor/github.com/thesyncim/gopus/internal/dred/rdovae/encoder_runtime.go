package rdovae

const (
	encoderInputSize            = 2 * NumFeatures
	encoderDense1OutSize        = 64
	encoderLatentOutSize        = PaddedLatentDim
	encoderStateHiddenOutSize   = 128
	encoderStateInitOutSize     = PaddedStateDim
	encoderConvDenseOutSize     = 64
	encoderGRUOutSize           = 32
	encoderGRUStateSize         = 32
	encoderConvOutSize          = 64
	encoderConvInSize           = 64
	encoderProcessBufferSize    = encoderDense1OutSize + 5*encoderGRUOutSize + 5*encoderConvOutSize
	encoderDilatedConvStateSize = 2 * encoderConvInSize
)

type encoderState struct {
	initialized bool
	gru         [5][encoderGRUStateSize]float32
	conv        [5][encoderDilatedConvStateSize]float32
}

type encoderRuntimeScratch struct {
	base          runtimeScratch
	stateHidden   [encoderStateHiddenOutSize]float32
	paddedLatents [encoderLatentOutSize]float32
	paddedState   [encoderStateInitOutSize]float32
}

// EncoderProcessor owns reusable DRED encoder runtime state and scratch so
// callers can keep repeated d-frame encoding on an explicit zero-allocation
// path.
type EncoderProcessor struct {
	state   encoderState
	scratch encoderRuntimeScratch
}

// Reset clears the retained encoder-side RDOVAE recurrent state.
func (p *EncoderProcessor) Reset() {
	if p == nil {
		return
	}
	p.state = encoderState{}
}

// EncodeDFrame mirrors libopus dred_rdovae_encode_dframe() for one
// concatenated 2x20-feature DRED input frame.
func (m *EncoderModel) EncodeDFrame(latents, initialState, input []float32) bool {
	return m.EncodeDFrameWithProcessor(nil, latents, initialState, input)
}

// EncodeDFrameWithProcessor mirrors libopus dred_rdovae_encode_dframe() and
// reuses caller-owned recurrent state/scratch when provided.
func (m *EncoderModel) EncodeDFrameWithProcessor(processor *EncoderProcessor, latents, initialState, input []float32) bool {
	if m == nil || len(latents) < LatentDim || len(initialState) < StateDim || len(input) < encoderInputSize {
		return false
	}

	var local EncoderProcessor
	if processor == nil {
		processor = &local
	}

	buffer := processor.scratch.base.buffer[:]
	convTmp := processor.scratch.base.convTmp[:]
	outputIndex := 0

	computeGenericDense(&m.Dense1, buffer[outputIndex:outputIndex+encoderDense1OutSize], input[:encoderInputSize], activationTanh, &processor.scratch.base)
	outputIndex += encoderDense1OutSize

	for i := range processor.state.gru {
		computeGenericGRU(&m.GRUInput[i], &m.GRURecur[i], processor.state.gru[i][:], buffer[:outputIndex], &processor.scratch.base)
		copy(buffer[outputIndex:outputIndex+encoderGRUOutSize], processor.state.gru[i][:])
		outputIndex += encoderGRUOutSize

		conv1CondInit(processor.state.conv[i][:], &processor.state.initialized)
		computeGenericDense(&m.ConvDense[i], convTmp[:encoderConvDenseOutSize], buffer[:outputIndex], activationTanh, &processor.scratch.base)
		if i == 0 {
			computeGenericConv1D(&m.Conv[i], buffer[outputIndex:outputIndex+encoderConvOutSize], processor.state.conv[i][:encoderConvInSize], convTmp[:encoderConvDenseOutSize], encoderConvOutSize, activationTanh, &processor.scratch.base)
		} else {
			computeGenericConv1DDilation(&m.Conv[i], buffer[outputIndex:outputIndex+encoderConvOutSize], processor.state.conv[i][:], convTmp[:encoderConvDenseOutSize], encoderConvOutSize, 2, activationTanh, &processor.scratch.base)
		}
		outputIndex += encoderConvOutSize
	}

	computeGenericDense(&m.ZDense, processor.scratch.paddedLatents[:], buffer[:outputIndex], activationLinear, &processor.scratch.base)
	copy(latents[:LatentDim], processor.scratch.paddedLatents[:LatentDim])

	computeGenericDense(&m.GDense1, processor.scratch.stateHidden[:], buffer[:outputIndex], activationTanh, &processor.scratch.base)
	computeGenericDense(&m.GDense2, processor.scratch.paddedState[:], processor.scratch.stateHidden[:], activationLinear, &processor.scratch.base)
	copy(initialState[:StateDim], processor.scratch.paddedState[:StateDim])
	return true
}
