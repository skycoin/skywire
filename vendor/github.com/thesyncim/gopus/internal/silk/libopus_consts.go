package silk

const (
	maxNbSubfr                   = 4
	subFrameLengthMs             = 5
	ltpMemLengthMs               = 20
	maxFsKHz                     = 16
	maxSubFrameLength            = subFrameLengthMs * maxFsKHz
	maxFrameLength               = maxSubFrameLength * maxNbSubfr
	maxSilkPacketBytes           = 1275
	maxLPCOrder                  = 16
	minLPCOrder                  = 10
	ltpOrder                     = 5
	shellCodecFrameLength        = 16
	log2ShellCodecFrameLength    = 4
	nRateLevels                  = 10
	silkMaxPulses                = 16
	maxFramesPerPacket           = 3
	MaxFramesPerPacket           = maxFramesPerPacket
	maxLPCStabilizeIterations    = 16
	maxPredictionPowerGainInvQ30 = 107374

	nLevelsQGain        = 64
	maxDeltaGainQuant   = 36
	minDeltaGainQuant   = -4
	minQGainDb          = 2
	maxQGainDb          = 88
	quantLevelAdjustQ10 = 80

	typeNoVoiceActivity = 0
	typeUnvoiced        = 1
	typeVoiced          = 2

	codeIndependently             = 0
	codeIndependentlyNoLtpScaling = 1
	codeConditionally             = 2

	nlsfQuantMaxAmplitude     = 4
	nlsfQuantMaxAmplitudeExt  = 10
	nlsfQuantDelDecStatesLog2 = 2
	nlsfQuantDelDecStates     = 1 << nlsfQuantDelDecStatesLog2
	nlsfWQ                    = 2
	bweAfterLossQ16           = 63570
	nlsfQuantLevelAdjQ10      = 102
	lsfCosTabSizeFix          = 128
	stereoInterpLenMs         = 8
	stereoRatioSmoothCoef     = 0.01

	// Stereo prediction weight quantization constants (matching libopus define.h)
	stereoQuantTabSize  = 16 // Number of main quantization levels
	stereoQuantSubSteps = 5  // Number of sub-steps per main level (total 80 levels)

	// LTP quantization constants (tuning_parameters.h)
	maxSumLogGainDB = 250.0
	ltpCorrInvMax   = 0.03

	// Pitch analysis constants (define.h / tuning_parameters.h)
	laPitchMs                   = 2
	laShapeMs                   = 5
	findPitchLpcWinMs           = 20 + (laPitchMs << 1)
	findPitchLpcWinMs2SF        = 10 + (laPitchMs << 1)
	findPitchBandwidthExpansion = 0.99
	findPitchWhiteNoiseFraction = 1e-3
	maxFindPitchLpcOrder        = 16
	silkSampleScale             = 32768.0

	// Prediction gain limits (define.h)
	maxPredictionPowerGain           = 1e4
	maxPredictionPowerGainAfterReset = 1e2

	// Delayed decision quantization limits (define.h)
	maxDelDecStates = 4

	// CNG constants (define.h)
	cngBufMaskMax           = 255
	cngGainSmthQ16          = 4634
	cngGainSmthThresholdQ16 = 46396
	cngNLSFSMthQ16          = 16348

	// int32 max (SigProc_FIX.h)
	silk_int32_MAX = int32(0x7fffffff)
)

// SilkSampleScale exposes the internal sample scaling factor used by the SILK float path.
const SilkSampleScale = silkSampleScale

// Pitch estimation constants moved to pitch_detect.go

var silk_Quantization_Offsets_Q10 = [][]int16{
	{100, 240},
	{32, 100},
}
