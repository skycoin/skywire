//go:build gopus_fixed_point

package silk

// This file ports the libopus FIXED_POINT SILK voice-activity / speech-activity
// estimator from silk/VAD.c: silk_VAD_GetSA_Q8_c, its helper
// silk_VAD_GetNoiseLevels, and the analysis filterbank silk_ana_filt_bank_1
// (silk/ana_filt_bank_1.c). It computes the 4-band analysis filterbank, the
// per-band energy, the noise-level estimation/smoothing, the SNR, and the
// SA_Q8 / input_tilt_Q15 / input_quality_bands_Q15 / NrgRatioSmth_Q8 outputs.
//
// All arithmetic is bit-exact with the FIXED_POINT reference; the fixed-point
// helper macros (silkRSHIFT, silkSMLABB, silkSMLAWB, silkSMULWB, silkSMULWW,
// silkADD_POS_SAT32, silkSQRT_APPROX via silkSqrtApproxPLC, silkSigmQ15,
// silkLin2Log, ...) are reused from libopus_fixed.go / process_gains_fixedpoint.go.

const (
	vadNBands                = 4
	vadInternalSubframesLog2 = 2
	vadInternalSubframes     = 1 << vadInternalSubframesLog2

	vadNoiseLevelSmoothCoefQ16 = 1024 // Must be < 4096
	vadNoiseLevelsBias         = 50

	// vadMinFrameLength is the smallest frame the band decimation can analyse.
	// The lowest band decimates by 8 (>>3) and is then split into
	// vadInternalSubframes (>>vadInternalSubframesLog2); the per-subframe length
	// frame_length >> (3 + vadInternalSubframesLog2) must be >= 1, which requires
	// frame_length >= 1 << (3 + vadInternalSubframesLog2). libopus only ever calls
	// the VAD with a full SILK frame, which always satisfies this.
	vadMinFrameLength = 1 << (3 + vadInternalSubframesLog2)

	vadNegativeOffsetQ5 = 128 // sigmoid is 0 at -128
	vadSNRFactorQ16     = 45000

	vadSNRSmoothCoefQ18 = 4096

	silkInt16MAX = int32(0x7fff)
	silkUint8MAX = int32(0xff)
)

// vadTiltWeights are the weighting factors for the tilt measure
// (silk/VAD.c tiltWeights).
var vadTiltWeights = [vadNBands]int32{30000, 6000, -12000, -12000}

// silkVADState mirrors the libopus silk_VAD_state struct (silk/structs.h).
type silkVADState struct {
	AnaState       [2]int32         // Analysis filterbank state: 0-8 kHz
	AnaState1      [2]int32         // Analysis filterbank state: 0-4 kHz
	AnaState2      [2]int32         // Analysis filterbank state: 0-2 kHz
	XnrgSubfr      [vadNBands]int32 // Subframe energies
	NrgRatioSmthQ8 [vadNBands]int32 // Smoothed energy level in each band
	HPstate        int16            // State of differentiator in the lowest band
	NL             [vadNBands]int32 // Noise energy level in each band
	invNL          [vadNBands]int32 // Inverse noise energy level in each band
	NoiseLevelBias [vadNBands]int32 // Noise level estimator bias/offset
	counter        int32            // Frame counter used in the initial phase
}

// silkVADInit ports silk_VAD_Init (silk/VAD.c): initializes the VAD state with
// approximate pink-noise levels and the initial smoothing counter.
func silkVADInit(s *silkVADState) {
	*s = silkVADState{}

	for b := 0; b < vadNBands; b++ {
		s.NoiseLevelBias[b] = silkMax32(silkDiv32_16(vadNoiseLevelsBias, int32(b)+1), 1)
	}
	for b := 0; b < vadNBands; b++ {
		s.NL[b] = silkMUL(100, s.NoiseLevelBias[b])
		s.invNL[b] = silkDiv32(silk_int32_MAX, s.NL[b])
	}
	s.counter = 15
	for b := 0; b < vadNBands; b++ {
		s.NrgRatioSmthQ8[b] = 100 * 256 // 100 * 256 --> 20 dB SNR
	}
}

