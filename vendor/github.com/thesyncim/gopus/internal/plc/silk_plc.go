package plc

// SILK-side concealment for the plc package.
//
// ConcealSILKWithLTP / SILKPLCState / UpdateFromGoodFrame are a faithful Go port
// of the SILK packet-loss concealer in libopus silk/PLC.c (silk_PLC_conceal and
// silk_PLC_update), driven by the silk_PLC_struct state. The fixed-point helpers
// below (smulwb, smlawb, silk_INVERSE32_varQ, silk_LPC_inverse_pred_gain,
// silk_bwexpander, etc.) port the macros from silk/SigProc_FIX.h, silk/Inlines.h
// and silk/MacroCount.h that the concealer depends on; their integer widths are
// chosen to reproduce libopus truncation and saturation bit-for-bit.
//
// ConcealSILK / concealVoicedSILK / concealUnvoicedSILK are a simpler float
// fallback used when only the minimal SILKDecoderState (no LTP/excitation
// history) is available; the bit-exact path is ConcealSILKWithLTP.
//
// Reference: libopus silk/PLC.c, silk/PLC.h, RFC 6716 Section 4.2.8.
import (
	"math"
	"math/bits"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// Constants from libopus silk/PLC.h and silk/define.h, governing the SILK
// concealment cadence (attenuation, pitch drift, buffer sizes). Values match
// the C macros exactly.
const (
	// ltpOrder is the number of LTP (long-term prediction) filter taps;
	// libopus LTP_ORDER (silk/define.h).
	ltpOrder = 5

	// maxLPCOrder is the maximum LPC order (16 for WB, 10 for NB/MB);
	// libopus MAX_LPC_ORDER (silk/define.h).
	maxLPCOrder = 16

	// bweCoef is the bandwidth-expansion coefficient applied to the previous
	// LPC before concealment to pull the poles inward and keep the filter
	// stable; libopus BWE_COEF (silk/PLC.h).
	bweCoef float32 = 0.99

	// vPitchGainStartMinQ14 is the minimum starting LTP gain (0.7 in Q14);
	// gains below this are scaled up. libopus V_PITCH_GAIN_START_MIN_Q14.
	vPitchGainStartMinQ14 = 11469

	// vPitchGainStartMaxQ14 is the maximum starting LTP gain (0.95 in Q14);
	// gains above this are scaled down. libopus V_PITCH_GAIN_START_MAX_Q14.
	vPitchGainStartMaxQ14 = 15565

	// maxPitchLagMs is the pitch-lag ceiling in milliseconds used by the drift
	// clamp; libopus MAX_PITCH_LAG_MS (silk/PLC.h).
	maxPitchLagMs = 18

	// randBufSize is the size of the excitation-derived random noise buffer;
	// libopus RAND_BUF_SIZE (silk/PLC.h).
	randBufSize = 128

	// randBufMask masks a random index into randBuf; libopus RAND_BUF_MASK.
	randBufMask = randBufSize - 1

	// pitchDriftFacQ16 is the per-subframe pitch-lag drift factor (0.01 in
	// Q16), slowly lengthening the lag during extended loss; libopus
	// PITCH_DRIFT_FAC_Q16 (silk/PLC.h).
	pitchDriftFacQ16 = 655

	// log2InvLPCGainHighThres and log2InvLPCGainLowThres bound the unvoiced
	// LPC-gain downscale (8 dB / 24 dB); libopus LOG2_INV_LPC_GAIN_HIGH_THRES
	// and LOG2_INV_LPC_GAIN_LOW_THRES (silk/PLC.h).
	log2InvLPCGainHighThres = 3
	log2InvLPCGainLowThres  = 8

	// Fixed-point parameters for the LPC inverse-prediction-gain computation,
	// ported from libopus silk_LPC_inverse_pred_gain (silk/LPC_inv_pred_gain.c).
	lpcInvPredQA        = 24       // working precision (QA)
	lpcInvPredALimitQ24 = 16773023 // SILK_FIX_CONST(0.99975, 24): reflection-coef limit
	minInvPredGainQ30   = 107374   // SILK_FIX_CONST(1 / 1e4, 30): stability floor

	// Per-frame attenuation gains in Q15, indexed by loss count (first lost
	// frame vs. subsequent). harm_* attenuate the LTP (harmonic) contribution,
	// rand_* the random-noise contribution; V is voiced, UV unvoiced. These are
	// HARM_ATT_Q15, PLC_RAND_ATTENUATE_V_Q15 and PLC_RAND_ATTENUATE_UV_Q15
	// from libopus silk/PLC.c.
	harmAttQ15_0   = 32440 // 0.99 - first lost frame
	harmAttQ15_1   = 31130 // 0.95 - subsequent frames
	randAttVQ15_0  = 31130 // 0.95 - voiced, first frame
	randAttVQ15_1  = 26214 // 0.8 - voiced, subsequent
	randAttUVQ15_0 = 32440 // 0.99 - unvoiced, first frame
	randAttUVQ15_1 = 29491 // 0.9 - unvoiced, subsequent
)

// SILKDecoderState is the minimal SILK decoder view required by the float
// fallback concealer (ConcealSILK): the previous LPC envelope, its order, the
// last voicing decision, and the output history for pitch repetition. It lets
// the plc package conceal without importing the silk package. The bit-exact
// concealer needs the richer SILKDecoderStateExtended instead.
type SILKDecoderState interface {
	// PrevLPCValues returns the LPC filter state from the last frame.
	PrevLPCValues() []float32
	// LPCOrder returns the current LPC order (10 for NB/MB, 16 for WB).
	LPCOrder() int
	// IsPreviousFrameVoiced returns true if the last frame was voiced.
	IsPreviousFrameVoiced() bool
	// OutputHistory returns the output history buffer for pitch prediction.
	OutputHistory() []float32
	// HistoryIndex returns the current position in the history buffer.
	HistoryIndex() int
}

// SILKPitchLagProvider optionally exposes the decoder's most recent pitch lag
// (libopus lagPrev), letting the float fallback concealer use the tracked lag
// instead of re-estimating it by autocorrelation.
type SILKPitchLagProvider interface {
	GetLagPrev() int
}

// SILKSignalTypeProvider optionally exposes the libopus prevSignalType tracking:
// 0=inactive, 1=unvoiced, 2=voiced.
type SILKSignalTypeProvider interface {
	GetLastSignalType() int
}

// SILKSLPCQ14Provider optionally exposes the decoder's LPC synthesis history in
// Q14 (libopus sLPC_Q14_buf, the most recent lpcOrder samples) so concealment
// can seed LPC synthesis from real decoder state rather than the float envelope.
type SILKSLPCQ14Provider interface {
	GetSLPCQ14HistoryQ14() []int32
}

// SILKSLPCQ14Setter optionally lets concealment write the advanced LPC
// synthesis history (Q14) back to the decoder's sLPC_Q14_buf, matching the
// state cadence libopus silk_PLC_conceal applies after each concealed frame.
type SILKSLPCQ14Setter interface {
	SetSLPCQ14HistoryQ14(history []int32)
}

// SILKOutBufProvider optionally exposes the decoder's output history in Q0
// (libopus outBuf, the last ltp_mem_length samples) used for the LPC-analysis
// rewhitening step of silk_PLC_conceal. Preferred over the float OutputHistory
// because it is the exact integer input libopus rewhitens.
type SILKOutBufProvider interface {
	GetOutBufHistoryQ0() []int16
}

// SILKDecoderStateExtended is the full SILK decoder view consumed by the
// bit-exact concealer ConcealSILKWithLTP. It exposes everything
// silk_PLC_conceal reads from silk_decoder_state / silk_decoder_control in
// libopus: LTP coefficients and scale, pitch lag, subframe gains, LPC
// coefficients, the excitation history (exc_Q14), and the frame geometry
// (sample rate, subframe length, subframe count, LTP memory length).
type SILKDecoderStateExtended interface {
	SILKDecoderState

	// GetLastSignalType returns 0=inactive, 1=unvoiced, 2=voiced.
	GetLastSignalType() int

	// GetLTPCoefficients returns the LTP coefficients from the last good frame.
	// Returns 5 coefficients in Q14 format.
	GetLTPCoefficients() [ltpOrder]int16

	// GetPitchLag returns the pitch lag from the last good frame.
	GetPitchLag() int

	// GetLastGain returns the gain from the last subframe (Q16 format).
	GetLastGain() int32

	// GetLTPScale returns the LTP scale factor (Q14 format).
	GetLTPScale() int32

	// GetExcitationHistory returns the excitation signal history (Q14 format).
	GetExcitationHistory() []int32

	// GetLPCCoefficientsQ12 returns LPC coefficients in Q12 format.
	GetLPCCoefficientsQ12() []int16

	// GetSampleRateKHz returns the sample rate in kHz (8, 12, or 16).
	GetSampleRateKHz() int

	// GetSubframeLength returns the subframe length in samples.
	GetSubframeLength() int

	// GetNumSubframes returns the number of subframes (2 or 4).
	GetNumSubframes() int

	// GetLTPMemoryLength returns the LTP memory length in samples.
	GetLTPMemoryLength() int
}

// SILKPLCState is the persistent SILK concealment state carried across lost
// frames, a Go port of silk_PLC_struct from libopus silk/structs.h. Field
// Q-formats match the C struct exactly (see type_parity_test.go), because the
// concealer's fixed-point math depends on the precise widths. It is updated
// from each good frame by UpdateFromGoodFrame (silk_PLC_update) and advanced on
// each loss by ConcealSILKWithLTP (silk_PLC_conceal).
type SILKPLCState struct {
	// LTP coefficients from the last good voiced frame (Q14)
	LTPCoefQ14 [ltpOrder]int16

	// Pitch lag in Q8 format for sub-sample precision
	PitchLQ8 int32

	// Previous gains from last 2 subframes (Q16)
	PrevGainQ16 [2]int32

	// Previous LPC coefficients (Q12)
	PrevLPCQ12 [maxLPCOrder]int16

	// Previous LTP scale factor (Q14)
	PrevLTPScaleQ14 int32

	// Random scale for noise mixing (Q14)
	RandScaleQ14 int16

	// Random seed for noise generation
	RandSeed int32

	// Sample rate in kHz
	FsKHz int32

	// Subframe length
	SubfrLength int32

	// Number of subframes
	NbSubfr int32

	// LPC order
	LPCOrder int32

	// Concealed frame energy for glue frames
	ConcEnergy      int32
	ConcEnergyShift int32

	// Flag indicating if last frame was lost
	LastFrameLost bool
}

// NewSILKPLCState returns a SILKPLCState initialized to the libopus
// silk_PLC_Reset defaults (unit gains, 16 kHz WB geometry, unit random scale,
// zero seed), with the pitch lag pre-seeded to half a 16 kHz 20 ms frame.
func NewSILKPLCState() *SILKPLCState {
	return &SILKPLCState{
		// Default pitch lag: half frame length in Q8
		// 160 samples * 256 / 2 = 20480 (10ms at 16kHz)
		PitchLQ8: 160 << 7, // 160 samples = 10ms at 16kHz

		// Initialize gains to 1.0 (Q16)
		PrevGainQ16: [2]int32{1 << 16, 1 << 16},

		// Match libopus silk_PLC_Reset defaults.
		SubfrLength: 20,
		NbSubfr:     2,
		FsKHz:       16,
		LPCOrder:    16,

		// Initial random scale (1.0 in Q14)
		RandScaleQ14: 1 << 14,

		// Match libopus zero-initialized PLC rand_seed cadence.
		RandSeed: 0,
	}
}

// Reset clears the PLC state for a new stream, mirroring libopus silk_PLC_Reset:
// the pitch lag is set to half the frame length in Q8, gains and random scale
// to unity, and the cached LTP/LPC coefficients and loss flag are zeroed.
func (s *SILKPLCState) Reset(frameLength int) {
	s.PitchLQ8 = int32(frameLength) << 7 // Half frame length in Q8

	s.PrevGainQ16[0] = 1 << 16
	s.PrevGainQ16[1] = 1 << 16

	s.SubfrLength = 20
	s.NbSubfr = 2

	for i := range s.LTPCoefQ14 {
		s.LTPCoefQ14[i] = 0
	}

	for i := range s.PrevLPCQ12 {
		s.PrevLPCQ12[i] = 0
	}

	s.PrevLTPScaleQ14 = 0
	s.RandScaleQ14 = 1 << 14
	s.RandSeed = 0
	s.LastFrameLost = false
}

// UpdateFromGoodFrame refreshes the concealment state from a successfully
// decoded frame so a subsequent loss can extrapolate from it; call it after
// every good SILK frame. It is a port of silk_PLC_update (libopus silk/PLC.c):
// for voiced frames it picks the subframe with the strongest LTP gain, centers
// that gain on the middle tap, and clamps it to the
// [vPitchGainStartMinQ14, vPitchGainStartMaxQ14] band; for unvoiced frames it
// clears the LTP taps and sets an 18 ms default pitch lag. The last two
// subframe gains and the LPC coefficients are always cached.
func (s *SILKPLCState) UpdateFromGoodFrame(
	signalType int, // 0=inactive, 1=unvoiced, 2=voiced
	pitchL []int32, // Pitch lags for each subframe
	ltpCoefQ14 []int16, // LTP coefficients for all subframes (nbSubfr * ltpOrder)
	ltpScaleQ14 int32, // LTP scale factor
	gainsQ16 []int32, // Gains for each subframe (Q16)
	lpcQ12 []int16, // LPC coefficients (Q12)
	fsKHz int,
	nbSubfr int,
	subfrLength int,
) {
	s.FsKHz = int32(fsKHz)
	s.SubfrLength = int32(subfrLength)
	s.NbSubfr = int32(nbSubfr)
	s.LPCOrder = int32(len(lpcQ12))

	// Save LPC coefficients
	copy(s.PrevLPCQ12[:], lpcQ12)

	// Save last two gains
	if len(gainsQ16) >= 2 {
		s.PrevGainQ16[0] = gainsQ16[len(gainsQ16)-2]
		s.PrevGainQ16[1] = gainsQ16[len(gainsQ16)-1]
	}

	if signalType == 2 { // Voiced
		// Find the subframe with the highest LTP gain to use for concealment
		var ltpGainQ14 int32
		var tempLtpGainQ14 int32

		// libopus always calls silk_PLC_update with a consistent nb_subfr (2 or
		// 4) and full-length parameter arrays. Skip the search on degenerate
		// inputs (nb_subfr < 1 or short pitch/LTP arrays) so it cannot index out
		// of bounds; this leaves ltpGainQ14 == 0, the same as finding no pitch
		// pulse. Valid inputs run the loop exactly as before.
		voicedInputsOK := nbSubfr >= 1 &&
			len(pitchL) >= nbSubfr &&
			len(ltpCoefQ14) >= nbSubfr*ltpOrder

		for j := 0; voicedInputsOK && j*subfrLength < int(pitchL[nbSubfr-1]) && j < nbSubfr; j++ {
			tempLtpGainQ14 = 0
			subfrIdx := nbSubfr - 1 - j

			// Sum LTP coefficients for this subframe
			for i := range ltpOrder {
				tempLtpGainQ14 += int32(ltpCoefQ14[subfrIdx*ltpOrder+i])
			}

			if tempLtpGainQ14 > ltpGainQ14 {
				ltpGainQ14 = tempLtpGainQ14

				// Copy LTP coefficients from this subframe
				for i := range ltpOrder {
					s.LTPCoefQ14[i] = ltpCoefQ14[subfrIdx*ltpOrder+i]
				}

				// Save pitch lag in Q8
				s.PitchLQ8 = pitchL[subfrIdx] << 8
			}
		}

		// Center the LTP gain on the middle tap (like libopus)
		for i := range s.LTPCoefQ14 {
			s.LTPCoefQ14[i] = 0
		}
		s.LTPCoefQ14[ltpOrder/2] = int16(ltpGainQ14)

		// Limit LTP coefficients to valid range
		if ltpGainQ14 < vPitchGainStartMinQ14 {
			// Scale up if gain is too low
			if ltpGainQ14 > 0 {
				scaleQ10 := (vPitchGainStartMinQ14 << 10) / ltpGainQ14
				for i := range s.LTPCoefQ14 {
					s.LTPCoefQ14[i] = int16((int32(s.LTPCoefQ14[i]) * scaleQ10) >> 10)
				}
			}
		} else if ltpGainQ14 > vPitchGainStartMaxQ14 {
			// Scale down if gain is too high
			scaleQ14 := (vPitchGainStartMaxQ14 << 14) / ltpGainQ14
			for i := range s.LTPCoefQ14 {
				s.LTPCoefQ14[i] = int16((int32(s.LTPCoefQ14[i]) * scaleQ14) >> 14)
			}
		}

		s.PrevLTPScaleQ14 = ltpScaleQ14
	} else {
		// Unvoiced: use default pitch lag
		s.PitchLQ8 = int32(fsKHz*18) << 8 // 18ms pitch lag
		for i := range s.LTPCoefQ14 {
			s.LTPCoefQ14[i] = 0
		}
		s.PrevLTPScaleQ14 = 0
	}

	s.LastFrameLost = false
}

// ConcealSILK is the float fallback concealer for a lost SILK frame, used when
// only the minimal SILKDecoderState is available. It follows the RFC 6716
// Section 4.2.8 strategy in floating point rather than the bit-exact
// fixed-point path:
//
//  1. Reuse the previous frame's LPC envelope.
//  2. Voiced: repeat the pitch period from output history with a decaying gain.
//  3. Unvoiced: generate LPC-shaped comfort noise.
//  4. Apply the overall fade factor.
//
// This keeps transitions smooth by preserving the last good frame's spectral
// character, but it is NOT the byte-exact libopus path; prefer
// ConcealSILKWithLTP (a port of silk_PLC_conceal) when the extended decoder
// state is available.
//
// Parameters:
//   - dec: SILK decoder state from the last good frame
//   - frameSize: samples to generate at the native SILK rate (8/12/16 kHz)
//   - fadeFactor: overall gain multiplier (0.0 to 1.0)
//
// Returns the concealed mono samples at the native SILK rate.
func ConcealSILK(dec SILKDecoderState, frameSize int, fadeFactor float32) []float32 {
	if frameSize <= 0 {
		// Nothing to generate; a negative size would also panic make().
		return []float32{}
	}
	if dec == nil {
		return make([]float32, frameSize)
	}

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		return make([]float32, frameSize)
	}

	output := make([]float32, frameSize)

	// Get state from decoder
	prevLPC := dec.PrevLPCValues()
	order := dec.LPCOrder()
	if order == 0 {
		order = 10 // Default NB/MB order
	}

	wasVoiced := dec.IsPreviousFrameVoiced()

	// RNG state for noise generation
	rng := uint32(22222)

	if wasVoiced {
		// Voiced PLC: use pitch repetition with LPC filtering
		// Get pitch information from history
		concealVoicedSILK(dec, output, prevLPC, order, fadeFactor, &rng)
	} else {
		// Unvoiced PLC: generate comfort noise filtered by LPC
		concealUnvoicedSILK(output, prevLPC, order, fadeFactor, &rng)
	}

	return output
}

