//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer CELT FIXED_POINT transient detector:
// transient_analysis and patch_transient_decision from celt/celt_encoder.c.
//
// Type model (celt/arch.h FIXED_POINT, QEXT off): opus_val16 is int16;
// opus_val32, celt_sig and celt_glog are int32; the squared-sample accumulator
// stays in int32. DB_SHIFT is 24, so GCONST(1.0f) == 1<<24.
//
// transient_analysis takes the time-domain input (in[c*len+i], celt_sig/int32)
// for C channels of len samples and detects forward-masked transients via a
// high-pass filter, a forward/backward envelope follower, and a bitrate-
// normalised temporal noise-to-mask ratio. It returns is_transient and writes
// tf_estimate (Q14), tf_chan and weak_transient.

// invTransientTable is the libopus 6*64/x table trained to minimise average
// error, indexed by the clamped masking ratio.
var invTransientTable = [128]uint8{
	255, 255, 156, 110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
	23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
	12, 12, 11, 11, 11, 10, 10, 10, 9, 9, 9, 9, 9, 9, 8, 8,
	8, 8, 8, 7, 7, 7, 7, 7, 7, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2,
}

// transientDBShift is libopus DB_SHIFT for the FIXED_POINT, QEXT-off build.
// NOTE(dedup): the package has no exported DB_SHIFT constant; defined locally.
const transientDBShift = 24

// transientPSHR32 implements libopus PSHR32(a,shift): round-half-up arithmetic
// right shift. NOTE(dedup): a package-local rounding shift exists only inside
// other kernels; this distinct helper avoids redeclaration.
func transientPSHR32(a int32, shift uint) int32 {
	return (a + (int32(1) << shift >> 1)) >> shift
}

// transientSROUND16 implements libopus SROUND16(x,a) = EXTRACT16(SATURATE(
// PSHR32(x,a), 32767)): round-half-up shift, clamp to [-32767,32767], truncate
// to int16.
func transientSROUND16(x int32, a uint) int16 {
	v := transientPSHR32(x, a)
	if v > 32767 {
		v = 32767
	} else if v < -32767 {
		v = -32767
	}
	return int16(v)
}

// transientMaxabs16 mirrors libopus celt_maxabs16: MAX32 of the running max and
// the negated running min over an int16 slice. NOTE(dedup): plcCeltMaxabs16 is
// identical but lives behind the PLC kernel; a distinct name avoids coupling.
func transientMaxabs16(x []int16) int32 {
	var maxval, minval int16
	for _, v := range x {
		if v > maxval {
			maxval = v
		}
		if v < minval {
			minval = v
		}
	}
	return max32(int32(maxval), -int32(minval))
}

// mult16_32Q15 implements the FIXED_POINT MULT16_32_Q15(a,b) =
// (opus_val32)SHR((opus_int64)(opus_val16)a * b, 15).
func transientMult16_32Q15(a int16, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 15)
}

// TransientAnalysisResult bundles the outputs of TransientAnalysis.
type TransientAnalysisResult struct {
	IsTransient   bool
	TFEstimate    int16 // Q14
	TFChan        int
	WeakTransient bool
}

