//go:build gopus_fixed_point

// CELT fixed-point encode-side prefilter ported from libopus celt/celt_encoder.c
// run_prefilter and its celt/pitch.c dependencies, under FIXED_POINT (non-QEXT,
// float API enabled, ENABLE_RES24). This increment covers the value-producing
// stage: the pitch analysis (pitch_downsample + pitch_search + remove_doubling),
// the single-tone fallback, the gain/qg quantisation and tapset decision, and
// the post-filter parameter bitstream emission (octave/period/gain/tapset).
//
// The comb_filter prefiltering of the time-domain input is wired by the caller
// using the already-ported CombFilter/CombFilterConst (celt_comb.go); see
// PrefilterAnalysis for the parameters it produces.
//
// Types match libopus FIXED_POINT (celt/arch.h):
//
//	opus_val16          -> int16
//	opus_val32/celt_sig -> int32
//	celt_coef           -> int16 (non-QEXT)
//
// pitch_downsample/pitch_search/remove_doubling are ported locally here under
// distinct names; the FIXED_POINT integer variants are not present elsewhere in
// this package. NOTE(dedup): if a future workstream needs these kernels outside
// the prefilter, lift them to a shared celt_pitch.go-style file.
package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

const (
	combFilterMaxPeriod = 1024
	combFilterMinPeriod = 15
)

// tapsetICDF (celt/celt.h tapset_icdf[3] = {2,1,0}) is declared in celt_decoder.go.

// half16 implements libopus HALF16(x) = SHR16(x, 1) on int16.
func half16(x int16) int16 { return x >> 1 }

// celtFir5 ports celt_fir5 (celt/pitch.c, FIXED_POINT): an in-place order-5 FIR
// with a SIG_SHIFT-scaled accumulator and ROUND16 output. x is modified in
// place over its first n samples.
func celtFir5(x []int16, num []int16, n int) {
	num0 := num[0]
	num1 := num[1]
	num2 := num[2]
	num3 := num[3]
	num4 := num[4]
	var mem0, mem1, mem2, mem3, mem4 int32
	for i := 0; i < n; i++ {
		sum := shl32(extend32(x[i]), sigShift)
		sum = mac16(sum, num0, int16(mem0))
		sum = mac16(sum, num1, int16(mem1))
		sum = mac16(sum, num2, int16(mem2))
		sum = mac16(sum, num3, int16(mem3))
		sum = mac16(sum, num4, int16(mem4))
		mem4 = mem3
		mem3 = mem2
		mem2 = mem1
		mem1 = mem0
		mem0 = int32(x[i])
		x[i] = round16(sum, sigShift)
	}
}