// ConcealSILKWithLTP is the bit-exact SILK concealer: a Go port of
// silk_PLC_conceal (libopus silk/PLC.c) that generates one lost frame's residual
// and synthesizes it through the LPC filter, advancing plcState exactly as
// libopus does so consecutive losses behave identically. The pipeline is:
//
//  1. Select loss-count attenuation gains (harm/rand) by voicing.
//  2. Bandwidth-expand the cached LPC (silk_bwexpander) in place.
//  3. On the first loss, set the random scale from the LTP gain (voiced) or
//     downscale it by the LPC inverse prediction gain (unvoiced).
//  4. Build the random-noise source from the lower-energy excitation subframe.
//  5. Rewhiten and scale the LTP state, then run LTP synthesis per subframe,
//     attenuating gains and drifting the pitch lag each subframe.
//  6. Run LPC synthesis, scale by the previous gain, and saturate to int16.
//
// When the decoder implements the optional SILKOutBufProvider /
// SILKSLPCQ14Provider / SILKSLPCQ14Setter interfaces, the integer outBuf and
// LPC synthesis history are used and written back, which is what makes the
// output byte-exact with libopus rather than approximate.
//
// Parameters:
//   - dec: extended SILK decoder state from the last good frame
//   - plcState: persistent concealment state, updated in place
//   - lossCnt: consecutive lost-frame count (0 for the first loss)
//   - frameSize: samples to generate at the native SILK rate
//
// Returns the concealed mono samples at the native SILK rate (int16 Q0).
func ConcealSILKWithLTP(dec SILKDecoderStateExtended, plcState *SILKPLCState, lossCnt int, frameSize int) []int16 {
	if frameSize <= 0 {
		// Nothing to generate; a negative size would also panic make().
		return []int16{}
	}
	if dec == nil || plcState == nil {
		return make([]int16, frameSize)
	}

	fsKHz := dec.GetSampleRateKHz()
	if fsKHz <= 0 {
		fsKHz = 16
	}

	nbSubfr := dec.GetNumSubframes()
	if nbSubfr <= 0 {
		nbSubfr = 4
	}

	subfrLength := dec.GetSubframeLength()
	if subfrLength <= 0 {
		subfrLength = 80
	}

	// libopus LPC_order is always in [10, MAX_LPC_ORDER]. Clamp degenerate
	// decoder reports so the fixed-size PrevLPCQ12 / sLPC_Q14 buffers and the
	// PrevLPCQ12[:lpcOrder] slices below stay in bounds; the valid range is
	// unaffected.
	lpcOrder := dec.LPCOrder()
	if lpcOrder <= 0 {
		lpcOrder = 16
	}
	if lpcOrder > maxLPCOrder {
		lpcOrder = maxLPCOrder
	}

	ltpMemLength := dec.GetLTPMemoryLength()
	if ltpMemLength <= 0 {
		ltpMemLength = 320
	}

	signalType := dec.GetLastSignalType()
	prevGainQ10 := [2]int32{
		plcState.PrevGainQ16[0] >> 6,
		plcState.PrevGainQ16[1] >> 6,
	}

	// Get attenuation factors based on loss count
	attIdx := min(lossCnt, 1)

	var harmGainQ15 int32
	var randGainQ15 int32

	if attIdx == 0 {
		harmGainQ15 = harmAttQ15_0
	} else {
		harmGainQ15 = harmAttQ15_1
	}

	if signalType == 2 { // Voiced
		if attIdx == 0 {
			randGainQ15 = randAttVQ15_0
		} else {
			randGainQ15 = randAttVQ15_1
		}
	} else {
		if attIdx == 0 {
			randGainQ15 = randAttUVQ15_0
		} else {
			randGainQ15 = randAttUVQ15_1
		}
	}

	// Apply bandwidth expansion to previous LPC in-state, matching libopus
	// silk_PLC_conceal() cadence across consecutive losses.
	bwExpandQ12(plcState.PrevLPCQ12[:lpcOrder], bweCoef)
	lpcQ12 := make([]int16, lpcOrder)
	copy(lpcQ12, plcState.PrevLPCQ12[:lpcOrder])

	// Initialize random scale on first lost frame
	randScaleQ14 := plcState.RandScaleQ14
	if lossCnt == 0 {
		randScaleQ14 = 1 << 14 // 1.0

		// For voiced frames, reduce noise based on LTP gain
		if signalType == 2 {
			for i := range ltpOrder {
				randScaleQ14 -= plcState.LTPCoefQ14[i]
			}
			if randScaleQ14 < 3277 { // 0.2 in Q14
				randScaleQ14 = 3277
			}
			// Apply LTP scale
			randScaleQ14 = int16((int32(randScaleQ14) * plcState.PrevLTPScaleQ14) >> 14)
		} else {
			// For unvoiced frames, reduce random gain for high LPC gain.
			// Mirrors libopus silk_PLC_conceal().
			invGainQ30 := lpcInversePredGainQ30(lpcQ12, lpcOrder)
			downScaleQ30 := minInt32((1<<30)>>log2InvLPCGainHighThres, invGainQ30)
			downScaleQ30 = maxInt32((1<<30)>>log2InvLPCGainLowThres, downScaleQ30)
			downScaleQ30 <<= log2InvLPCGainHighThres
			randGainQ15 = smulwb(downScaleQ30, randGainQ15) >> 14
		}
	}

	// Get pitch lag
	lag := int(plcState.PitchLQ8+128) >> 8

	// Prepare output buffers
	output := make([]int16, frameSize)

	// Generate excitation history buffer for random noise source
	excHistory := dec.GetExcitationHistory()
	randBuf := make([]int32, randBufSize)
	if len(excHistory) > 0 {
		// Use excitation from subframe with lower energy
		energy1, shift1 := computeEnergy(excHistory, prevGainQ10[0], subfrLength, (nbSubfr-2)*subfrLength)
		energy2, shift2 := computeEnergy(excHistory, prevGainQ10[1], subfrLength, (nbSubfr-1)*subfrLength)

		var randStart int
		if (energy1 >> uint(shift2)) < (energy2 >> uint(shift1)) {
			randStart = max(0, (nbSubfr-1)*subfrLength-randBufSize)
		} else {
			randStart = max(0, nbSubfr*subfrLength-randBufSize)
		}

		for i := 0; i < randBufSize && randStart+i < len(excHistory); i++ {
			randBuf[i] = excHistory[randStart+i]
		}
	}

	// LTP synthesis filtering
	sLTPQ15 := make([]int32, ltpMemLength+frameSize)
	sLTPBufIdx := ltpMemLength

	// Rewhiten LTP state using LPC analysis
	if signalType == 2 {
		startIdx := ltpMemLength - lag - lpcOrder - ltpOrder/2
		if startIdx <= 0 {
			startIdx = 1
		}

		// Perform LPC analysis to get sLTP.
		// Prefer decoder outBuf history (Q0), which matches libopus PLC inputs.
		sLTP := make([]int16, ltpMemLength)
		haveOutBufQ0 := false
		if provider, ok := dec.(SILKOutBufProvider); ok {
			outBufQ0 := provider.GetOutBufHistoryQ0()
			if len(outBufQ0) >= ltpMemLength && startIdx < ltpMemLength {
				lpcAnalysisFilterInt16(
					sLTP[startIdx:],
					outBufQ0[startIdx:ltpMemLength],
					lpcQ12,
					ltpMemLength-startIdx,
					lpcOrder,
				)
				haveOutBufQ0 = true
			}
		}
		if !haveOutBufQ0 {
			// Fallback for decoders that don't expose outBuf history.
			outHistory := dec.OutputHistory()
			if len(outHistory) > 0 {
				lpcAnalysisFilter(sLTP[startIdx:], outHistory, lpcQ12, ltpMemLength-startIdx, lpcOrder, startIdx)
			}
		}

		// Scale LTP state
		invGainQ30 := inverse32VarQ(plcState.PrevGainQ16[1], 46)
		if invGainQ30 > (1<<30 - 1) {
			invGainQ30 = 1<<30 - 1
		}

		for i := startIdx + lpcOrder; i < ltpMemLength; i++ {
			sLTPQ15[i] = smulwb(invGainQ30, int32(sLTP[i]))
		}
	}

	randSeed := plcState.RandSeed
	B_Q14 := plcState.LTPCoefQ14

	// Process each subframe
	sLPCQ14 := make([]int32, frameSize+maxLPCOrder)
	haveSLPCHistory := false
	if provider, ok := dec.(SILKSLPCQ14Provider); ok {
		historyQ14 := provider.GetSLPCQ14HistoryQ14()
		if len(historyQ14) >= lpcOrder {
			start := maxLPCOrder - lpcOrder
			copy(sLPCQ14[start:maxLPCOrder], historyQ14[:lpcOrder])
			haveSLPCHistory = true
		}
	}
	if !haveSLPCHistory {
		prev := dec.PrevLPCValues()
		if len(prev) >= lpcOrder {
			start := maxLPCOrder - lpcOrder
			for i := 0; i < lpcOrder; i++ {
				sLPCQ14[start+i] = opusmath.RoundF32HalfAwayFromZeroToInt32(prev[i] * 16384.0)
			}
		}
	}

	generated := 0
	for k := 0; k < nbSubfr && generated < frameSize; k++ {
		subfrSamples := subfrLength
		if remaining := frameSize - generated; subfrSamples > remaining {
			subfrSamples = remaining
		}
		// Match libopus PLC.c cadence:
		// always run LTP_pred + noise synthesis loop; for unvoiced frames,
		// B_Q14 is zero so prediction naturally collapses to rounding bias only.
		predLagPtr := sLTPBufIdx - lag + ltpOrder/2
		for i := 0; i < subfrSamples; i++ {
			ltpPredQ12 := int32(2) // rounding to avoid negative bias
			ltpPredQ12 = smlawb(ltpPredQ12, silkPLCBufferAt(sLTPQ15, predLagPtr+0), int32(B_Q14[0]))
			ltpPredQ12 = smlawb(ltpPredQ12, silkPLCBufferAt(sLTPQ15, predLagPtr-1), int32(B_Q14[1]))
			ltpPredQ12 = smlawb(ltpPredQ12, silkPLCBufferAt(sLTPQ15, predLagPtr-2), int32(B_Q14[2]))
			ltpPredQ12 = smlawb(ltpPredQ12, silkPLCBufferAt(sLTPQ15, predLagPtr-3), int32(B_Q14[3]))
			ltpPredQ12 = smlawb(ltpPredQ12, silkPLCBufferAt(sLTPQ15, predLagPtr-4), int32(B_Q14[4]))
			predLagPtr++

			randSeed = silkRand(randSeed)
			idx := (randSeed >> 25) & randBufMask
			randExc := randBuf[idx]
			if sLTPBufIdx >= len(sLTPQ15) {
				break
			}
			sLTPQ15[sLTPBufIdx] = smlawb(ltpPredQ12, randExc, int32(randScaleQ14)) << 2
			sLTPBufIdx++
		}
		generated += subfrSamples

		// Attenuate LTP gain
		for j := range ltpOrder {
			B_Q14[j] = int16((int32(B_Q14[j]) * harmGainQ15) >> 15)
		}

		// Attenuate random scale
		randScaleQ14 = int16((int32(randScaleQ14) * randGainQ15) >> 15)

		// Increase pitch lag slowly
		plcState.PitchLQ8 = plcState.PitchLQ8 + ((plcState.PitchLQ8 * pitchDriftFacQ16) >> 16)
		maxLag := int32(maxPitchLagMs*fsKHz) << 8
		if plcState.PitchLQ8 > maxLag {
			plcState.PitchLQ8 = maxLag
		}
		lag = int(plcState.PitchLQ8+128) >> 8
	}

	// LPC synthesis filtering
	sLPCBufIdx := ltpMemLength - maxLPCOrder
	for i := range frameSize {
		// LPC prediction
		lpcPredQ10 := int32(lpcOrder >> 1) // Rounding
		for j := 0; j < lpcOrder; j++ {
			lpcPredQ10 = smlawb(lpcPredQ10, sLPCQ14[maxLPCOrder+i-j-1], int32(lpcQ12[j]))
		}

		// Add LTP excitation to LPC prediction
		sLPCQ14[maxLPCOrder+i] = addSat32(sLTPQ15[sLPCBufIdx+maxLPCOrder+i], lshiftSat32(lpcPredQ10, 4))

		// Scale with gain
		outVal := smulww(sLPCQ14[maxLPCOrder+i], prevGainQ10[1])
		output[i] = sat16(rshiftRound(outVal, 8))
	}

	// Update PLC state for next concealment
	plcState.RandScaleQ14 = randScaleQ14
	plcState.RandSeed = randSeed
	plcState.LTPCoefQ14 = B_Q14
	plcState.LastFrameLost = true

	// Match libopus PLC.c cadence: persist LPC synthesis history after conceal.
	if setter, ok := dec.(SILKSLPCQ14Setter); ok && lpcOrder > 0 {
		end := min(maxLPCOrder+frameSize, len(sLPCQ14))
		start := max(end-lpcOrder, 0)
		if start < end {
			setter.SetSLPCQ14HistoryQ14(sLPCQ14[start:end])
		}
	}

	return output
}

