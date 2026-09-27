package silk

// LP variable cutoff filter for smooth bandwidth transitions.
// Port of libopus silk/LP_variable_cutoff.c and silk/biquad_alt.c.
//
// The filter uses elliptic/Cauer design with 0.1 dB passband ripple,
// 80 dB minimum stopband attenuation, and interpolated cutoff frequencies.

const (
	transitionNB       = 3 // Number of B coefficients (numerator)
	transitionNA       = 2 // Number of A coefficients (denominator)
	transitionIntNum   = 5 // Number of interpolation points
	transitionTimeMsLP = 5120
	maxFrameLengthMs   = 20
	transitionFrames   = transitionTimeMsLP / maxFrameLengthMs     // 256
	transitionIntSteps = transitionFrames / (transitionIntNum - 1) // 64
)

// LPState holds the LP variable cutoff filter state.
type LPState struct {
	InLPState         [2]int32 // Biquad filter state
	TransitionFrameNo int32    // Counter mapped to cutoff frequency
	Mode              int      // Operating mode: <0=switch down, >0=switch up, 0=do nothing
	SavedFsKHz        int32    // Last sampling rate before bandwidth switching reset
}

// Transition LP filter coefficient tables (Q28 format).
// From libopus silk/tables_other.c.
var silkTransitionLPBQ28 = [transitionIntNum][transitionNB]int32{
	{250767114, 501534038, 250767114},
	{209867381, 419732057, 209867381},
	{170987846, 341967853, 170987846},
	{131531482, 263046905, 131531482},
	{89306658, 178584282, 89306658},
}

var silkTransitionLPAQ28 = [transitionIntNum][transitionNA]int32{
	{506393414, 239854379},
	{411067935, 169683996},
	{306733530, 116694253},
	{185807084, 77959395},
	{35497197, 57401098},
}

// lpInterpolateFilterTaps interpolates the filter taps between two points.
// Matches libopus silk_LP_interpolate_filter_taps.
func lpInterpolateFilterTaps(bQ28 *[transitionNB]int32, aQ28 *[transitionNA]int32, ind int, facQ16 int32) {
	if ind < transitionIntNum-1 {
		if facQ16 > 0 {
			if facQ16 < 32768 { // facQ16 is in range of a 16-bit int
				for nb := range transitionNB {
					bQ28[nb] = silkSMLAWB(
						silkTransitionLPBQ28[ind][nb],
						silkTransitionLPBQ28[ind+1][nb]-silkTransitionLPBQ28[ind][nb],
						facQ16)
				}
				for na := range transitionNA {
					aQ28[na] = silkSMLAWB(
						silkTransitionLPAQ28[ind][na],
						silkTransitionLPAQ28[ind+1][na]-silkTransitionLPAQ28[ind][na],
						facQ16)
				}
			} else { // (facQ16 - (1<<16)) is in range of a 16-bit int
				for nb := range transitionNB {
					bQ28[nb] = silkSMLAWB(
						silkTransitionLPBQ28[ind+1][nb],
						silkTransitionLPBQ28[ind+1][nb]-silkTransitionLPBQ28[ind][nb],
						facQ16-int32(1<<16))
				}
				for na := range transitionNA {
					aQ28[na] = silkSMLAWB(
						silkTransitionLPAQ28[ind+1][na],
						silkTransitionLPAQ28[ind+1][na]-silkTransitionLPAQ28[ind][na],
						facQ16-int32(1<<16))
				}
			}
		} else {
			*bQ28 = silkTransitionLPBQ28[ind]
			*aQ28 = silkTransitionLPAQ28[ind]
		}
	} else {
		*bQ28 = silkTransitionLPBQ28[transitionIntNum-1]
		*aQ28 = silkTransitionLPAQ28[transitionIntNum-1]
	}
}

// silkBiquadAltStride1 applies a second-order ARMA filter (Direct Form II Transposed).
// Matches libopus silk_biquad_alt_stride1.
// Input and output are int16 slices, filter operates in-place when in==out.
func silkBiquadAltStride1(in []int16, bQ28 [transitionNB]int32, aQ28 [transitionNA]int32, s *[2]int32, out []int16, length int) {
	// Negate A_Q28 values and split into two parts
	a0LQ28 := (-aQ28[0]) & 0x00003FFF  // lower part
	a0UQ28 := silkRSHIFT(-aQ28[0], 14) // upper part
	a1LQ28 := (-aQ28[1]) & 0x00003FFF  // lower part
	a1UQ28 := silkRSHIFT(-aQ28[1], 14) // upper part

	for k := range length {
		// S[0], S[1]: Q12
		inval := int32(in[k])
		out32Q14 := silkLSHIFT(silkSMLAWB(s[0], bQ28[0], inval), 2)

		s[0] = s[1] + silkRSHIFT_ROUND(silkSMULWB(out32Q14, a0LQ28), 14)
		s[0] = silkSMLAWB(s[0], out32Q14, a0UQ28)
		s[0] = silkSMLAWB(s[0], bQ28[1], inval)

		s[1] = silkRSHIFT_ROUND(silkSMULWB(out32Q14, a1LQ28), 14)
		s[1] = silkSMLAWB(s[1], out32Q14, a1UQ28)
		s[1] = silkSMLAWB(s[1], bQ28[2], inval)

		// Scale back to Q0 and saturate
		out[k] = silkSAT16(silkRSHIFT(out32Q14+(1<<14)-1, 14))
	}
}

// LPVariableCutoff applies the LP variable cutoff filter to a frame.
// Matches libopus silk_LP_variable_cutoff.
func (lp *LPState) LPVariableCutoff(frame []int16, frameLength int) {
	if lp.Mode == 0 {
		return
	}

	// Calculate index and interpolation factor
	// TRANSITION_INT_STEPS == 64, so we use the shift optimization
	facQ16 := silkLSHIFT(int32(transitionFrames)-lp.TransitionFrameNo, 16-6)
	ind := silkRSHIFT(facQ16, 16)
	facQ16 -= silkLSHIFT(ind, 16)

	if ind < 0 {
		ind = 0
	}
	if ind >= int32(transitionIntNum) {
		ind = int32(transitionIntNum) - 1
	}

	// Interpolate filter coefficients
	var bQ28 [transitionNB]int32
	var aQ28 [transitionNA]int32
	lpInterpolateFilterTaps(&bQ28, &aQ28, int(ind), facQ16)

	// Update transition frame number for next frame
	next := min(max(lp.TransitionFrameNo+int32(lp.Mode), 0), int32(transitionFrames))
	lp.TransitionFrameNo = next

	// ARMA low-pass filtering
	silkBiquadAltStride1(frame, bQ28, aQ28, &lp.InLPState, frame, frameLength)
}
