//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer (FIXED_POINT) CELT encode-side allocation-decision
// analysis kernels from celt/celt_encoder.c (libopus 1.6.1):
//
//   - l1_metric   : the L1 cost used by tf_analysis.
//   - TFAnalysis  : the time/frequency resolution Viterbi search; returns
//                   tf_select and fills tf_res[start..len).
//   - TFEncode    : codes the tf_res / tf_select decisions into the range coder.
//   - AllocTrimAnalysis : the allocation-trim decision derived from inter-channel
//                   correlation, spectral tilt, surround trim, tf_estimate and the
//                   optional float analysis tonality slope.
//
// Type model (celt/arch.h FIXED_POINT): celt_norm, celt_glog and opus_val32 are
// int32, opus_val16 is int16. NORM_SHIFT and DB_SHIFT are both 24. The
// truncating multiply macros (MULT16_16, MULT16_16_Q14/Q15, MAC16_32_Q15) cast
// their 16-bit operand(s) to int16 first; that truncation is reproduced exactly.

import "github.com/thesyncim/gopus/internal/rangecoding"

// tfSelectTable ports celt/celt.c tf_select_table[4][8] (signed char). Indexed
// by LM (0..3); the inner index is 4*isTransient + 2*tf_select + tf_res.
var tfSelectTable = [4][8]int{
	{0, -1, 0, -1, 0, -1, 0, -1}, // 2.5 ms
	{0, -1, 0, -2, 1, 0, 1, -1},  // 5 ms
	{0, -2, 0, -3, 2, 0, 1, -1},  // 10 ms
	{0, -2, 0, -3, 3, 0, 1, -1},  // 20 ms
}

// l1Metric ports celt/celt_encoder.c l1_metric (FIXED_POINT). tmp holds N
// celt_norm values; bias is opus_val16 (Q15).
func l1Metric(tmp []int32, n, lm int, bias int16) int32 {
	var l1 int32
	for i := 0; i < n; i++ {
		// L1 += EXTEND32(ABS16(SHR32(tmp[i], NORM_SHIFT-14)))
		l1 += abs32(shr32(tmp[i], normShift-14))
	}
	// L1 = MAC16_32_Q15(L1, LM*bias, L1)
	// MAC16_32_Q15(c,a,b) = c + (MULT16_16(a, b>>15) + (MULT16_16(a, b&0x7fff)>>15))
	// where a is truncated to int16.
	a := int32(int16(lm * int(bias)))
	l1 = mac16x32Q15(l1, a, l1)
	return l1
}

// mac16x32Q15 ports MAC16_32_Q15(c,a,b): a is truncated to int16, b is a 32-bit
// value, and the product is accumulated in Q15.
func mac16x32Q15(c, a, b int32) int32 {
	// ADD32(c, ADD32(MULT16_16(a, SHR(b,15)), SHR(MULT16_16(a, b&0x7fff), 15)))
	// where MULT16_16 truncates both operands to int16.
	hi := int32(int16(a)) * int32(int16(b>>15))
	lo := (int32(int16(a)) * int32(int16(b&0x00007fff))) >> 15
	return c + (hi + lo)
}