// silkPLCBufferAt reads sLTP_Q14 at idx, returning 0 for out-of-range indices.
// libopus indexes this buffer through pointer arithmetic that is always in
// bounds given its assertions; the explicit guard keeps the Go port safe when
// degenerate geometry (e.g. a tiny ltp_mem_length) pushes pred_lag_ptr out of
// range, without altering the value for the valid in-range case.
func silkPLCBufferAt(buf []int32, idx int) int32 {
	if idx < 0 || idx >= len(buf) {
		return 0
	}
	return buf[idx]
}

// concealVoicedSILK is the voiced branch of the float fallback concealer
// (ConcealSILK): it repeats the pitch period from the decoder's output history
// with a per-sample decay and a touch of dither to avoid pure periodicity. With
// no usable history it falls back to the unvoiced comfort-noise path.
func concealVoicedSILK(dec SILKDecoderState, output []float32, prevLPC []float32, order int, fade float32, rng *uint32) {
	// Get history for pitch repetition
	history := dec.OutputHistory()
	histIdx := dec.HistoryIndex()
	histLen := len(history)

	if histLen == 0 {
		// No history available, fall back to noise
		concealUnvoicedSILK(output, prevLPC, order, fade, rng)
		return
	}

	// Prefer decoder-tracked pitch lag (lagPrev) when available.
	pitchLag := 0
	if p, ok := dec.(SILKPitchLagProvider); ok {
		pitchLag = p.GetLagPrev()
	}
	if pitchLag <= 0 {
		// Fallback to autocorrelation estimate.
		pitchLag = estimatePitchFromHistory(history, histIdx, histLen)
	}
	if pitchLag < 10 {
		pitchLag = 80 // Default to ~5ms at 16kHz if estimation fails
	}

	// Generate voiced excitation by repeating pitch period
	excitation := make([]float32, len(output))
	for i := range excitation {
		// Get sample from pitch-delayed history (posMod keeps the index in
		// [0, histLen) even if histIdx is a degenerate negative value, and is
		// identical to the previous wrap loop for valid non-negative indices).
		srcIdx := posMod(histIdx-pitchLag+(i%pitchLag), histLen)

		// Copy with decay
		excitation[i] = history[srcIdx] * fade

		// Add small noise to prevent pure repetition artifacts
		*rng = *rng*1664525 + 1013904223
		noise := (float32(*rng>>16) - 32768.0) / 32768.0 * 0.01
		excitation[i] += noise * fade
	}

	// Apply simple smoothing to avoid harsh transitions
	for i := range output {
		output[i] = excitation[i]
	}
}

