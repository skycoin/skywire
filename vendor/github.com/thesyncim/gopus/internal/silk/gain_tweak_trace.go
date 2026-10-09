package silk

// SILKGainTweakSnapshot captures one subframe at the gain adjustment in
// silk/float/noise_shape_analysis_FLP.c:290-294.
type SILKGainTweakSnapshot struct {
	Subframe         int32
	NumSubframes     int32
	ShapingLPCOrder  int32
	WarpingQ16       int32
	GainMultExponent float32
	GainMult         float32
	GainAdd          float32
	PreGain          float32
	PostGain         float32
}