// pitchDownsample ports pitch_downsample (celt/pitch.c, FIXED_POINT). x holds C
// channel pointers, each into a celt_sig (int32) buffer with at least len*factor
// samples; xLP receives len downsampled, LPC-shaped opus_val16 samples. factor
// is the decimation factor (2 in the prefilter).
func pitchDownsample(x [][]int32, xLP []int16, length, c, factor int, scratch *celtEncodeScratch) {
	offset := factor / 2

	maxabs := CeltMaxabs32(x[0][:length*factor])
	if c == 2 {
		maxabs1 := CeltMaxabs32(x[1][:length*factor])
		maxabs = max32(maxabs, maxabs1)
	}
	if maxabs < 1 {
		maxabs = 1
	}
	shift := int(CeltILog2(maxabs)) - 10
	if shift < 0 {
		shift = 0
	}
	if c == 2 {
		shift++
	}

	for i := 1; i < length; i++ {
		xLP[i] = int16(shr32(x[0][factor*i-offset], shift+2) +
			shr32(x[0][factor*i+offset], shift+2) +
			shr32(x[0][factor*i], shift+1))
	}
	xLP[0] = int16(shr32(x[0][offset], shift+2) + shr32(x[0][0], shift+1))
	if c == 2 {
		for i := 1; i < length; i++ {
			xLP[i] += int16(shr32(x[1][factor*i-offset], shift+2) +
				shr32(x[1][factor*i+offset], shift+2) +
				shr32(x[1][factor*i], shift+1))
		}
		xLP[0] += int16(shr32(x[1][offset], shift+2) + shr32(x[1][0], shift+1))
	}

	var ac [5]int32
	plcCeltAutocorr(xLP[:length], ac[:], nil, 0, 4, length, scratch)

	// Noise floor -40 dB: ac[0] += SHR32(ac[0], 13).
	ac[0] += shr32(ac[0], 13)
	// Lag windowing.
	for i := 1; i <= 4; i++ {
		ac[i] -= mult16x32q15(int16(2*i*i), ac[i])
	}

	var lpc [4]int16
	plcCeltLPC(lpc[:], ac[:], 4)

	tmp := q15One
	for i := 0; i < 4; i++ {
		tmp = mult16x16q15(int16(29491), tmp) // QCONST16(.9f,15) = 29491
		lpc[i] = mult16x16q15(lpc[i], tmp)
	}

	// Add a zero.
	c1 := int16(26214) // QCONST16(.8f,15)
	var lpc2 [5]int16
	lpc2[0] = lpc[0] + int16(QConst16PointEight) // QCONST16(.8f, SIG_SHIFT) = 0.8*4096
	lpc2[1] = lpc[1] + mult16x16q15(c1, lpc[0])
	lpc2[2] = lpc[2] + mult16x16q15(c1, lpc[1])
	lpc2[3] = lpc[3] + mult16x16q15(c1, lpc[2])
	lpc2[4] = mult16x16q15(c1, lpc[3])
	celtFir5(xLP, lpc2[:], length)
}

// QConst16PointEight is QCONST16(.8f, SIG_SHIFT) with SIG_SHIFT=12:
// round(0.8 * 4096) = 3277.
const QConst16PointEight = 3277

// findBestPitch ports find_best_pitch (celt/pitch.c, FIXED_POINT). It fills
// bestPitch[0..1] with the two strongest correlation lags. y must hold at least
// maxPitch+len samples.
func findBestPitch(xcorr []int32, y []int16, length, maxPitch int, yshift int, maxcorr int32, bestPitch *[2]int) {
	xshift := int(CeltILog2(maxcorr)) - 14

	Syy := int32(1)
	bestNum := [2]int16{-1, -1}
	bestDen := [2]int32{0, 0}
	bestPitch[0] = 0
	bestPitch[1] = 1

	for j := 0; j < length; j++ {
		Syy = add32(Syy, shr32(mult16x16(int32(y[j]), int32(y[j])), yshift))
	}
	for i := 0; i < maxPitch; i++ {
		if xcorr[i] > 0 {
			xcorr16 := vshr32(xcorr[i], xshift)
			num := mult16x16q15(int16(xcorr16), int16(xcorr16))
			if mult16x32q15(num, bestDen[1]) > mult16x32q15(bestNum[1], Syy) {
				if mult16x32q15(num, bestDen[0]) > mult16x32q15(bestNum[0], Syy) {
					bestNum[1] = bestNum[0]
					bestDen[1] = bestDen[0]
					bestPitch[1] = bestPitch[0]
					bestNum[0] = num
					bestDen[0] = Syy
					bestPitch[0] = i
				} else {
					bestNum[1] = num
					bestDen[1] = Syy
					bestPitch[1] = i
				}
			}
		}
		Syy += shr32(mult16x16(int32(y[i+length]), int32(y[i+length])), yshift) -
			shr32(mult16x16(int32(y[i]), int32(y[i])), yshift)
		Syy = max32(1, Syy)
	}
}