// concealUnvoicedSILK is the unvoiced branch of the float fallback concealer
// (ConcealSILK): it generates white noise and shapes it with a lightweight IIR
// derived from the previous LPC envelope, clamping the output for stability so
// the comfort noise carries the spectral tilt of the last good frame.
func concealUnvoicedSILK(output []float32, prevLPC []float32, order int, fade float32, rng *uint32) {
	// Generate white noise excitation
	excitation := make([]float32, len(output))
	for i := range excitation {
		*rng = *rng*1664525 + 1013904223
		// Generate noise in [-1, 1] range
		noise := (float32(*rng>>16) - 32768.0) / 65536.0
		excitation[i] = noise * fade
	}

	// Apply simple LPC filter to shape the noise
	// This gives the noise the spectral character of speech
	if order > 0 && len(prevLPC) >= order {
		state := make([]float32, order)
		copy(state, prevLPC[:order])

		for i := range output {
			// IIR filter: y[n] = x[n] + sum(a[k]*y[n-k-1])
			y := excitation[i]
			for k := 0; k < order && k < len(state); k++ {
				if i-k-1 >= 0 {
					y += state[k] * output[i-k-1] * 0.1
				}
			}
			output[i] = y

			// Clamp to prevent instability
			if output[i] > 1.0 {
				output[i] = 1.0
			} else if output[i] < -1.0 {
				output[i] = -1.0
			}
		}
	} else {
		// No LPC available, just use the noise directly
		copy(output, excitation)
	}
}

