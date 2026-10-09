package silk

// SILKNLSFInterpolationCandidateSnapshot captures one candidate in the active
// float encoder's NLSF interpolation search. EnergyFirst and EnergySecond use
// the C double width returned by silk_energy_FLP in silk/float/energy_FLP.c;
// their sum is cast to silk_float in silk/float/find_LPC_FLP.c.
type SILKNLSFInterpolationCandidateSnapshot struct {
	InterpIndex    int32
	NLSFQ15        [maxLPCOrder]int16
	LPCQ12         [maxLPCOrder]int16
	EnergyFirst    silkCReal
	EnergySecond   silkCReal
	ResidualEnergy float32
}

// SILKNLSFInterpolationSnapshot captures the bounded operands and results used
// by one silk_find_LPC_FLP interpolation search. Input is valid only during the
// callback; copy it there when retaining the trace.
type SILKNLSFInterpolationSnapshot struct {
	FrameInPacket int32 // SILK frame index within the current packet.
	Order         int
	SubframeLen   int
	NumSubframes  int
	MinInvGain    float32
	Input         []float32
	PrevNLSFQ15   [maxLPCOrder]int16

	FullBurgCoefficients [maxLPCOrder]float32
	FullResidualEnergy   float32
	LastBurgCoefficients [maxLPCOrder]float32
	LastResidualEnergy   float32
	LastNLSFQ15          [maxLPCOrder]int16

	Candidates     [4]SILKNLSFInterpolationCandidateSnapshot
	CandidateCount int
	SelectedIndex  int32
}