// TFAnalysis ports celt/celt_encoder.c tf_analysis (FIXED_POINT). It selects the
// per-band time/frequency resolution via a Viterbi search and returns tf_select.
//
//	eBands     mode band boundaries (m->eBands), at least len+1 entries.
//	length     m len: number of bands to analyse (eBands index span).
//	X          normalised MDCT bins (celt_norm), interleaved by channel at N0.
//	N0         per-channel stride into X (m->shortMdctSize<<LM in callers).
//	LM         log2 of the number of short MDCTs.
//	tfEstimate opus_val16 (Q14) transient estimate.
//	tfChan     channel index selected by transient_analysis.
//	importance per-band importance weights (length len).
//	tfRes      output decisions, length len.
func TFAnalysis(eBands []int16, length int, isTransient bool, tfRes []int, lambda int, x []int32, n0, lm int, tfEstimate int16, tfChan int, importance []int, scratch *celtEncodeScratch) int {
	// bias = MULT16_16_Q14(QCONST16(.04f,15), MAX16(-QCONST16(.25f,14), QCONST16(.5f,14)-tf_estimate))
	const q04Q15 = int16(1311) // QCONST16(.04f,15)  = .5+.04*32768
	const q25Q14 = int16(4096) // QCONST16(.25f,14)
	const q05Q14 = int16(8192) // QCONST16(.5f,14)
	inner := max16(-q25Q14, q05Q14-tfEstimate)
	bias := int16(mult16x16Q14(int32(q04Q15), int32(inner)))

	tmpLen := (int(eBands[length]) - int(eBands[length-1])) << lm
	var metric, path0, path1 []int
	var tmp, tmp1 []int32
	if scratch != nil {
		metric = ensureInt(&scratch.tfMetric, length)
		tmp = ensureInt32(&scratch.tfTmp, tmpLen)
		tmp1 = ensureInt32(&scratch.tfTmp1, tmpLen)
		path0 = ensureInt(&scratch.tfPath0, length)
		path1 = ensureInt(&scratch.tfPath1, length)
	} else {
		metric = make([]int, length)
		tmp = make([]int32, tmpLen)
		tmp1 = make([]int32, tmpLen)
		path0 = make([]int, length)
		path1 = make([]int, length)
	}

	itr := 0
	if isTransient {
		itr = 1
	}

	for i := 0; i < length; i++ {
		n := (int(eBands[i+1]) - int(eBands[i])) << lm
		narrow := (int(eBands[i+1]) - int(eBands[i])) == 1
		copy(tmp[:n], x[tfChan*n0+(int(eBands[i])<<lm):])

		lmForL1 := 0
		if isTransient {
			lmForL1 = lm
		}
		l1 := l1Metric(tmp, n, lmForL1, bias)
		bestL1 := l1
		bestLevel := 0

		if isTransient && !narrow {
			copy(tmp1[:n], tmp[:n])
			haar1(tmp1, n>>lm, 1<<lm)
			l1 = l1Metric(tmp1, n, lm+1, bias)
			if l1 < bestL1 {
				bestL1 = l1
				bestLevel = -1
			}
		}

		kMax := lm
		if !(isTransient || narrow) {
			kMax = lm + 1
		}
		for k := 0; k < kMax; k++ {
			var b int
			if isTransient {
				b = lm - k - 1
			} else {
				b = k + 1
			}
			haar1(tmp, n>>k, 1<<k)
			l1 = l1Metric(tmp, n, b, bias)
			if l1 < bestL1 {
				bestL1 = l1
				bestLevel = k + 1
			}
		}

		if isTransient {
			metric[i] = 2 * bestLevel
		} else {
			metric[i] = -2 * bestLevel
		}
		if narrow && (metric[i] == 0 || metric[i] == -2*lm) {
			metric[i]--
		}
	}

	// Search for the optimal tf resolution, including tf_select.
	tfSelect := 0
	var selcost [2]int
	for sel := 0; sel < 2; sel++ {
		cost0 := importance[0] * iabs(metric[0]-2*tfSelectTable[lm][4*itr+2*sel+0])
		lamTrans := lambda
		if isTransient {
			lamTrans = 0
		}
		cost1 := importance[0]*iabs(metric[0]-2*tfSelectTable[lm][4*itr+2*sel+1]) + lamTrans
		for i := 1; i < length; i++ {
			curr0 := imin(cost0, cost1+lambda)
			curr1 := imin(cost0+lambda, cost1)
			cost0 = curr0 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*itr+2*sel+0])
			cost1 = curr1 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*itr+2*sel+1])
		}
		cost0 = imin(cost0, cost1)
		selcost[sel] = cost0
	}
	if selcost[1] < selcost[0] && isTransient {
		tfSelect = 1
	}

	cost0 := importance[0] * iabs(metric[0]-2*tfSelectTable[lm][4*itr+2*tfSelect+0])
	lamTrans := lambda
	if isTransient {
		lamTrans = 0
	}
	cost1 := importance[0]*iabs(metric[0]-2*tfSelectTable[lm][4*itr+2*tfSelect+1]) + lamTrans
	for i := 1; i < length; i++ {
		from0 := cost0
		from1 := cost1 + lambda
		var curr0 int
		if from0 < from1 {
			curr0 = from0
			path0[i] = 0
		} else {
			curr0 = from1
			path0[i] = 1
		}

		from0 = cost0 + lambda
		from1 = cost1
		var curr1 int
		if from0 < from1 {
			curr1 = from0
			path1[i] = 0
		} else {
			curr1 = from1
			path1[i] = 1
		}
		cost0 = curr0 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*itr+2*tfSelect+0])
		cost1 = curr1 + importance[i]*iabs(metric[i]-2*tfSelectTable[lm][4*itr+2*tfSelect+1])
	}
	if cost0 < cost1 {
		tfRes[length-1] = 0
	} else {
		tfRes[length-1] = 1
	}
	for i := length - 2; i >= 0; i-- {
		if tfRes[i+1] == 1 {
			tfRes[i] = path1[i+1]
		} else {
			tfRes[i] = path0[i+1]
		}
	}
	return tfSelect
}