// posMod returns the non-negative remainder of x modulo m (m must be > 0). For
// non-negative x it equals x % m exactly, so it preserves behavior for the
// valid in-range history indices; for a degenerate negative index it wraps into
// [0, m) instead of returning Go's negative remainder, keeping array accesses in
// bounds.
func posMod(x, m int) int {
	r := x % m
	if r < 0 {
		r += m
	}
	return r
}

// estimatePitchFromHistory estimates the pitch period (in samples) of the
// recent output history by a plain autocorrelation peak search over a typical
// speech pitch range, used by the float fallback concealer only when the
// decoder does not expose a tracked lag. It returns a safe default when the
// history is too short or no clear peak is found.
func estimatePitchFromHistory(history []float32, histIdx, histLen int) int {
	// Search range: 32 to 288 samples (typical pitch range)
	// At 16kHz: 32 samples = 2ms (500Hz), 288 samples = 18ms (55Hz)
	minLag := 32
	maxLag := min(288, histLen/2)
	if minLag >= maxLag {
		return 80 // Default
	}

	// Look at last analysisLen samples
	analysisLen := min(
		// ~20ms at 16kHz
		320, histLen)

	var bestLag int
	var bestCorr float32 = -1e10

	// Simple autocorrelation search
	for lag := minLag; lag < maxLag; lag++ {
		var corr float32

		for i := 0; i < analysisLen-lag; i++ {
			idx1 := posMod(histIdx-analysisLen+i, histLen)
			idx2 := posMod(histIdx-analysisLen+i+lag, histLen)
			corr += history[idx1] * history[idx2]
		}

		if corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}

	if bestLag < minLag {
		bestLag = 80 // Default
	}

	return bestLag
}