// pitchSearch ports pitch_search (celt/pitch.c, FIXED_POINT). xLP holds the
// downsampled analysis signal (len samples plus history); y is the reference
// buffer (len+maxPitch samples). It writes the refined pitch lag into *pitch.
func pitchSearch(xLP, y []int16, length, maxPitch int, scratch *celtEncodeScratch) int {
	lag := length + maxPitch

	var xLP4, yLP4 []int16
	var xcorr []int32
	if scratch != nil {
		xLP4 = ensureInt16(&scratch.pitchXLP4, length>>2)
		yLP4 = ensureInt16(&scratch.pitchYLP4, lag>>2)
		xcorr = ensureInt32(&scratch.pitchXcor, maxPitch>>1)
	} else {
		xLP4 = make([]int16, length>>2)
		yLP4 = make([]int16, lag>>2)
		xcorr = make([]int32, maxPitch>>1)
	}

	// Downsample by 2 again.
	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}

	xmax := plcCeltMaxabs16(xLP4)
	ymax := plcCeltMaxabs16(yLP4)
	shift := int(CeltILog2(max32(1, max32(xmax, ymax)))) - 14 + int(CeltILog2(int32(length)))/2
	if shift > 0 {
		for j := 0; j < length>>2; j++ {
			xLP4[j] = int16(shr32(int32(xLP4[j]), shift))
		}
		for j := 0; j < lag>>2; j++ {
			yLP4[j] = int16(shr32(int32(yLP4[j]), shift))
		}
		shift *= 2
	} else {
		shift = 0
	}

	// Coarse search with 4x decimation.
	maxcorr := CeltPitchXcorr(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)

	var bestPitch [2]int
	findBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, 0, maxcorr, &bestPitch)

	// Finer search with 2x decimation.
	maxcorr = 1
	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if iabs(i-2*bestPitch[0]) > 2 && iabs(i-2*bestPitch[1]) > 2 {
			continue
		}
		var sum int32
		for j := 0; j < length>>1; j++ {
			sum += shr32(mult16x16(int32(xLP[j]), int32(y[i+j])), shift)
		}
		xcorr[i] = max32(-1, sum)
		maxcorr = max32(maxcorr, sum)
	}
	findBestPitch(xcorr, y, length>>1, maxPitch>>1, shift+1, maxcorr, &bestPitch)

	// Refine by pseudo-interpolation.
	var offset int
	if bestPitch[0] > 0 && bestPitch[0] < (maxPitch>>1)-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		cc := xcorr[bestPitch[0]+1]
		if cc-a > mult16x32q15(int16(22938), b-a) { // QCONST16(.7f,15) = 22938
			offset = 1
		} else if a-cc > mult16x32q15(int16(22938), b-cc) {
			offset = -1
		} else {
			offset = 0
		}
	} else {
		offset = 0
	}
	return 2*bestPitch[0] - offset
}

// computePitchGain ports compute_pitch_gain (celt/pitch.c, FIXED_POINT).
func computePitchGain(xy, xx, yy int32) int16 {
	if xy == 0 || xx == 0 || yy == 0 {
		return 0
	}
	sx := int(CeltILog2(xx)) - 14
	sy := int(CeltILog2(yy)) - 14
	shift := sx + sy
	x2y2 := shr32(mult16x16(vshr32(xx, sx), vshr32(yy, sy)), 14)
	if shift&1 != 0 {
		if x2y2 < 32768 {
			x2y2 <<= 1
			shift--
		} else {
			x2y2 >>= 1
			shift++
		}
	}
	den := CeltRsqrtNorm(x2y2)
	g := mult16x32q15(den, xy)
	g = vshr32(g, (shift>>1)-1)
	return int16(max32(-int32(q15One), min32(g, int32(q15One))))
}

var removeDoublingSecondCheck = [16]int{0, 0, 3, 2, 3, 2, 5, 2, 3, 2, 3, 2, 5, 2, 3, 2}

