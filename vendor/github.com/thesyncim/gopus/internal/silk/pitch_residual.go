package silk

func autocorrelationF32(out, in []float32, length, order int) {
	if length <= 0 || order <= 0 {
		return
	}
	_ = in[length-1]
	_ = out[order-1]
	for k := range order {
		cnt := length - k
		out[k] = float32(innerProductF32Libopus(in[:cnt], in[k:k+cnt], cnt))
	}
}

func schurF32(refl, autoCorr []float32, order int) float32 {
	if order <= 0 {
		if len(autoCorr) > 0 {
			return autoCorr[0]
		}
		return 0
	}
	if order > maxShapeLpcOrder {
		order = maxShapeLpcOrder
	}
	if order >= len(autoCorr) {
		order = len(autoCorr) - 1
	}
	if order > len(refl) {
		order = len(refl)
	}
	if order <= 0 {
		if len(autoCorr) > 0 {
			return autoCorr[0]
		}
		return 0
	}
	// Match libopus silk/float/schur_FLP.c: C is a C double work array.
	var C [maxShapeLpcOrder + 1][2]silkCReal
	for k := 0; k <= order; k++ {
		C[k][0] = silkCReal(autoCorr[k])
		C[k][1] = silkCReal(autoCorr[k])
	}
	// Match libopus silk_max_float(C[0][1], 1e-9f):
	// compare against float32 literal, then use that exact value in double domain.
	minDen := silkCReal(float32(1e-9))
	for k := 0; k < order; k++ {
		den := C[0][1]
		if den < minDen {
			den = minDen
		}
		rc := -C[k+1][0] / den
		refl[k] = float32(rc)
		for n := 0; n < order-k; n++ {
			c1 := C[n+k+1][0]
			c2 := C[n][1]
			C[n+k+1][0] = c1 + c2*rc
			C[n][1] = c2 + c1*rc
		}
	}
	return float32(C[0][1])
}

func k2aF32(a, rc []float32, order int) {
	for k := range order {
		rck := rc[k]
		for n := 0; n < (k+1)/2; n++ {
			tmp1 := a[n]
			tmp2 := a[k-n-1]
			a[n] = tmp1 + tmp2*rck
			a[k-n-1] = tmp2 + tmp1*rck
		}
		a[k] = -rck
	}
}

func bwexpanderF32(ar []float32, order int, chirp float32) {
	cfac := chirp
	for i := 0; i < order-1; i++ {
		ar[i] *= cfac
		cfac *= chirp
	}
	if order > 0 {
		ar[order-1] *= cfac
	}
}