// ConcealSILKStereo conceals a stereo SILK frame using the float fallback path.
// It runs the mono concealer once and duplicates the result to both channels;
// it does not reconstruct the SILK mid/side stereo prediction, so it is a
// fallback for the simple SILKDecoderState only.
//
// Parameters:
//   - dec: SILK decoder state, applied to both channels
//   - frameSize: samples per channel at the native SILK rate
//   - fadeFactor: overall gain multiplier (0.0 to 1.0)
//
// Returns the left and right concealed channels.
func ConcealSILKStereo(dec SILKDecoderState, frameSize int, fadeFactor float32) (left, right []float32) {
	if frameSize <= 0 {
		// Nothing to generate; a negative size would also panic make().
		return []float32{}, []float32{}
	}

	// For stereo, apply mono PLC to both channels
	// A more sophisticated approach would use the stereo prediction weights
	mono := ConcealSILK(dec, frameSize, fadeFactor)

	// Copy mono to both channels (simple approach)
	// In practice, you'd want to maintain separate L/R state
	left = make([]float32, frameSize)
	right = make([]float32, frameSize)
	copy(left, mono)
	copy(right, mono)

	return left, right
}

// Fixed-point arithmetic helpers ported from the libopus SILK macros
// (silk/SigProc_FIX.h and silk/Inlines.h). Each reproduces the exact
// truncation, rounding and saturation of its C counterpart; the integer widths
// are load-bearing for bit-exact concealment and must not be widened.

// silkRand advances the SILK PLC linear-congruential noise seed; libopus
// silk_RAND (silk/SigProc_FIX.h). Overflow wraps as in C int32 arithmetic.
func silkRand(seed int32) int32 {
	return seed*196314165 + 907633515
}

