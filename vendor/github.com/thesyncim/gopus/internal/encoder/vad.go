// This file implements multi-band Voice Activity Detection (VAD) for DTX,
// matching libopus SILK VAD behavior from silk/VAD.c.
//
// The VAD splits the signal into 4 frequency bands using analysis filter banks:
//   - Band 0: 0-1 kHz
//   - Band 1: 1-2 kHz
//   - Band 2: 2-4 kHz
//   - Band 3: 4-8 kHz
//
// Each band's energy is compared against adaptive noise estimates to determine
// speech activity probability.
//
// Reference: libopus silk/VAD.c, silk/define.h

package encoder

import (
	"math"
	"math/bits"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// VAD Constants matching libopus silk/define.h
const (
	// VADNBands is the number of frequency bands for VAD analysis.
	VADNBands = 4

	// VADInternalSubframesLog2 is log2 of the number of internal subframes.
	VADInternalSubframesLog2 = 2
	// VADInternalSubframes is the number of internal subframes (1 << 2 = 4).
	VADInternalSubframes = 1 << VADInternalSubframesLog2

	// VADMinFrameLength is the smallest frame the band decimation can analyse.
	// The lowest band decimates the frame by 8 (>>3) and then splits it into
	// VADInternalSubframes subframes (>>VADInternalSubframesLog2), so the
	// per-subframe length is frameLength >> (3+VADInternalSubframesLog2). That
	// quotient must be at least 1, which requires frameLength >= 1<<5.
	VADMinFrameLength = 1 << (3 + VADInternalSubframesLog2)

	// VADNoiseLevelSmoothCoefQ16 is the noise level smoothing coefficient.
	// Must be < 4096.
	VADNoiseLevelSmoothCoefQ16 = 1024
	// VADNoiseLevelsBias is the bias for noise level estimation.
	VADNoiseLevelsBias = 50

	// VADNegativeOffsetQ5 is the sigmoid offset (sigmoid is 0 at -128).
	VADNegativeOffsetQ5 = 128
	// VADSNRFactorQ16 is the SNR scaling factor.
	VADSNRFactorQ16 = 45000

	// VADSNRSmoothCoefQ18 is the smoothing coefficient for SNR measurement.
	VADSNRSmoothCoefQ18 = 4096

	// DTXActivityThreshold is the activity probability threshold for DTX.
	// Matches libopus DTX_ACTIVITY_THRESHOLD = 0.1f
	DTXActivityThreshold = 0.1
	// speechActivityThresholdQ8 is the SILK VAD/no-voice threshold in Q8.
	// Matches libopus SPEECH_ACTIVITY_DTX_THRES = 0.05f (silk/tuning_parameters.h),
	// i.e. SILK_FIX_CONST(0.05, 8) = 13.
	speechActivityThresholdQ8 = 13

	// NBSpeechFramesBeforeDTX is frames of speech required before DTX (200ms).
	NBSpeechFramesBeforeDTX = 10
	// MaxConsecutiveDTX is maximum consecutive DTX frames (400ms).
	MaxConsecutiveDTX = 20
)

// Tilt weights for spectral analysis (matching libopus tiltWeights).
var vadTiltWeights = [VADNBands]int32{30000, 6000, -12000, -12000}

// Analysis filter bank coefficients (matching silk/ana_filt_bank_1.c).
const (
	aFB1_20 = 5394 << 1 // 10788
	aFB1_21 = -24290    // (20623 << 1) cast to int16
)

// VADState holds the state for Voice Activity Detection.
// This mirrors silk_VAD_state from libopus silk/structs.h.
type VADState struct {
	// Analysis filterbank states
	AnaState  [2]int32 // 0-8 kHz state
	AnaState1 [2]int32 // 0-4 kHz state
	AnaState2 [2]int32 // 0-2 kHz state

	// Subframe energies from previous frame
	XnrgSubfr [VADNBands]int32

	// Smoothed energy-to-noise ratio per band (Q8)
	NrgRatioSmthQ8 [VADNBands]int32

	// High-pass filter state for lowest band differentiator
	HPState int16

	// Noise energy level per band
	NL [VADNBands]int32

	// Inverse noise energy level per band
	InvNL [VADNBands]int32

	// Noise level estimator bias per band
	NoiseLevelBias [VADNBands]int32

	// Frame counter for initial faster adaptation
	Counter int32

	// Speech activity probability (Q8, 0-255)
	SpeechActivityQ8 int32

	// Input quality bands (Q15)
	InputQualityBandsQ15 [VADNBands]int32

	// Input tilt (Q15)
	InputTiltQ15 int32

	// Hangover counter for smooth transitions
	HangoverCount int

	// Previous activity decision for hysteresis
	PrevActivity bool

	// Scratch buffers for zero-allocation processing
	scratchInput []int16
	scratchX     []int16
}

// NewVADState creates and initializes a new VAD state.
// Matches silk_VAD_Init from libopus.
func NewVADState() *VADState {
	v := &VADState{}
	v.Reset()
	return v
}

// Reset initializes VAD state to default values.
// Matches silk_VAD_Init from libopus silk/VAD.c.
func (v *VADState) Reset() {
	// Initialize noise level bias (approx pink noise - PSD proportional to 1/f)
	for b := range VADNBands {
		v.NoiseLevelBias[b] = max(VADNoiseLevelsBias/(int32(b)+1), 1)
	}

	// Initialize state with noise estimates
	for b := range VADNBands {
		v.NL[b] = 100 * v.NoiseLevelBias[b]
		if v.NL[b] > 0 {
			v.InvNL[b] = math.MaxInt32 / v.NL[b]
		} else {
			v.InvNL[b] = math.MaxInt32
		}
	}

	// Frame counter starts at 15 for initial faster smoothing
	v.Counter = 15

	// Initialize smoothed energy-to-noise ratio to 20 dB SNR (100 * 256)
	for b := range VADNBands {
		v.NrgRatioSmthQ8[b] = 100 * 256
	}

	// Clear other state
	v.AnaState = [2]int32{}
	v.AnaState1 = [2]int32{}
	v.AnaState2 = [2]int32{}
	v.XnrgSubfr = [VADNBands]int32{}
	v.HPState = 0
	v.SpeechActivityQ8 = 0
	v.InputQualityBandsQ15 = [VADNBands]int32{}
	v.InputTiltQ15 = 0
	v.HangoverCount = 0
	v.PrevActivity = false
}

// GetSpeechActivity analyzes the input signal and returns speech activity level.
// This is the main VAD function matching silk_VAD_GetSA_Q8 from libopus.
//
// Parameters:
//   - pcm: Input PCM samples (mono, 16-bit range scaled to int16)
//   - frameLength: Number of samples in the frame
//   - fsKHz: Sample rate in kHz (8, 12, or 16)
//
// Returns:
//   - activityQ8: Speech activity level (0-255, Q8)
//   - isActive: True if speech activity detected
func (v *VADState) GetSpeechActivity(pcm []float32, frameLength int, fsKHz int) (int, bool) {
	return v.getSpeechActivityFast(pcm, frameLength, fsKHz)
}

// getSpeechActivityFast is the hot path used by normal VAD callers.
// It keeps the fixed-point math identical to getSpeechActivity.
func (v *VADState) getSpeechActivityFast(pcm []float32, frameLength int, fsKHz int) (int, bool) {
	// The band decimation needs at least VADMinFrameLength samples (otherwise a
	// band subframe length collapses to zero). libopus only ever calls the VAD
	// with full SILK frames, which always satisfy this; reject anything shorter
	// rather than indexing past the start of a zero-length subframe.
	if frameLength < VADMinFrameLength || len(pcm) < frameLength {
		return 0, false
	}

	// Convert float32 samples to int16 for fixed-point processing
	if cap(v.scratchInput) < frameLength {
		v.scratchInput = make([]int16, frameLength)
	}
	input := v.scratchInput[:frameLength]
	_ = pcm[frameLength-1]
	_ = input[frameLength-1]
	for i := range frameLength {
		input[i] = opusmath.Float32ToInt16Raw(pcm[i] * 32768.0)
	}

	// Calculate decimated frame lengths
	decimatedFrameLength1 := frameLength >> 1 // frame_length / 2
	decimatedFrameLength2 := frameLength >> 2 // frame_length / 4
	decimatedFrameLength := frameLength >> 3  // frame_length / 8

	// Allocate workspace for decimated signals
	// Layout: [0-1kHz][temp][1-2kHz][2-4kHz][4-8kHz]
	xOffset := [VADNBands]int{
		0,
		decimatedFrameLength + decimatedFrameLength2,
		decimatedFrameLength + decimatedFrameLength2 + decimatedFrameLength,
		decimatedFrameLength + decimatedFrameLength2 + decimatedFrameLength + decimatedFrameLength2,
	}
	xLen := xOffset[3] + decimatedFrameLength1
	if cap(v.scratchX) < xLen {
		v.scratchX = make([]int16, xLen)
	}
	X := v.scratchX[:xLen]

	// Filter and decimate into 4 bands

	// 0-8 kHz to 0-4 kHz and 4-8 kHz
	anaFiltBank1Exact(input, &v.AnaState, X[:decimatedFrameLength1], X[xOffset[3]:xOffset[3]+decimatedFrameLength1])

	// 0-4 kHz to 0-2 kHz and 2-4 kHz
	anaFiltBank1Exact(X[:decimatedFrameLength1], &v.AnaState1, X[:decimatedFrameLength2], X[xOffset[2]:xOffset[2]+decimatedFrameLength2])

	// 0-2 kHz to 0-1 kHz and 1-2 kHz
	anaFiltBank1Exact(X[:decimatedFrameLength2], &v.AnaState2, X[:decimatedFrameLength], X[xOffset[1]:xOffset[1]+decimatedFrameLength])

	// HP filter on lowest band (differentiator)
	X[decimatedFrameLength-1] = X[decimatedFrameLength-1] >> 1
	hpStateTmp := X[decimatedFrameLength-1]
	for i := decimatedFrameLength - 1; i > 0; i-- {
		X[i-1] = X[i-1] >> 1
		X[i] -= X[i-1]
	}
	X[0] -= v.HPState
	v.HPState = hpStateTmp

	// Calculate energy in each band
	Xnrg := [VADNBands]int32{}
	decLenByBand := [VADNBands]int{
		decimatedFrameLength,
		decimatedFrameLength,
		decimatedFrameLength2,
		decimatedFrameLength1,
	}
	for b := range VADNBands {
		decLen := decLenByBand[b]
		band := X[xOffset[b] : xOffset[b]+decLen]
		// Split into subframes
		decSubframeLength := decLen >> VADInternalSubframesLog2
		decSubframeOffset := 0

		// Initialize with energy from previous frame's last subframe
		Xnrg[b] = v.XnrgSubfr[b]

		var sumSquared int32
		for s := range VADInternalSubframes {
			sumSquared = 0
			subframe := band[decSubframeOffset : decSubframeOffset+decSubframeLength]
			_ = subframe[decSubframeLength-1]
			for i := range decSubframeLength {
				xTmp := int32(subframe[i]) >> 3
				sumSquared += xTmp * xTmp
			}

			// Add/saturate energy
			if s < VADInternalSubframes-1 {
				Xnrg[b] = addPosSat32(Xnrg[b], sumSquared)
			} else {
				// Look-ahead subframe gets half weight
				Xnrg[b] = addPosSat32(Xnrg[b], sumSquared>>1)
			}

			decSubframeOffset += decSubframeLength
		}
		v.XnrgSubfr[b] = sumSquared
	}

	// Noise estimation
	v.getNoiseLevels(Xnrg[:])

	// Signal-plus-noise to noise ratio estimation
	var sumSquared int32
	var inputTilt int32
	NrgToNoiseRatioQ8 := [VADNBands]int32{}

	for b := range VADNBands {
		speechNrg := Xnrg[b] - v.NL[b]
		if speechNrg > 0 {
			// Compute energy to noise ratio
			if (uint32(Xnrg[b]) & 0xFF800000) == 0 {
				NrgToNoiseRatioQ8[b] = (Xnrg[b] << 8) / (v.NL[b] + 1)
			} else {
				NrgToNoiseRatioQ8[b] = Xnrg[b] / ((v.NL[b] >> 8) + 1)
			}

			// Convert to log domain
			snrQ7 := lin2log(NrgToNoiseRatioQ8[b]) - 8*128

			// Sum-of-squares for mean calculation
			sumSquared = smlabb(sumSquared, snrQ7, snrQ7)

			// Tilt measure
			snrForTilt := snrQ7
			if speechNrg < (1 << 20) {
				// Scale down SNR for small speech energies
				snrForTilt = smulwb(int32(sqrtApprox(speechNrg)<<6), snrQ7)
			}
			inputTilt = smlawb(inputTilt, vadTiltWeights[b], snrForTilt)
		} else {
			NrgToNoiseRatioQ8[b] = 256 // 1.0 in Q8
		}
	}

	// Mean-of-squares
	sumSquared /= VADNBands

	// Root-mean-square approximation, scale to dBs
	pSNRdBQ7 := int32(int16(3 * sqrtApprox(sumSquared)))

	// Speech probability estimation using sigmoid
	saQ15 := sigmQ15(smulwb(VADSNRFactorQ16, pSNRdBQ7) - VADNegativeOffsetQ5)

	// Frequency tilt measure
	v.InputTiltQ15 = (sigmQ15(inputTilt) - 16384) << 1

	// Scale sigmoid output based on power levels
	var speechNrg int32
	for b := range VADNBands {
		// Higher frequency bands have more weight
		speechNrg += int32(b+1) * ((Xnrg[b] - v.NL[b]) >> 4)
	}

	// Adjust for frame length (20ms has half the energy of 10ms)
	if frameLength == 20*fsKHz {
		speechNrg >>= 1
	}

	// Power scaling
	if speechNrg <= 0 {
		saQ15 >>= 1
	} else if speechNrg < 16384 {
		speechNrg <<= 16
		speechNrg = sqrtApprox(speechNrg)
		saQ15 = smulwb(32768+speechNrg, saQ15)
	}

	// Convert to Q8 (0-255) and clamp
	speechActivityQ8 := min(saQ15>>7, 255)
	v.SpeechActivityQ8 = speechActivityQ8

	// Smoothing coefficient based on activity
	tmp := smulwb(int32(saQ15), int32(saQ15))
	smoothCoefQ16 := smulwb(VADSNRSmoothCoefQ18, tmp)

	if frameLength == 10*fsKHz {
		smoothCoefQ16 >>= 1
	}

	// Update smoothed energy-to-noise ratios and quality bands
	for b := range VADNBands {
		// Smooth energy-to-noise ratio
		v.NrgRatioSmthQ8[b] = smlawb(v.NrgRatioSmthQ8[b], NrgToNoiseRatioQ8[b]-v.NrgRatioSmthQ8[b], smoothCoefQ16)

		// SNR in dB per band
		snrQ7 := int32(3) * (lin2log(v.NrgRatioSmthQ8[b]) - 8*128)
		// Quality = sigmoid(0.25 * (SNR_dB - 16))
		v.InputQualityBandsQ15[b] = sigmQ15((snrQ7 - 16*128) >> 4)
	}

	// Activity decision (matches libopus: compare speech_activity_Q8 to threshold)
	isActive := v.SpeechActivityQ8 >= speechActivityThresholdQ8

	return int(v.SpeechActivityQ8), isActive
}

// getNoiseLevels updates noise level estimates for each band.
// Matches silk_VAD_GetNoiseLevels from libopus.
func (v *VADState) getNoiseLevels(pX []int32) {
	// Initially faster smoothing during first 20 seconds
	var minCoef int32
	if v.Counter < 1000 {
		minCoef = 32767 / ((v.Counter >> 4) + 1)
		v.Counter++
	} else {
		minCoef = 0
	}

	for k := range VADNBands {
		// Get old noise level estimate
		nl := v.NL[k]

		// Add bias
		nrg := addPosSat32(pX[k], v.NoiseLevelBias[k])
		if nrg <= 0 {
			nrg = 1
		}

		// Invert energy
		invNrg := int32(math.MaxInt32) / nrg

		// Adaptive smoothing coefficient based on energy vs noise level
		var coef int32
		if nrg > (nl << 3) {
			// Much higher than noise - slow update
			coef = VADNoiseLevelSmoothCoefQ16 >> 3
		} else if nrg < nl {
			// Below noise level - fast update
			coef = VADNoiseLevelSmoothCoefQ16
		} else {
			// In between - interpolate
			coef = smulwb(smulww(invNrg, nl), VADNoiseLevelSmoothCoefQ16<<1)
		}

		// Initially faster smoothing
		if coef < minCoef {
			coef = minCoef
		}

		// Smooth inverse energies
		v.InvNL[k] = max(smlawb(v.InvNL[k], invNrg-v.InvNL[k], coef), 0)

		// Compute noise level by inverting again
		if v.InvNL[k] > 0 {
			nl = int32(math.MaxInt32) / v.InvNL[k]
		} else {
			nl = int32(math.MaxInt32)
		}
		if nl < 0 {
			nl = 0
		}

		// Limit noise levels (guarantee 7 bits of headroom)
		if nl > 0x00FFFFFF {
			nl = 0x00FFFFFF
		}

		v.NL[k] = nl
	}
}

// anaFiltBank1 splits signal into two decimated bands using first-order allpass filters.
// Matches silk_ana_filt_bank_1 from libopus silk/ana_filt_bank_1.c.
func anaFiltBank1(in []int16, S *[2]int32, outL, outH []int16, N int) {
	N2 := N >> 1
	if N2 == 0 {
		return
	}

	// Clamp N2 to available buffer lengths.
	if 2*N2 > len(in) {
		N2 = len(in) / 2
	}
	if N2 > len(outL) {
		N2 = len(outL)
	}
	if N2 > len(outH) {
		N2 = len(outH)
	}
	if N2 == 0 {
		return
	}

	// Trim slices to exact required lengths for BCE.
	anaFiltBank1Exact(in[:2*N2], S, outL[:N2], outH[:N2])
}

// anaFiltBank1Exact is the VAD hot path version of the libopus analysis filter.
// Callers provide exact-length slices, so it avoids the wrapper's clamp work.
func anaFiltBank1Exact(in []int16, S *[2]int32, outL, outH []int16) {
	N2 := len(outL)
	if N2 == 0 {
		return
	}
	_ = in[2*N2-1]
	_ = outL[N2-1]
	_ = outH[N2-1]

	// Cache allpass coefficients as int64 to avoid repeated conversions.
	coefEven := int64(int16(aFB1_21))
	coefOdd := int64(int16(aFB1_20))

	s0, s1 := S[0], S[1]

	// Internal variables and state are in Q10 format.
	for k, inIdx := 0, 0; k < N2; k++ {
		// Convert to Q10
		in32 := int32(in[inIdx]) << 10
		inIdx++

		// All-pass section for even input sample
		Y := in32 - s0
		X := Y + int32((int64(Y)*coefEven)>>16)
		out1 := s0 + X
		s0 = in32 + X

		// Convert to Q10
		in32 = int32(in[inIdx]) << 10
		inIdx++

		// All-pass section for odd input sample
		Y = in32 - s1
		X = int32((int64(Y) * coefOdd) >> 16)
		out2 := s1 + X
		s1 = in32 + X

		// Add/subtract, convert back to int16 and store
		sum := rshiftRound(out2+out1, 11)
		diff := rshiftRound(out2-out1, 11)
		if uint32(sum+32768) <= 65535 && uint32(diff+32768) <= 65535 {
			outL[k] = int16(sum)
			outH[k] = int16(diff)
			continue
		}
		outL[k] = satInt16(sum)
		outH[k] = satInt16(diff)
	}

	S[0], S[1] = s0, s1
}

// Helper functions for fixed-point arithmetic

func smulbb(a, b int32) int32 {
	return int32(int64(int16(a)) * int64(int16(b)))
}

func smulwb(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

func smulww(a, b int32) int32 {
	// Use the OPUS_FAST_INT64 path matching libopus on 64-bit platforms:
	// (opus_int32)(((opus_int64)(a) * (b)) >> 16)
	return int32((int64(a) * int64(b)) >> 16)
}

func smlawb(a, b, c int32) int32 {
	return a + smulwb(b, c)
}

func smlabb(a, b, c int32) int32 {
	return a + smulbb(b, c)
}

func rshiftRound(a int32, shift int) int32 {
	if shift <= 0 {
		return a
	}
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}

var sigmLUTSlopeQ10 = [6]int32{237, 153, 73, 30, 12, 7}
var sigmLUTPosQ15 = [6]int32{16384, 23955, 28861, 31213, 32178, 32548}
var sigmLUTNegQ15 = [6]int32{16384, 8812, 3906, 1554, 589, 219}

func sigmQ15(inQ5 int32) int32 {
	if inQ5 < 0 {
		/* Negative input */
		inQ5 = -inQ5
		if inQ5 >= 6*32 {
			return 0 /* Clip */
		} else {
			/* Linear interpolation of look up table */
			ind := inQ5 >> 5
			return sigmLUTNegQ15[ind] - sigmLUTSlopeQ10[ind]*(inQ5&0x1F)
		}
	} else {
		/* Positive input */
		if inQ5 >= 6*32 {
			return 32767 /* clip */
		} else {
			/* Linear interpolation of look up table */
			ind := inQ5 >> 5
			return sigmLUTPosQ15[ind] + sigmLUTSlopeQ10[ind]*(inQ5&0x1F)
		}
	}
}

// lin2log converts linear value to log domain.
// Approximation of 128 * log2(x)
// Matches silk_lin2log from libopus.
func lin2log(inLin int32) int32 {
	if inLin <= 0 {
		return 0
	}
	lz := int32(bits.LeadingZeros32(uint32(inLin)))
	fracQ7 := int32(bits.RotateLeft32(uint32(inLin), -int(24-lz)) & 0x7f)

	return (((fracQ7 * (128 - fracQ7) * 179) >> 16) + fracQ7) + ((31 - lz) << 7)
}

// sqrtApprox computes approximate integer square root.
func sqrtApprox(x int32) int32 {
	if x <= 0 {
		return 0
	}
	lz := int32(bits.LeadingZeros32(uint32(x)))
	fracQ7 := int32(bits.RotateLeft32(uint32(x), -int(24-lz)) & 0x7f)

	var y int32
	if lz&1 != 0 {
		y = 32768
	} else {
		y = 46214 // sqrt(2) * 32768
	}
	y >>= (lz >> 1)

	// y = y + (y * (213 * fracQ7)) >> 16
	y += int32((int64(y) * int64(213*fracQ7)) >> 16)
	return y
}

// addPosSat32 adds two positive int32 values with saturation.
func addPosSat32(a, b int32) int32 {
	sum := a + b
	if sum < a || sum < b {
		return math.MaxInt32
	}
	return sum
}

// satInt16 saturates an int32 to int16 range.
func satInt16(x int32) int16 {
	if uint32(x+32768) <= 65535 {
		return int16(x)
	}
	if x > 0 {
		return 32767
	}
	return -32768
}