// silkAnaFiltBank1 ports silk_ana_filt_bank_1 (silk/ana_filt_bank_1.c):
// splits the signal into two decimated bands using first-order allpass filters.
// State vector S has length 2; outL/outH each have length N/2.
func silkAnaFiltBank1(in []int16, s []int32, outL, outH []int16, n int) {
	const aFb120 = int16(5394 << 1)
	const aFb121 = int16(-24290) // (opus_int16)(20623 << 1)

	n2 := silkRSHIFT(int32(n), 1)
	for k := int32(0); k < n2; k++ {
		// Convert to Q10.
		in32 := silkLSHIFT(int32(in[2*k]), 10)

		// All-pass section for even input sample.
		Y := in32 - s[0]
		X := silkSMLAWB(Y, Y, int32(aFb121))
		out1 := s[0] + X
		s[0] = in32 + X

		// Convert to Q10.
		in32 = silkLSHIFT(int32(in[2*k+1]), 10)

		// All-pass section for odd input sample, and add to output of previous section.
		Y = in32 - s[1]
		X = silkSMULWB(Y, int32(aFb120))
		out2 := s[1] + X
		s[1] = in32 + X

		// Add/subtract, convert back to int16 and store to output.
		outL[k] = silkSAT16(silkRSHIFT_ROUND(out2+out1, 11))
		outH[k] = silkSAT16(silkRSHIFT_ROUND(out2-out1, 11))
	}
}

// silkVADGetNoiseLevels ports silk_VAD_GetNoiseLevels (silk/VAD.c): updates the
// per-band noise level estimates from the subband energies pX.
func silkVADGetNoiseLevels(pX *[vadNBands]int32, s *silkVADState) {
	var minCoef int32
	if s.counter < 1000 { // 1000 = 20 sec
		minCoef = silkDiv32_16(silkInt16MAX, silkRSHIFT(s.counter, 4)+1)
		s.counter++
	} else {
		minCoef = 0
	}

	for k := 0; k < vadNBands; k++ {
		nl := s.NL[k]

		// Add bias.
		nrg := silkAddPosSat32(pX[k], s.NoiseLevelBias[k])

		// Invert energies.
		invNrg := silkDiv32(silk_int32_MAX, nrg)

		// Less update when subband energy is high.
		var coef int32
		switch {
		case nrg > silkLSHIFT(nl, 3):
			coef = vadNoiseLevelSmoothCoefQ16 >> 3
		case nrg < nl:
			coef = vadNoiseLevelSmoothCoefQ16
		default:
			coef = silkSMULWB(silkSMULWW(invNrg, nl), vadNoiseLevelSmoothCoefQ16<<1)
		}

		// Initially faster smoothing.
		if coef < minCoef {
			coef = minCoef
		}

		// Smooth inverse energies.
		s.invNL[k] = silkSMLAWB(s.invNL[k], invNrg-s.invNL[k], coef)

		// Compute noise level by inverting again.
		nl = silkDiv32(silk_int32_MAX, s.invNL[k])

		// Limit noise levels (guarantee 7 bits of head room).
		if nl > 0x00FFFFFF {
			nl = 0x00FFFFFF
		}

		s.NL[k] = nl
	}
}

// silkVADResult holds the per-frame outputs of silk_VAD_GetSA_Q8.
type silkVADResult struct {
	speechActivityQ8     int32
	inputTiltQ15         int32
	inputQualityBandsQ15 [vadNBands]int32
}