func lpcAnalysisFilterF32(rLPC, predCoef, s []float32, length, order int) {
	if order > length {
		return
	}
	// BCE hints: ensure all slice accesses in the unrolled loops are in-bounds.
	_ = rLPC[length-1]
	_ = s[length-1]
	_ = predCoef[order-1]
	switch order {
	case 6:
		// Cache coefficients in locals to avoid repeated slice indexing.
		a0, a1, a2, a3, a4, a5 := predCoef[0], predCoef[1], predCoef[2], predCoef[3], predCoef[4], predCoef[5]
		for ix := 6; ix < length; ix++ {
			lpcPred := s[ix-1]*a0 + s[ix-2]*a1 + s[ix-3]*a2 +
				s[ix-4]*a3 + s[ix-5]*a4 + s[ix-6]*a5
			rLPC[ix] = s[ix] - lpcPred
		}
	case 8:
		// Cache coefficients in locals to avoid repeated slice indexing.
		b0, b1, b2, b3 := predCoef[0], predCoef[1], predCoef[2], predCoef[3]
		b4, b5, b6, b7 := predCoef[4], predCoef[5], predCoef[6], predCoef[7]
		for ix := 8; ix < length; ix++ {
			lpcPred := s[ix-1]*b0 + s[ix-2]*b1 + s[ix-3]*b2 + s[ix-4]*b3 +
				s[ix-5]*b4 + s[ix-6]*b5 + s[ix-7]*b6 + s[ix-8]*b7
			rLPC[ix] = s[ix] - lpcPred
		}
	case 10:
		// Cache coefficients in locals to avoid repeated slice indexing.
		q0, q1, q2, q3, q4 := predCoef[0], predCoef[1], predCoef[2], predCoef[3], predCoef[4]
		q5, q6, q7, q8, q9 := predCoef[5], predCoef[6], predCoef[7], predCoef[8], predCoef[9]
		for ix := 10; ix < length; ix++ {
			lpcPred := s[ix-1]*q0 + s[ix-2]*q1 + s[ix-3]*q2 + s[ix-4]*q3 + s[ix-5]*q4 +
				s[ix-6]*q5 + s[ix-7]*q6 + s[ix-8]*q7 + s[ix-9]*q8 + s[ix-10]*q9
			rLPC[ix] = s[ix] - lpcPred
		}
	case 12:
		// Cache coefficients in locals to avoid repeated slice indexing.
		r0, r1, r2, r3 := predCoef[0], predCoef[1], predCoef[2], predCoef[3]
		r4, r5, r6, r7 := predCoef[4], predCoef[5], predCoef[6], predCoef[7]
		r8, r9, r10, r11 := predCoef[8], predCoef[9], predCoef[10], predCoef[11]
		for ix := 12; ix < length; ix++ {
			lpcPred := s[ix-1]*r0 + s[ix-2]*r1 + s[ix-3]*r2 + s[ix-4]*r3 +
				s[ix-5]*r4 + s[ix-6]*r5 + s[ix-7]*r6 + s[ix-8]*r7 +
				s[ix-9]*r8 + s[ix-10]*r9 + s[ix-11]*r10 + s[ix-12]*r11
			rLPC[ix] = s[ix] - lpcPred
		}
	case 16:
		// Cache coefficients in locals to avoid repeated slice indexing.
		p0, p1, p2, p3 := predCoef[0], predCoef[1], predCoef[2], predCoef[3]
		p4, p5, p6, p7 := predCoef[4], predCoef[5], predCoef[6], predCoef[7]
		p8, p9, p10, p11 := predCoef[8], predCoef[9], predCoef[10], predCoef[11]
		p12, p13, p14, p15 := predCoef[12], predCoef[13], predCoef[14], predCoef[15]
		for ix := 16; ix < length; ix++ {
			lpcPred := s[ix-1]*p0 + s[ix-2]*p1 + s[ix-3]*p2 + s[ix-4]*p3 +
				s[ix-5]*p4 + s[ix-6]*p5 + s[ix-7]*p6 + s[ix-8]*p7 +
				s[ix-9]*p8 + s[ix-10]*p9 + s[ix-11]*p10 + s[ix-12]*p11 +
				s[ix-13]*p12 + s[ix-14]*p13 + s[ix-15]*p14 + s[ix-16]*p15
			rLPC[ix] = s[ix] - lpcPred
		}
	default:
		for ix := order; ix < length; ix++ {
			var lpcPred float32
			for k := range order {
				lpcPred += s[ix-k-1] * predCoef[k]
			}
			rLPC[ix] = s[ix] - lpcPred
		}
	}
	for i := range order {
		rLPC[i] = 0
	}
}

func applySineWindowFLP32(pxWin, px []float32, winType, length int) {
	if length == 0 || length&3 != 0 {
		return
	}
	// Match libopus: freq = PI / (length + 1) where PI = 3.1415926536f (float32).
	// Compute in float32 to match C float / float precision.
	const piF32 = float32(3.1415926536)
	freq := piF32 / float32(length+1)
	// Approximation of 2 * cos(f)
	c := float32(2.0) - freq*freq

	var S0, S1 float32
	if winType < 2 {
		S0 = 0
		S1 = freq
	} else {
		S0 = 1
		S1 = 0.5 * c
	}

	for k := 0; k < length; k += 4 {
		pxWin[k+0] = px[k+0] * 0.5 * (S0 + S1)
		pxWin[k+1] = px[k+1] * S1
		S0 = c*S1 - S0
		pxWin[k+2] = px[k+2] * 0.5 * (S1 + S0)
		pxWin[k+3] = px[k+3] * S0
		S1 = c*S0 - S1
	}
}

type pitchResidualInfo struct {
	predGain  float32
	autoCorr0 float32
	resNrg    float32
}