// TransientAnalysis ports the FIXED_POINT celt/celt_encoder.c transient_analysis.
//
// in holds C*len celt_sig (int32) time-domain samples laid out as in[c*len+i].
// allowWeakTransients enables the conservative forward-masking decay and the
// weak-transient reclassification used at low bitrates. toneFreq is Q13 and
// toneishness is Q29; they suppress false transients from very low tones.
func TransientAnalysis(in []int32, length, c int, allowWeakTransients bool, toneFreq int16, toneishness int32, scratch *celtEncodeScratch) TransientAnalysisResult {
	const forwardShiftDefault = 4
	forwardShift := uint(forwardShiftDefault)

	// in_shift = IMAX(0, celt_ilog2(1+celt_maxabs32(in, C*len))-14)
	inShift := int(CeltILog2(1+CeltMaxabs32(in[:c*length])) - 14)
	if inShift < 0 {
		inShift = 0
	}

	var weakTransient bool
	if allowWeakTransients {
		forwardShift = 5
	}

	len2 := length / 2
	var tmp []int16
	if scratch != nil {
		tmp = ensureInt16(&scratch.transientTmp, length)
	} else {
		tmp = make([]int16, length)
	}

	tfChan := 0
	var maskMetric int32

	for ch := 0; ch < c; ch++ {
		var mem0, mem1 int32

		// High-pass filter: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + .5*z^-2).
		for i := 0; i < length; i++ {
			x := in[i+ch*length] >> uint(inShift)
			y := mem0 + x
			mem0 = mem1 + y - (x << 1)
			mem1 = x - (y >> 1)
			tmp[i] = transientSROUND16(y, 2)
		}
		// First few samples are bad because we don't propagate the memory.
		for i := 0; i < 12; i++ {
			tmp[i] = 0
		}

		// Normalize tmp to max range.
		{
			m := transientMaxabs16(tmp[:length])
			if m < 1 {
				m = 1
			}
			shift := 14 - int(CeltILog2(m))
			if shift != 0 {
				for i := 0; i < length; i++ {
					tmp[i] = int16(uint16(tmp[i]) << uint(shift))
				}
			}
		}

		var mean int32
		mem0 = 0
		// Forward pass to compute the post-echo threshold (grouping by two).
		for i := 0; i < len2; i++ {
			x2 := transientPSHR32(int32(tmp[2*i])*int32(tmp[2*i])+int32(tmp[2*i+1])*int32(tmp[2*i+1]), 4)
			mean += transientPSHR32(x2, 12)
			mem0 = mem0 + transientPSHR32(x2-mem0, forwardShift)
			tmp[i] = int16(transientPSHR32(mem0, 12))
		}

		mem0 = 0
		var maxE int16
		// Backward pass to compute the pre-echo threshold (13.9 dB/ms).
		for i := len2 - 1; i >= 0; i-- {
			mem0 = mem0 + transientPSHR32((int32(tmp[i])<<4)-mem0, 3)
			tmp[i] = int16(transientPSHR32(mem0, 4))
			if tmp[i] > maxE {
				maxE = tmp[i]
			}
		}

		// Frame energy is the geometric mean of the energy and half the max;
		// costs two sqrt() to avoid overflows. MULT16_16 truncates each sqrt to
		// int16 before the 32-bit product.
		mean = int32(int16(CeltSqrt(mean))) * int32(int16(CeltSqrt(int32(maxE)*int32(len2>>1))))

		// Inverse of the mean energy in Q15+6.
		norm := (int32(len2) << (6 + 14)) / (1 + (mean >> 1))

		// Harmonic mean discarding the unreliable boundaries; the data is
		// smooth, so we only take 1/4th of the samples.
		var unmask int32
		for i := 12; i < len2-5; i += 4 {
			// id = MAX32(0, MIN32(127, MULT16_32_Q15(tmp[i]+EPSILON, norm)))
			id := transientMult16_32Q15(tmp[i]+1, norm)
			if id < 0 {
				id = 0
			} else if id > 127 {
				id = 127
			}
			unmask += int32(invTransientTable[id])
		}
		// Normalize, compensate for the 1/4th of the sample and the factor of 6
		// in the inverse table.
		unmask = 64 * unmask * 4 / (6 * (int32(len2) - 17))
		if unmask > maskMetric {
			tfChan = ch
			maskMetric = unmask
		}
	}

	isTransient := maskMetric > 200
	// Prevent confusing the partial cycle of a very low frequency tone with a
	// transient. QCONST32(.98f,29)=526133719, QCONST16(0.026f,13)=213.
	if toneishness > 526133494 && toneFreq < 213 {
		isTransient = false
		maskMetric = 0
	}
	// Weak transients need to be handled differently to avoid partial collapse.
	if allowWeakTransients && isTransient && maskMetric < 600 {
		isTransient = false
		weakTransient = true
	}

	// Arbitrary metric for VBR boost.
	// tf_max = MAX16(0, celt_sqrt(27*mask_metric)-42)
	tfMaxVal := CeltSqrt(27*maskMetric) - 42
	if tfMaxVal < 0 {
		tfMaxVal = 0
	}
	tfMax := int16(tfMaxVal)

	// *tf_estimate = celt_sqrt(MAX32(0,
	//   SHL32(MULT16_16(QCONST16(0.0069,14), MIN16(163, tf_max)), 14)
	//   - QCONST32(0.139,28)))
	// QCONST16(0.0069,14)=113, QCONST32(0.139,28)=37312528.
	minTF := tfMax
	if minTF > 163 {
		minTF = 163
	}
	inner := ((int32(113) * int32(minTF)) << 14) - 37312528
	if inner < 0 {
		inner = 0
	}
	tfEstimate := int16(CeltSqrt(inner))

	return TransientAnalysisResult{
		IsTransient:   isTransient,
		TFEstimate:    tfEstimate,
		TFChan:        tfChan,
		WeakTransient: weakTransient,
	}
}

// PatchTransientDecision ports the FIXED_POINT celt/celt_encoder.c
// patch_transient_decision: it spreads the previous frame's band energies with
// an aggressive -6 dB/Bark function and reports whether the mean energy increase
// versus the current frame exceeds 1 dB (GCONST(1.f) == 1<<DB_SHIFT).
//
// newE and oldE hold C*nbEBands celt_glog (Q24) band energies laid out as
// E[c*nbEBands+i]. The result decides whether to force a transient frame.
func PatchTransientDecision(newE, oldE []int32, nbEBands, start, end, c int) bool {
	const gconst1 = int32(1) << transientDBShift

	var spreadOld [26]int32
	if c == 1 {
		spreadOld[start] = oldE[start]
		for i := start + 1; i < end; i++ {
			spreadOld[i] = max32(spreadOld[i-1]-gconst1, oldE[i])
		}
	} else {
		spreadOld[start] = max32(oldE[start], oldE[start+nbEBands])
		for i := start + 1; i < end; i++ {
			spreadOld[i] = max32(spreadOld[i-1]-gconst1, max32(oldE[i], oldE[i+nbEBands]))
		}
	}
	for i := end - 2; i >= start; i-- {
		spreadOld[i] = max32(spreadOld[i], spreadOld[i+1]-gconst1)
	}

	imaxStart := start
	if imaxStart < 2 {
		imaxStart = 2
	}

	var meanDiff int32
	for ch := 0; ch < c; ch++ {
		for i := imaxStart; i < end-1; i++ {
			// x1, x2 are opus_val16 in libopus: the MAXG (int32) result is
			// truncated to int16 on assignment.
			x1 := int16(max32(0, newE[i+ch*nbEBands]))
			x2 := int16(max32(0, spreadOld[i]))
			meanDiff += max32(0, int32(x1)-int32(x2))
		}
	}
	meanDiff = meanDiff / int32(c*(end-1-imaxStart))
	return meanDiff > gconst1
}