// silkVADGetSAQ8 ports silk_VAD_GetSA_Q8_c (silk/VAD.c): the speech-activity
// estimator. frameLength must be a multiple of 8 and <= 512; fsKHz is the
// internal sampling rate in kHz. The VAD state s is updated in place.
func silkVADGetSAQ8(sc *silkFixedEncodeScratch, s *silkVADState, pIn []int16, frameLength, fsKHz int) silkVADResult {
	var res silkVADResult

	// The band decimation collapses a sub-vadMinFrameLength frame to a
	// zero-length lowest band, which indexes X[-1] below. libopus never feeds
	// the VAD such a frame; report no speech activity instead of reading past
	// the start of the band, matching the float VADState.getSpeechActivity guard.
	if frameLength < vadMinFrameLength || len(pIn) < frameLength {
		return res
	}

	// Filter and decimate.
	decimatedFramelength1 := int(silkRSHIFT(int32(frameLength), 1))
	decimatedFramelength2 := int(silkRSHIFT(int32(frameLength), 2))
	decimatedFramelength := int(silkRSHIFT(int32(frameLength), 3))

	// Decimate into 4 bands. The layout is arranged to allow the minimal
	// (frame_length / 4) extra scratch space during downsampling.
	var xOffset [vadNBands]int
	xOffset[0] = 0
	xOffset[1] = decimatedFramelength + decimatedFramelength2
	xOffset[2] = xOffset[1] + decimatedFramelength
	xOffset[3] = xOffset[2] + decimatedFramelength2
	X := ensureInt16Slice(&sc.vadX, xOffset[3]+decimatedFramelength1)

	// 0-8 kHz to 0-4 kHz and 4-8 kHz.
	silkAnaFiltBank1(pIn, s.AnaState[:], X, X[xOffset[3]:], frameLength)

	// 0-4 kHz to 0-2 kHz and 2-4 kHz.
	silkAnaFiltBank1(X, s.AnaState1[:], X, X[xOffset[2]:], decimatedFramelength1)

	// 0-2 kHz to 0-1 kHz and 1-2 kHz.
	silkAnaFiltBank1(X, s.AnaState2[:], X, X[xOffset[1]:], decimatedFramelength2)

	// HP filter on lowest band (differentiator).
	X[decimatedFramelength-1] = int16(silkRSHIFT(int32(X[decimatedFramelength-1]), 1))
	HPstateTmp := X[decimatedFramelength-1]
	for i := decimatedFramelength - 1; i > 0; i-- {
		X[i-1] = int16(silkRSHIFT(int32(X[i-1]), 1))
		X[i] -= X[i-1]
	}
	X[0] -= s.HPstate
	s.HPstate = HPstateTmp

	// Calculate the energy in each band.
	var Xnrg [vadNBands]int32
	var sumSquared int32
	for b := 0; b < vadNBands; b++ {
		// Find the decimated framelength in the non-uniformly divided bands.
		decFramelen := int(silkRSHIFT(int32(frameLength), silkMinInt(vadNBands-b, vadNBands-1)))

		// Split length into subframe lengths.
		decSubframeLength := int(silkRSHIFT(int32(decFramelen), vadInternalSubframesLog2))
		decSubframeOffset := 0

		// Compute energy per sub-frame, initialized with the summed energy of
		// the last subframe of the previous call.
		Xnrg[b] = s.XnrgSubfr[b]
		for sf := 0; sf < vadInternalSubframes; sf++ {
			sumSquared = 0
			for i := 0; i < decSubframeLength; i++ {
				xTmp := silkRSHIFT(int32(X[xOffset[b]+i+decSubframeOffset]), 3)
				sumSquared = silkSMLABB(sumSquared, xTmp, xTmp)
			}

			// Add/saturate summed energy of current subframe.
			if sf < vadInternalSubframes-1 {
				Xnrg[b] = silkAddPosSat32(Xnrg[b], sumSquared)
			} else {
				// Look-ahead subframe.
				Xnrg[b] = silkAddPosSat32(Xnrg[b], silkRSHIFT(sumSquared, 1))
			}

			decSubframeOffset += decSubframeLength
		}
		s.XnrgSubfr[b] = sumSquared
	}

	// Noise estimation.
	silkVADGetNoiseLevels(&Xnrg, s)

	// Signal-plus-noise to noise ratio estimation.
	sumSquared = 0
	inputTilt := int32(0)
	var nrgToNoiseRatioQ8 [vadNBands]int32
	for b := 0; b < vadNBands; b++ {
		speechNrg := Xnrg[b] - s.NL[b]
		if speechNrg > 0 {
			// Divide, with sufficient resolution.
			if (Xnrg[b] & int32(-0x00800000)) == 0 {
				nrgToNoiseRatioQ8[b] = silkDiv32(silkLSHIFT(Xnrg[b], 8), s.NL[b]+1)
			} else {
				nrgToNoiseRatioQ8[b] = silkDiv32(Xnrg[b], silkRSHIFT(s.NL[b], 8)+1)
			}

			// Convert to log domain.
			snrQ7 := silkLin2Log(nrgToNoiseRatioQ8[b]) - 8*128

			// Sum-of-squares (Q14).
			sumSquared = silkSMLABB(sumSquared, snrQ7, snrQ7)

			// Tilt measure.
			if speechNrg < (int32(1) << 20) {
				// Scale down SNR value for small subband speech energies.
				snrQ7 = silkSMULWB(silkLSHIFT(silkSqrtApproxPLC(speechNrg), 6), snrQ7)
			}
			inputTilt = silkSMLAWB(inputTilt, vadTiltWeights[b], snrQ7)
		} else {
			nrgToNoiseRatioQ8[b] = 256
		}
	}

	// Mean-of-squares (Q14).
	sumSquared = silkDiv32_16(sumSquared, vadNBands)

	// Root-mean-square approximation, scale to dBs (Q7).
	pSNRdBQ7 := int32(int16(3 * silkSqrtApproxPLC(sumSquared)))

	// Speech Probability Estimation.
	saQ15 := silkSigmQ15(silkSMULWB(vadSNRFactorQ16, pSNRdBQ7) - vadNegativeOffsetQ5)

	// Frequency Tilt Measure.
	res.inputTiltQ15 = silkLSHIFT(silkSigmQ15(inputTilt)-16384, 1)

	// Scale the sigmoid output based on power levels.
	speechNrg := int32(0)
	for b := 0; b < vadNBands; b++ {
		// Accumulate signal-without-noise energies; higher frequency bands
		// have more weight.
		speechNrg += int32(b+1) * silkRSHIFT(Xnrg[b]-s.NL[b], 4)
	}

	if frameLength == 20*fsKHz {
		speechNrg = silkRSHIFT(speechNrg, 1)
	}
	// Power scaling.
	if speechNrg <= 0 {
		saQ15 = silkRSHIFT(saQ15, 1)
	} else if speechNrg < 16384 {
		speechNrg = silkLSHIFT(speechNrg, 16)
		// Square-root.
		speechNrg = silkSqrtApproxPLC(speechNrg)
		saQ15 = silkSMULWB(32768+speechNrg, saQ15)
	}

	// Copy the resulting speech activity in Q8.
	res.speechActivityQ8 = silkRSHIFT(saQ15, 7)
	if res.speechActivityQ8 > silkUint8MAX {
		res.speechActivityQ8 = silkUint8MAX
	}

	// Energy Level and SNR estimation.
	// Smoothing coefficient.
	smoothCoefQ16 := silkSMULWB(vadSNRSmoothCoefQ18, silkSMULWB(saQ15, saQ15))

	if frameLength == 10*fsKHz {
		smoothCoefQ16 >>= 1
	}

	for b := 0; b < vadNBands; b++ {
		// Compute smoothed energy-to-noise ratio per band.
		s.NrgRatioSmthQ8[b] = silkSMLAWB(s.NrgRatioSmthQ8[b],
			nrgToNoiseRatioQ8[b]-s.NrgRatioSmthQ8[b], smoothCoefQ16)

		// Signal-to-noise ratio in dB per band.
		snrQ7 := 3 * (silkLin2Log(s.NrgRatioSmthQ8[b]) - 8*128)
		// quality = sigmoid( 0.25 * ( SNR_dB - 16 ) ).
		res.inputQualityBandsQ15[b] = silkSigmQ15(silkRSHIFT(snrQ7-16*128, 4))
	}

	return res
}