// TFEncode ports celt/celt_encoder.c tf_encode (FIXED_POINT). It encodes the
// tf_res differential decisions and the tf_select bit, then rewrites tf_res into
// the resolution offsets via tf_select_table. tfRes has at least end entries.
func TFEncode(start, end int, isTransient bool, tfRes []int, lm, tfSelect int, enc *rangecoding.Encoder) {
	itr := 0
	if isTransient {
		itr = 1
	}
	budget := enc.StorageBits()
	tell := enc.Tell()
	logp := 4
	if isTransient {
		logp = 2
	}
	tfSelectRsv := 0
	if lm > 0 && tell+logp+1 <= budget {
		tfSelectRsv = 1
	}
	budget -= tfSelectRsv
	curr := 0
	tfChanged := 0
	for i := start; i < end; i++ {
		if tell+logp <= budget {
			enc.EncodeBit(tfRes[i]^curr, uint(logp))
			tell = enc.Tell()
			curr = tfRes[i]
			tfChanged |= curr
		} else {
			tfRes[i] = curr
		}
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}
	if tfSelectRsv != 0 &&
		tfSelectTable[lm][4*itr+0+tfChanged] != tfSelectTable[lm][4*itr+2+tfChanged] {
		enc.EncodeBit(tfSelect, 1)
	} else {
		tfSelect = 0
	}
	for i := start; i < end; i++ {
		tfRes[i] = tfSelectTable[lm][4*itr+2*tfSelect+tfRes[i]]
	}
}

// AllocTrimResult holds the outputs of AllocTrimAnalysis.
type AllocTrimResult struct {
	TrimIndex    int
	StereoSaving int16
}