// removeDoubling ports remove_doubling (celt/pitch.c, FIXED_POINT). x indexes
// into the analysis buffer; xBase is the index of x[0] used as the pivot
// (libopus advances x by maxperiod, so xBase = maxperiod here). It refines the
// pitch period (*t0) and returns the pitch gain (opus_val16, Q15). Indices
// x[-i] map to x[xBase-i].
func removeDoubling(x []int16, xBase, maxperiod, minperiod, n int, t0 *int, prevPeriod int, prevGain int16, scratch *celtEncodeScratch) int16 {
	minperiod0 := minperiod
	maxperiod /= 2
	minperiod /= 2
	*t0 /= 2
	prevPeriod /= 2
	n /= 2
	xb := xBase + maxperiod
	if *t0 >= maxperiod {
		*t0 = maxperiod - 1
	}

	T := *t0
	T0 := *t0
	var yyLookup []int32
	if scratch != nil {
		yyLookup = ensureInt32(&scratch.pitchYY, maxperiod+1)
	} else {
		yyLookup = make([]int32, maxperiod+1)
	}

	xx, xy := DualInnerProd(x[xb:], x[xb:], x[xb-T0:], n)
	yyLookup[0] = xx
	yy := xx
	for i := 1; i <= maxperiod; i++ {
		yy = yy + mult16x16(int32(x[xb-i]), int32(x[xb-i])) - mult16x16(int32(x[xb+n-i]), int32(x[xb+n-i]))
		yyLookup[i] = max32(0, yy)
	}
	yy = yyLookup[T0]
	bestXY := xy
	bestYY := yy
	g := computePitchGain(xy, xx, yy)
	g0 := g

	for k := 2; k <= 15; k++ {
		T1 := int(celtUdiv(uint32(2*T0+k), uint32(2*k)))
		if T1 < minperiod {
			break
		}
		var T1b int
		if k == 2 {
			if T1+T0 > maxperiod {
				T1b = T0
			} else {
				T1b = T0 + T1
			}
		} else {
			T1b = int(celtUdiv(uint32(2*removeDoublingSecondCheck[k]*T0+k), uint32(2*k)))
		}
		xy1, xy2 := DualInnerProd(x[xb:], x[xb-T1:], x[xb-T1b:], n)
		xy = half32(xy1 + xy2)
		yy = half32(yyLookup[T1] + yyLookup[T1b])
		g1 := computePitchGain(xy, xx, yy)
		var cont int16
		if iabs(T1-prevPeriod) <= 1 {
			cont = prevGain
		} else if iabs(T1-prevPeriod) <= 2 && 5*k*k < T0 {
			cont = half16(prevGain)
		} else {
			cont = 0
		}
		thresh := max16(int16(9830), add16s(mult16x16q15(int16(22938), g0), -cont)) // QCONST16(.3f,15), QCONST16(.7f,15)
		if T1 < 3*minperiod {
			thresh = max16(int16(13107), add16s(mult16x16q15(int16(27853), g0), -cont)) // .4f, .85f
		} else if T1 < 2*minperiod {
			thresh = max16(int16(16384), add16s(mult16x16q15(int16(29491), g0), -cont)) // .5f, .9f
		}
		if g1 > thresh {
			bestXY = xy
			bestYY = yy
			T = T1
			g = g1
		}
	}
	bestXY = max32(0, bestXY)
	var pg int16
	if bestYY <= bestXY {
		pg = q15One
	} else {
		pg = int16(shr32(FracDiv32(bestXY, bestYY+1), 16))
	}

	var xcorr [3]int32
	for k := 0; k < 3; k++ {
		xcorr[k] = CeltInnerProd(x[xb:], x[xb-(T+k-1):], n)
	}
	var offset int
	if xcorr[2]-xcorr[0] > mult16x32q15(int16(22938), xcorr[1]-xcorr[0]) {
		offset = 1
	} else if xcorr[0]-xcorr[2] > mult16x32q15(int16(22938), xcorr[1]-xcorr[2]) {
		offset = -1
	} else {
		offset = 0
	}
	if pg > g {
		pg = g
	}
	*t0 = 2*T + offset
	if *t0 < minperiod0 {
		*t0 = minperiod0
	}
	return pg
}