func (e *Encoder) computePitchResidual(numSubframes int) ([]float32, int, int, pitchResidualInfo) {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000
	subframeSamples := config.SubframeSamples
	frameSamples := numSubframes * subframeSamples
	if frameSamples <= 0 {
		return nil, 0, 0, pitchResidualInfo{}
	}

	ltpMemSamples := ltpMemLengthMs * fsKHz
	histLen := ltpMemSamples + frameSamples
	laPitch := laPitchMs * fsKHz
	needed := histLen + laPitch

	// Use the SILK analysis buffer (x_buf in libopus). This already contains
	// LTP memory + LA_SHAPE lookahead + current frame. LA_PITCH is covered
	// by the LA_SHAPE region (LA_SHAPE >= LA_PITCH).
	input32 := ensureFloat32Slice(&e.scratchPitchInput32, needed)
	src := e.inputBuffer
	// Split into two loops to eliminate per-sample bounds check.
	copyLen := min(needed, len(src))
	if copyLen > 0 {
		_ = src[copyLen-1]    // BCE hint
		_ = input32[needed-1] // BCE hint
		for i := range copyLen {
			input32[i] = src[i] * silkSampleScale
		}
	}
	for i := copyLen; i < needed; i++ {
		input32[i] = 0
	}

	order := int(e.pitchEstimationLPCOrder)
	if order == 0 {
		order = int(e.lpcOrder)
	}
	if order > maxFindPitchLpcOrder {
		order = maxFindPitchLpcOrder
	}
	if order <= 0 {
		residual32 := ensureFloat32Slice(&e.scratchPitchRes32, needed)
		copy(residual32, input32)
		resStart := max(histLen-frameSamples, 0)
		return residual32, resStart, frameSamples, pitchResidualInfo{}
	}

	pitchWinMs := findPitchLpcWinMs
	if numSubframes == 2 {
		pitchWinMs = findPitchLpcWinMs2SF
	}
	pitchWinLen := min(pitchWinMs*fsKHz, needed)
	if laPitch*2 > pitchWinLen {
		laPitch = pitchWinLen / 2
	}

	Wsig := ensureFloat32Slice(&e.scratchPitchWsig32, pitchWinLen)
	xBufPtr := input32[needed-pitchWinLen:]
	if laPitch > 0 {
		applySineWindowFLP32(Wsig[:laPitch], xBufPtr, 1, laPitch)
		middleLen := pitchWinLen - 2*laPitch
		if middleLen > 0 {
			copy(Wsig[laPitch:laPitch+middleLen], xBufPtr[laPitch:laPitch+middleLen])
		}
		applySineWindowFLP32(Wsig[pitchWinLen-laPitch:], xBufPtr[pitchWinLen-laPitch:], 2, laPitch)
	} else {
		copy(Wsig, xBufPtr)
	}

	autoCorr := ensureFloat32Slice(&e.scratchPitchAuto32, order+1)
	autocorrelationF32(autoCorr, Wsig, pitchWinLen, order+1)
	autoCorr[0] += autoCorr[0]*float32(findPitchWhiteNoiseFraction) + 1.0

	refl := ensureFloat32Slice(&e.scratchPitchRefl32, order)
	resNrg := schurF32(refl, autoCorr, order)

	// Prediction gain (matching libopus silk_find_pitch_lags_FLP)
	// libopus: psEncCtrl->predGain = auto_corr[0] / silk_max_float(res_nrg, 1.0f)
	// This is silk_float / silk_float = float32 division.
	resNrgClamped := resNrg
	if resNrgClamped < 1.0 {
		resNrgClamped = 1.0
	}
	predGainF32 := autoCorr[0] / resNrgClamped
	e.lastLPCGain = predGainF32
	info := pitchResidualInfo{
		predGain:  predGainF32,
		autoCorr0: autoCorr[0],
		resNrg:    resNrg,
	}

	a := ensureFloat32Slice(&e.scratchPitchA32, order)
	for i := range a {
		a[i] = 0
	}
	k2aF32(a, refl, order)
	bwexpanderF32(a, order, float32(findPitchBandwidthExpansion))

	residual32 := ensureFloat32Slice(&e.scratchPitchRes32, needed)
	lpcAnalysisFilterF32(residual32, a, input32, needed, order)

	resStart := max(histLen-frameSamples, 0)

	return residual32, resStart, frameSamples, info
}