// AllocTrimAnalysis ports celt/celt_encoder.c alloc_trim_analysis (FIXED_POINT).
//
//	eBands        mode band boundaries (m->eBands), at least end+1 entries.
//	X             normalised MDCT bins, channel-interleaved at N0.
//	bandLogE      per-band log energies (celt_glog, Q24), channel-interleaved at nbEBands.
//	nbEBands      m->nbEBands stride for bandLogE.
//	stereoSaving  opus_val16 (Q8) running stereo-saving estimate (in/out).
//	tfEstimate    opus_val16 (Q14).
//	surroundTrim  celt_glog (Q24).
//	analysisValid / analysisTonalitySlope mirror AnalysisInfo (float API).
func AllocTrimAnalysis(eBands []int16, x []int32, bandLogE []int32, end, lm, c, n0, nbEBands int, stereoSaving int16, tfEstimate int16, intensity int, surroundTrim int32, equivRate int32, analysisValid bool, analysisTonalitySlope float32) AllocTrimResult {
	// trim = QCONST16(5.f, 8)
	trim := int16(1280)
	if equivRate < 64000 {
		trim = 1024 // QCONST16(4.f, 8)
	} else if equivRate < 80000 {
		frac := (equivRate - 64000) >> 10
		// QCONST16(4.f,8) + QCONST16(1.f/16.f,8)*frac = 1024 + 16*frac
		trim = int16(1024 + 16*frac)
	}

	out := AllocTrimResult{StereoSaving: stereoSaving}

	if c == 2 {
		var sum int16 // Q10
		for i := 0; i < 8; i++ {
			lo := int(eBands[i]) << lm
			n := (int(eBands[i+1]) - int(eBands[i])) << lm
			partial := celtInnerProdNormShift(x[lo:], x[n0+lo:], n)
			// sum = ADD16(sum, EXTRACT16(SHR32(partial, 18)))
			sum = sum + int16(partial>>18)
		}
		// sum = MULT16_16_Q15(QCONST16(1.f/8, 15), sum)
		sum = mult16x16q15(4096, sum) // QCONST16(.125f,15)=4096
		// sum = MIN16(QCONST16(1.f, 10), ABS16(sum))
		sum = min16(1024, abs16(sum))
		minXC := sum
		for i := 8; i < intensity; i++ {
			lo := int(eBands[i]) << lm
			n := (int(eBands[i+1]) - int(eBands[i])) << lm
			partial := celtInnerProdNormShift(x[lo:], x[n0+lo:], n)
			minXC = min16(minXC, abs16(int16(partial>>18)))
		}
		minXC = min16(1024, abs16(minXC))

		// logXC = celt_log2(QCONST32(1.001f, 20)-MULT16_16(sum, sum))
		const q1001Q20 = int32(1049624) // QCONST32(1.001f,20)=.5+1.001*(1<<20)
		logXC := int32(CeltLog2(q1001Q20 - mult16x16(int32(sum), int32(sum))))
		// logXC2 = MAX16(HALF16(logXC), celt_log2(QCONST32(1.001f,20)-MULT16_16(minXC,minXC)))
		logXC2 := max32(logXC>>1, int32(CeltLog2(q1001Q20-mult16x16(int32(minXC), int32(minXC)))))

		// Compensate for Q20 vs Q14 input and convert output to Q8.
		// logXC = PSHR32(logXC-QCONST16(6.f,10), 10-8); QCONST16(6.f,10)=6144
		logXC = int32(int16(pshr32(logXC-6144, 2)))
		logXC2 = int32(int16(pshr32(logXC2-6144, 2)))

		// trim += MAX16(-QCONST16(4.f,8), MULT16_16_Q15(QCONST16(.75f,15), logXC))
		// QCONST16(.75f,15)=24576, QCONST16(4.f,8)=1024
		add := max16(-1024, int16(mult16x16Q15i32(24576, logXC)))
		trim = trim + add

		// *stereo_saving = MIN16(*stereo_saving + QCONST16(0.25f,8), -HALF16(logXC2))
		// QCONST16(0.25f,8)=64
		out.StereoSaving = min16(stereoSaving+64, int16(-(logXC2 >> 1)))
	}

	// Estimate spectral tilt.
	var diff int32
	for cc := 0; cc < c; cc++ {
		for i := 0; i < end-1; i++ {
			diff += (bandLogE[i+cc*nbEBands] >> 5) * int32(2+2*i-end)
		}
	}
	diff /= int32(c * (end - 1))

	// trim -= MAX32(-QCONST16(2.f,8), MIN32(QCONST16(2.f,8), SHR32(diff+QCONST32(1.f,DB_SHIFT-5),DB_SHIFT-13)/6))
	// QCONST16(2.f,8)=512, QCONST32(1.f,19)=524288, DB_SHIFT-13=11
	tilt := max32(-512, min32(512, (diff+524288)>>11/6))
	trim = int16(int32(trim) - tilt)
	// trim -= SHR16(surround_trim, DB_SHIFT-8) = surround_trim>>16
	trim = int16(int32(trim) - (surroundTrim >> 16))
	// trim -= 2*SHR16(tf_estimate, 14-8) = 2*(tf_estimate>>6)
	trim = int16(int32(trim) - 2*(int32(tfEstimate)>>6))

	if analysisValid {
		// trim -= MAX16(-QCONST16(2.f,8), MIN16(QCONST16(2.f,8),
		//        (opus_val16)(QCONST16(2.f,8)*(tonality_slope+.05f))))
		v := int16(float32(512) * (analysisTonalitySlope + 0.05))
		trim = int16(int32(trim) - int32(max16(-512, min16(512, v))))
	}

	// trim_index = PSHR32(trim, 8) = (trim+128)>>8
	trimIndex := int(pshr32(int32(trim), 8))
	trimIndex = imax(0, imin(10, trimIndex))
	out.TrimIndex = trimIndex
	return out
}

// max16 ports MAX16 over int16.
func max16(a, b int16) int16 {
	if a > b {
		return a
	}
	return b
}

// min16 ports MIN16 over int16.
func min16(a, b int16) int16 {
	if a < b {
		return a
	}
	return b
}
