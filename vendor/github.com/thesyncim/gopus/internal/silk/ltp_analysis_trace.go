package silk

// SILKLTPAnalysisTraceSnapshot exposes the bounded operands and result of one
// encoder LTP-residual build to parity tests. PitchBuffer and Residual are
// valid only during the callback; copy any values the callback retains.
type SILKLTPAnalysisTraceSnapshot struct {
	FrameInPacket   int32
	FrameStart      int // buffer index, not a libopus state value
	SignalType      int32
	Order           int32
	SubframeSamples int32
	NumSubframes    int32
	Scale           float32
	PitchBuffer     []float32
	Gains           [maxNbSubfr]float32
	InvGains        [maxNbSubfr]float32
	PitchLags       [maxNbSubfr]int32
	Taps            [maxNbSubfr][ltpOrderConst]float32
	Residual        []float32
}