// PrefilterParams holds the previous-frame prefilter state and per-frame inputs
// to the encode-side prefilter decision, mirroring the relevant CELTEncoder
// fields and run_prefilter arguments under FIXED_POINT.
type PrefilterParams struct {
	// PrefilterPeriod / PrefilterGain / PrefilterTapset are st->prefilter_*.
	PrefilterPeriod int
	PrefilterGain   int16
	PrefilterTapset int

	// Enabled, Complexity, LossRate map to the run_prefilter enabled flag,
	// st->complexity and st->loss_rate.
	Enabled    bool
	Complexity int
	LossRate   int

	// TFEstimate is tf_estimate (opus_val16, Q14); NbAvailableBytes is the
	// remaining byte budget; PrefilterTapsetDecision is st->tapset_decision,
	// which run_prefilter passes as prefilter_tapset.
	TFEstimate              int16
	NbAvailableBytes        int
	PrefilterTapsetDecision int

	// Single-tone fallback inputs: ToneFreq (opus_val16, Q13) and Toneishness
	// (opus_val32, Q29).
	ToneFreq    int16
	Toneishness int32

	// AnalysisValid mirrors st->analysis.valid; MaxPitchRatio is
	// st->analysis.max_pitch_ratio (float) applied when valid.
	AnalysisValid bool
	MaxPitchRatio float32
}

// PrefilterResult holds the decision values produced by PrefilterAnalysis.
type PrefilterResult struct {
	PitchIndex int
	// Gain is gain1 (opus_val16, Q15) returned by run_prefilter.
	Gain int16
	// QG is the quantised gain index in [0,7]; PFOn is the prefilter-on flag.
	QG   int
	PFOn bool
	// Tapset is prefilter_tapset (== PrefilterTapsetDecision), the value emitted.
	Tapset int
}