// smulwb returns (a * (int16)b) >> 16: 32-bit times the low 16 bits of b,
// keeping the high word; libopus silk_SMULWB.
func smulwb(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

// smulww returns (a * b) >> 16: the high word of a full 32x32 product in Q16;
// libopus silk_SMULWW.
func smulww(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

// smlawb returns a + smulwb(b, c); libopus silk_SMLAWB. As in libopus this
// rounds toward -inf, which is why the concealer pre-loads a +2 bias.
func smlawb(a, b, c int32) int32 {
	return a + smulwb(b, c)
}

// rshiftRound returns a right-shifted by shift with round-to-nearest (ties up);
// libopus silk_RSHIFT_ROUND. The shift==1 special case avoids overflow in the
// rounding add on large-magnitude values.
func rshiftRound(a int32, shift int) int32 {
	if shift <= 0 {
		return a
	}
	// Match libopus silk_RSHIFT_ROUND macro to avoid overflow in the
	// rounding add path on large-magnitude values.
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}

// sat16 clamps a to the int16 range; libopus silk_SAT16.
func sat16(a int32) int16 {
	if a > 32767 {
		return 32767
	}
	if a < -32768 {
		return -32768
	}
	return int16(a)
}

// addSat32 returns a + b saturated to the int32 range; libopus silk_ADD_SAT32.
func addSat32(a, b int32) int32 {
	res := int64(a) + int64(b)
	if res > math.MaxInt32 {
		return math.MaxInt32
	}
	if res < math.MinInt32 {
		return math.MinInt32
	}
	return int32(res)
}

// lshiftSat32 returns a << shift saturated to the int32 range; libopus
// silk_LSHIFT_SAT32.
func lshiftSat32(a int32, shift int) int32 {
	if shift == 0 {
		return a
	}
	max := int32(math.MaxInt32 >> shift)
	min := int32(math.MinInt32 >> shift)
	if a > max {
		return math.MaxInt32
	}
	if a < min {
		return math.MinInt32
	}
	return a << shift
}

// inverse32VarQ returns an approximation of 1/b in Q(qRes) using one
// Newton refinement step; libopus silk_INVERSE32_varQ (silk/Inlines.h). The
// concealer uses it to invert the previous gain when scaling the LTP state.
func inverse32VarQ(b, qRes int32) int32 {
	if b == 0 {
		return math.MaxInt32
	}
	if qRes <= 0 {
		return 0
	}

	// Port of libopus silk_INVERSE32_varQ() from silk/Inlines.h.
	bHeadrm := int32(clz32(abs32Int(b)) - 1)
	bNrm := b << bHeadrm

	den := int16(bNrm >> 16)
	if den == 0 {
		return math.MaxInt32
	}
	bInv := int32(int64(math.MaxInt32>>2) / int64(den))

	result := bInv << 16
	errQ32 := (((int32(1) << 29) - smulwb(bNrm, bInv)) << 3)
	result = result + smulww(errQ32, bInv)

	lshift := int32(61) - bHeadrm - qRes
	if lshift <= 0 {
		return lshiftSat32(result, int(-lshift))
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

// smmul returns the top 32 bits of the 64-bit product a*b (i.e. (a*b) >> 32);
// libopus silk_SMMUL.
func smmul(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 32)
}

// rshiftRound64 is the 64-bit form of rshiftRound (round-to-nearest right
// shift); libopus silk_RSHIFT_ROUND64.
func rshiftRound64(a int64, shift int) int64 {
	if shift <= 0 {
		return a
	}
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}

// subSat32 returns a - b saturated to the int32 range; libopus silk_SUB_SAT32.
func subSat32(a, b int32) int32 {
	v := int64(a) - int64(b)
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// mul32FracQ returns a*b rounded down to Q(q) from the 64-bit product; libopus
// silk_MUL32_FRAC_Q.
func mul32FracQ(a, b int32, q uint) int32 {
	return int32(rshiftRound64(int64(a)*int64(b), int(q)))
}

// abs32Int returns the absolute value of a; libopus silk_abs (int32).
func abs32Int(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}

// clz32 returns the count of leading zero bits in a (32 for zero); libopus
// silk_CLZ32.
func clz32(a int32) int {
	if a == 0 {
		return 32
	}
	return bits.LeadingZeros32(uint32(a))
}

// lpcInversePredGainQ30 returns the inverse prediction gain (1/prediction gain)
// in Q30 for the Q12 LPC coefficients, a stability proxy used in the unvoiced
// first-loss random-scale downscale. It ports silk_LPC_inverse_pred_gain
// (libopus silk/LPC_inv_pred_gain.c): it returns 0 for an unstable filter
// (including a DC response at or above unity) so the caller treats it as
// maximally unstable.
func lpcInversePredGainQ30(aQ12 []int16, order int) int32 {
	if order <= 0 {
		return 1 << 30
	}
	if order > len(aQ12) {
		order = len(aQ12)
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}

	var aQA [maxLPCOrder]int32
	dcResp := int32(0)
	for k := 0; k < order; k++ {
		dcResp += int32(aQ12[k])
		aQA[k] = int32(aQ12[k]) << (lpcInvPredQA - 12)
	}
	if dcResp >= 4096 {
		return 0
	}
	return lpcInversePredGainQAC(aQA[:order], order)
}

// lpcInversePredGainQAC is the QA-domain core of lpcInversePredGainQ30: the
// Levinson-style reflection-coefficient recursion from
// silk_LPC_inverse_pred_gain_QA_c (libopus silk/LPC_inv_pred_gain.c). It
// returns 0 as soon as a reflection coefficient or running gain leaves the
// stable region. aQA is consumed (modified) in place.
func lpcInversePredGainQAC(aQA []int32, order int) int32 {
	invGainQ30 := int32(1 << 30)

	for k := order - 1; k > 0; k-- {
		if aQA[k] > lpcInvPredALimitQ24 || aQA[k] < -lpcInvPredALimitQ24 {
			return 0
		}

		rcQ31 := -(aQA[k] << (31 - lpcInvPredQA))
		rcMult1Q30 := (1 << 30) - smmul(rcQ31, rcQ31)
		if rcMult1Q30 <= (1<<15) || rcMult1Q30 > (1<<30) {
			return 0
		}

		invGainQ30 = smmul(invGainQ30, rcMult1Q30) << 2
		if invGainQ30 < minInvPredGainQ30 {
			return 0
		}

		mult2Q := 32 - clz32(abs32Int(rcMult1Q30))
		rcMult2 := inverse32VarQ(rcMult1Q30, int32(mult2Q+30))

		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := aQA[n]
			tmp2 := aQA[k-n-1]

			v1 := subSat32(tmp1, mul32FracQ(tmp2, rcQ31, 31))
			upd1 := rshiftRound64(int64(v1)*int64(rcMult2), mult2Q)
			if upd1 > math.MaxInt32 || upd1 < math.MinInt32 {
				return 0
			}
			aQA[n] = int32(upd1)

			v2 := subSat32(tmp2, mul32FracQ(tmp1, rcQ31, 31))
			upd2 := rshiftRound64(int64(v2)*int64(rcMult2), mult2Q)
			if upd2 > math.MaxInt32 || upd2 < math.MinInt32 {
				return 0
			}
			aQA[k-n-1] = int32(upd2)
		}
	}

	if aQA[0] > lpcInvPredALimitQ24 || aQA[0] < -lpcInvPredALimitQ24 {
		return 0
	}
	rcQ31 := -(aQA[0] << (31 - lpcInvPredQA))
	rcMult1Q30 := (1 << 30) - smmul(rcQ31, rcQ31)
	invGainQ30 = smmul(invGainQ30, rcMult1Q30) << 2
	if invGainQ30 < minInvPredGainQ30 {
		return 0
	}

	return invGainQ30
}

// minInt32 returns the smaller of two int32 values; libopus silk_min_32.
func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// maxInt32 returns the larger of two int32 values; libopus silk_max_32.
func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// bwExpandQ12 applies bandwidth expansion (chirp) to the Q12 LPC coefficients
// ar in place, scaling tap k by coef^(k+1); libopus silk_bwexpander
// (silk/bwexpander.c). The concealer uses it with bweCoef to pull the previous
// LPC poles inward and keep the synthesis filter stable across losses.
func bwExpandQ12(ar []int16, coef float32) {
	if len(ar) == 0 {
		return
	}
	// Match SILK_FIX_CONST(coef, 16) rounding semantics.
	chirpQ16 := opusmath.RoundF32HalfAwayFromZeroToInt32(coef * 65536.0)
	chirpMinusOneQ16 := chirpQ16 - 65536

	for i := 0; i < len(ar)-1; i++ {
		ar[i] = int16(rshiftRound(chirpQ16*int32(ar[i]), 16))
		chirpQ16 = chirpQ16 + rshiftRound(chirpQ16*chirpMinusOneQ16, 16)
	}
	last := len(ar) - 1
	ar[last] = int16(rshiftRound(chirpQ16*int32(ar[last]), 16))
}

// computeEnergy returns the summed-square energy (with its associated headroom
// shift) of length gain-scaled excitation samples starting at offset. It
// combines the gain scaling of silk_PLC_energy with the two-pass shift
// selection of silk_sum_sqr_shift (libopus silk/sum_sqr_shift.c): a first pass
// picks a shift that prevents accumulator overflow, the second accumulates at
// that shift. The concealer compares the two candidate subframes' energies to
// choose the random-noise source. A degenerate range returns (0, 0).
func computeEnergy(exc []int32, gainQ10 int32, length, offset int) (energy int32, shift int) {
	// libopus calls this with offset = (nb_subfr-2)*subfr_length, which is
	// non-negative for the valid nb_subfr of 2 or 4. A degenerate nb_subfr (< 2)
	// makes the offset negative; reject that (and any empty range) up front so
	// the indexed reads below stay in bounds.
	if length <= 0 || offset < 0 || offset >= len(exc) {
		return 0, 0
	}

	end := min(offset+length, len(exc))
	n := end - offset
	if n <= 0 {
		return 0, 0
	}

	// Exact silk_sum_sqr_shift() two-pass shift selection from libopus.
	shft := 31 - bits.LeadingZeros32(uint32(n))
	nrg := int32(n)

	i := 0
	for ; i < n-1; i += 2 {
		s0 := int32(sat16(smulww(exc[offset+i], gainQ10) >> 8))
		s1 := int32(sat16(smulww(exc[offset+i+1], gainQ10) >> 8))
		nrgTmp := uint32(s0*s0 + s1*s1)
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}
	if i < n {
		s0 := int32(sat16(smulww(exc[offset+i], gainQ10) >> 8))
		nrgTmp := uint32(s0 * s0)
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}

	shft = max(0, shft+3-int(bits.LeadingZeros32(uint32(nrg))))

	nrg = 0
	i = 0
	for ; i < n-1; i += 2 {
		s0 := int32(sat16(smulww(exc[offset+i], gainQ10) >> 8))
		s1 := int32(sat16(smulww(exc[offset+i+1], gainQ10) >> 8))
		nrgTmp := uint32(s0*s0 + s1*s1)
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}
	if i < n {
		s0 := int32(sat16(smulww(exc[offset+i], gainQ10) >> 8))
		nrgTmp := uint32(s0 * s0)
		nrg = int32(uint32(nrg) + (nrgTmp >> uint(shft)))
	}

	return nrg, shft
}

// lpcAnalysisFilter runs the LPC analysis (whitening) filter over float input
// history to produce the int16 residual used to rewhiten the LTP state. It is
// the fallback for decoders that expose only the float OutputHistory; the
// integer lpcAnalysisFilterInt16 over outBuf is preferred and bit-exact. Ports
// silk_LPC_analysis_filter (libopus silk/LPC_analysis_filter.c).
func lpcAnalysisFilter(out []int16, in []float32, B []int16, length, order, startIdx int) {
	for i := 0; i < order && i < length; i++ {
		out[i] = 0
	}

	histIdx := len(in) - 1

	for ix := order; ix < length; ix++ {
		inIdx := max(histIdx-(length-1-ix-startIdx), 0)

		outQ12 := int32(0)
		for j := range order {
			prevIdx := inIdx - j - 1
			if prevIdx < 0 {
				break
			}
			if prevIdx < len(in) {
				inVal := int32(in[prevIdx] * 32768.0)
				outQ12 += (inVal * int32(B[j])) >> 12
			}
		}

		if inIdx < len(in) {
			inVal := int32(in[inIdx] * 32768.0)
			outQ12 = (inVal << 0) - outQ12
		}

		out32 := rshiftRound(outQ12, 0)
		out[ix] = sat16(out32)
	}
}

// lpcAnalysisFilterInt16 runs the LPC analysis (whitening) filter over the
// decoder's integer output history (outBuf, Q0) to produce the int16 residual
// for LTP-state rewhitening. This is the bit-exact path, matching
// silk_LPC_analysis_filter (libopus silk/LPC_analysis_filter.c) on the same
// integer inputs libopus uses.
func lpcAnalysisFilterInt16(out []int16, in []int16, B []int16, length, order int) {
	for i := 0; i < order && i < length; i++ {
		out[i] = 0
	}

	for ix := order; ix < length; ix++ {
		inPos := ix
		if inPos >= len(in) {
			break
		}

		outQ12 := int32(in[inPos-1]) * int32(B[0])
		for j := 1; j < order; j++ {
			outQ12 += int32(in[inPos-1-j]) * int32(B[j])
		}

		outQ12 = (int32(in[inPos]) << 12) - outQ12
		out[ix] = sat16(rshiftRound(outQ12, 12))
	}
}