// PrefilterAnalysis ports the value-producing portion of run_prefilter
// (celt/celt_encoder.c, FIXED_POINT, lines through the gain/qg/tapset decision).
//
// pre holds the assembled per-channel analysis buffers, each of length
// max_period+N (the prefilter_mem history followed by N input samples),
// matching pre[c] in run_prefilter. cc is the channel count, n is the frame
// length. It returns the pitch index, gain, qg, pf_on and tapset that
// run_prefilter would produce.
//
// This does NOT perform the comb_filter prefiltering of the input or the CC==2
// cancel-pitch energy check; those operate on the time-domain signal and are
// wired by the caller using CombFilter/CombFilterConst.
func PrefilterAnalysis(pre [][]int32, cc, n int, p PrefilterParams, scratch *celtEncodeScratch) PrefilterResult {
	maxPeriod := combFilterMaxPeriod
	minPeriod := combFilterMinPeriod

	var pitchIndex int
	var gain1 int16

	if p.Enabled && p.Toneishness > 532676608 { // QCONST32(.99f, 29)
		multiple := 1
		toneFreq := p.ToneFreq
		if int32(toneFreq) >= 25736 { // QCONST16(3.1416f, 13)
			toneFreq = 25736 - toneFreq // QCONST16(3.141593f,13) == 25736
		}
		for int32(toneFreq) >= int32(multiple)*3195 { // QCONST16(0.39f, 13) = 3195
			multiple++
		}
		if int32(toneFreq) > 50 { // QCONST16(0.006148f, 13) = 50
			pitchIndex = imin((51472*multiple+int(toneFreq)/2)/int(toneFreq), combFilterMaxPeriod-2)
		} else {
			pitchIndex = combFilterMinPeriod
		}
		gain1 = int16(24576) // QCONST16(.75f, 15)
	} else if p.Enabled && p.Complexity >= 5 {
		var pitchBuf []int16
		if scratch != nil {
			pitchBuf = ensureInt16(&scratch.pitchBuf, (maxPeriod+n)>>1)
		} else {
			pitchBuf = make([]int16, (maxPeriod+n)>>1)
		}
		pitchDownsample(pre, pitchBuf, (maxPeriod+n)>>1, cc, 2, scratch)
		pitchIndex = pitchSearch(pitchBuf[maxPeriod>>1:], pitchBuf, n, maxPeriod-3*minPeriod, scratch)
		pitchIndex = maxPeriod - pitchIndex

		gain1 = removeDoubling(pitchBuf, 0, maxPeriod, minPeriod, n, &pitchIndex, p.PrefilterPeriod, p.PrefilterGain, scratch)
		if pitchIndex > maxPeriod-2 {
			pitchIndex = maxPeriod - 2
		}
		gain1 = mult16x16q15(int16(22938), gain1) // QCONST16(.7f,15)
		if p.LossRate > 2 {
			gain1 = int16(half32(int32(gain1)))
		}
		if p.LossRate > 4 {
			gain1 = int16(half32(int32(gain1)))
		}
		if p.LossRate > 8 {
			gain1 = 0
		}
	} else {
		gain1 = 0
		pitchIndex = combFilterMinPeriod
	}

	if p.AnalysisValid {
		gain1 = int16(float32(gain1) * p.MaxPitchRatio)
	}

	// Gain threshold for enabling the prefilter/postfilter.
	pfThreshold := int16(6554) // QCONST16(.2f,15)
	if iabs(pitchIndex-p.PrefilterPeriod)*10 > pitchIndex {
		pfThreshold += 6554       // +QCONST16(.2f,15)
		if p.TFEstimate > 16056 { // QCONST16(.98f, 14)
			gain1 = 0
		}
	}
	if p.NbAvailableBytes < 25 {
		pfThreshold += 3277 // QCONST16(.1f,15)
	}
	if p.NbAvailableBytes < 35 {
		pfThreshold += 3277
	}
	if p.PrefilterGain > 13107 { // QCONST16(.4f,15)
		pfThreshold -= 3277
	}
	if p.PrefilterGain > 18022 { // QCONST16(.55f,15)
		pfThreshold -= 3277
	}
	pfThreshold = max16(pfThreshold, 6554) // MAX16(.2f)

	var qg int
	var pfOn bool
	if gain1 < pfThreshold {
		gain1 = 0
		pfOn = false
		qg = 0
	} else {
		if int16(abs32(int32(gain1)-int32(p.PrefilterGain))) < 3277 { // ABS16(...) < QCONST16(.1f,15)
			gain1 = p.PrefilterGain
		}
		qg = int((int(gain1)+1536)>>10)/3 - 1
		qg = imax(0, imin(7, qg))
		gain1 = int16(3072 * (qg + 1)) // QCONST16(0.09375f,15) = 3072
		pfOn = true
	}

	return PrefilterResult{
		PitchIndex: pitchIndex,
		Gain:       gain1,
		QG:         qg,
		PFOn:       pfOn,
		Tapset:     p.PrefilterTapsetDecision,
	}
}

// EmitPrefilterParams emits the post-filter parameters to the range encoder
// exactly as celt/celt_encoder.c does after run_prefilter (the pf_on branch).
// hybrid, tell and totalBits gate the pf_on==0 flag emission as in the encoder.
// When pfOn is true the octave/period/gain/tapset are written; the caller must
// have already established the same encoder state libopus has at that point.
func EmitPrefilterParams(enc *rangecoding.Encoder, r PrefilterResult, hybrid bool, tell, totalBits int) {
	if !r.PFOn {
		if !hybrid && tell+16 <= totalBits {
			enc.EncodeBit(0, 1)
		}
		return
	}
	enc.EncodeBit(1, 1)
	pitchIndex := r.PitchIndex + 1
	octave := int(ecILog(int32(pitchIndex))) - 5 // EC_ILOG(x)-5; ecILog == EC_ILOG
	enc.EncodeUniform(uint32(octave), 6)
	enc.EncodeRawBits(uint32(pitchIndex-(16<<octave)), uint(4+octave))
	enc.EncodeRawBits(uint32(r.QG), 3)
	enc.EncodeICDF(r.Tapset, tapsetICDF, 2)
}
