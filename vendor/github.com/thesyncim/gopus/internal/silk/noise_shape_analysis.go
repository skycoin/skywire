package silk

// noiseShapeAnalysis computes noise shaping parameters, gains, and sparseness-based
// quantization offset selection. This is a float port of the remaining pieces in
// silk_noise_shape_analysis_FLP.c.
func (e *Encoder) noiseShapeAnalysis(
	pcm []float32,
	pitchRes []float32,
	pitchResStart int,
	signalType int,
	speechActivityQ8 int,
	lpcPredGain float32,
	pitchLags []int32,
	quantOffset int,
	numSubframes int,
	subframeSamples int,
) (*NoiseShapeParams, []float32, int) {
	if e.noiseShapeState == nil {
		e.noiseShapeState = NewNoiseShapeState()
	}

	fsKHz := max(int(e.sampleRate/1000), 8)

	quantOffsetType := quantOffset
	if signalType == typeVoiced {
		// For voiced, start at 0; process_gains may override.
		quantOffsetType = 0
	} else {
		if offset, ok := computeSparsenessQuantOffset(pitchRes, pitchResStart, fsKHz, numSubframes); ok {
			quantOffsetType = offset
		}
	}

	inputQualityBandsQ15 := [4]int32{-1, -1, -1, -1}
	if e.speechActivitySet {
		inputQualityBandsQ15 = e.inputQualityBandsQ15
	}

	// Compute average input quality from first two bands (matches libopus psEncCtrl->input_quality)
	var inputQuality float32
	if inputQualityBandsQ15[0] >= 0 {
		inputQuality = 0.5 * (float32(inputQualityBandsQ15[0]) + float32(inputQualityBandsQ15[1])) / 32768.0
	} else {
		inputQuality = float32(speechActivityQ8) / 256.0
	}

	// SNR adjustment for gain tweaking and coding quality.
	// Match libopus: SNR_adj_dB and all intermediates are silk_float (float32).
	snrDB := float32(e.snrDBQ7) * (1.0 / 128.0)
	SNRAdjDB := snrDB
	b := float32(1.0) - float32(speechActivityQ8)*(1.0/256.0)
	// Initial estimate for coding quality to match recursive dependency in libopus
	initialCodingQuality := Sigmoid(0.25 * (snrDB - 20.0))
	if !e.useCBR {
		SNRAdjDB -= float32(bgSNRDecrDB) * initialCodingQuality * (0.5 + 0.5*inputQuality) * b * b
	}
	if signalType == typeVoiced {
		SNRAdjDB += float32(harmSNRIncrDB) * e.ltpCorr
	} else {
		SNRAdjDB += (-0.4*snrDB + 6.0) * (1.0 - inputQuality)
	}

	params := e.noiseShapeState.ComputeNoiseShapeParams(
		signalType,
		speechActivityQ8,
		e.ltpCorr,
		pitchLags,
		snrDB,
		quantOffsetType,
		inputQualityBandsQ15,
		numSubframes,
		fsKHz,
		int(e.nStatesDelayedDecision),
	)

	gains, arShpQ13 := e.computeShapingARAndGains(
		pcm,
		numSubframes,
		subframeSamples,
		lpcPredGain,
		SNRAdjDB,
		signalType,
		speechActivityQ8,
		params.CodingQuality,
		params.InputQuality,
	)
	params.ARShpQ13 = arShpQ13

	return params, gains, quantOffsetType
}

func computeSparsenessQuantOffset(pitchRes []float32, start, fsKHz, numSubframes int) (int, bool) {
	if fsKHz <= 0 || numSubframes <= 0 {
		return 0, false
	}
	if len(pitchRes) == 0 {
		return 0, false
	}

	nSamples := 2 * fsKHz
	if nSamples <= 0 {
		return 0, false
	}

	nSegs := (subFrameLengthMs * numSubframes) / 2
	if nSegs <= 0 {
		return 0, false
	}

	if start < 0 {
		start = 0
	}
	if start >= len(pitchRes) {
		return 0, false
	}

	pitchResFrame := pitchRes[start:]
	maxSegs := len(pitchResFrame) / nSamples
	if maxSegs <= 0 {
		return 0, false
	}
	if nSegs > maxSegs {
		nSegs = maxSegs
	}

	// Match libopus: nrg, log_energy, log_energy_prev, energy_variation are all silk_float (float32).
	energyVariation := float32(0)
	logEnergyPrev := float32(0)
	for k := 0; k < nSegs; k++ {
		seg := pitchResFrame[k*nSamples : (k+1)*nSamples]
		// libopus: nrg = (silk_float)nSamples + (silk_float)silk_energy_FLP(...)
		nrg := float32(nSamples) + float32(energyF32Libopus(seg, nSamples))
		// libopus: silk_log2() returns silk_float.
		logEnergy := silkLog2F32(nrg)
		if k > 0 {
			diff := logEnergy - logEnergyPrev
			if diff < 0 {
				diff = -diff
			}
			energyVariation += diff
		}
		logEnergyPrev = logEnergy
	}

	threshold := float32(energyVariationThresholdQntOffset) * float32(nSegs-1)
	if energyVariation > threshold {
		return 0, true
	}
	return 1, true
}

func (e *Encoder) computeShapingARAndGains(
	pcm []float32,
	numSubframes int,
	subframeSamples int,
	lpcPredGain float32,
	SNRAdjDB float32,
	signalType int,
	speechActivityQ8 int,
	codingQuality float32,
	inputQuality float32,
) ([]float32, []int16) {
	gains := ensureFloat32Slice(&e.scratchGains, numSubframes)
	arShpQ13 := ensureInt16Slice(&e.scratchArShpQ13, numSubframes*maxShapeLpcOrder)
	if numSubframes == 0 || subframeSamples <= 0 || len(pcm) == 0 {
		clear(arShpQ13)
		for i := range gains {
			gains[i] = 1.0
		}
		return gains, arShpQ13
	}

	shapeOrder := int(e.shapingLPCOrder)
	if shapeOrder <= 0 {
		shapeOrder = int(e.lpcOrder)
	}
	if shapeOrder > maxShapeLpcOrder {
		shapeOrder = maxShapeLpcOrder
	}
	if shapeOrder < 2 {
		shapeOrder = 2
	}
	if shapeOrder&1 != 0 {
		shapeOrder--
	}

	fsKHz := max(int(e.sampleRate/1000), 1)

	laShape := max(int(e.laShape), 0)

	frameSamples := min(numSubframes*subframeSamples, len(pcm))
	if frameSamples <= 0 {
		clear(arShpQ13)
		for i := range gains {
			gains[i] = 1.0
		}
		return gains, arShpQ13
	}

	shapeWinLength := subframeSamples + 2*laShape
	if shapeWinLength <= 0 {
		clear(arShpQ13)
		for i := range gains {
			gains[i] = 1.0
		}
		return gains, arShpQ13
	}

	xLen := frameSamples + 2*laShape
	xBuf := ensureFloat32Slice(&e.scratchPitchInput32, xLen)

	// Populate xBuf from the SILK analysis buffer (x_buf in libopus).
	// libopus noise shaping uses x_ptr = x - la_shape, where x points to x_frame
	// (x_buf + ltp_mem). Align our window to that same origin.
	src := e.inputBuffer
	start := max((ltpMemLengthMs*fsKHz - laShape), 0)
	if start < len(src) {
		copyLen := xLen
		if remaining := len(src) - start; copyLen > remaining {
			copyLen = remaining
		}
		for i, sample := range src[start : start+copyLen] {
			xBuf[i] = sample * float32(silkSampleScale)
		}
		clear(xBuf[copyLen:])
	} else {
		clear(xBuf)
	}
	// Bandwidth expansion and warping in float32 precision to mirror libopus FLP behavior.
	strengthF32 := float32(findPitchWhiteNoiseFraction) * lpcPredGain
	BWExp := float32(bandwidthExpansion) / (1.0 + strengthF32*strengthF32)
	warping := float32(e.warpingQ16)/65536.0 + 0.01*codingQuality

	flatPart := fsKHz * 3
	slopePart := max((shapeWinLength-flatPart)/2, 0)

	win := ensureFloat32Slice(&e.scratchPitchWsig32, shapeWinLength)
	autoCorr := ensureFloat32Slice(&e.scratchPitchAuto32, shapeOrder+1)
	rc := ensureFloat32Slice(&e.scratchPitchRefl32, shapeOrder+1)
	ar := ensureFloat32Slice(&e.scratchPitchA32, shapeOrder)

	for k := range numSubframes {
		offset := k * subframeSamples
		segment := xBuf[offset : offset+shapeWinLength]

		if slopePart > 0 && slopePart*2+flatPart == shapeWinLength {
			applySineWindowFLP32(win[:slopePart], segment[:slopePart], 1, slopePart)
			copy(win[slopePart:slopePart+flatPart], segment[slopePart:slopePart+flatPart])
			applySineWindowFLP32(win[slopePart+flatPart:], segment[slopePart+flatPart:], 2, slopePart)
		} else {
			copy(win, segment)
		}

		if e.warpingQ16 > 0 {
			warpedAutocorrelationFLP32(autoCorr, nil, win, warping, shapeWinLength, shapeOrder)
		} else {
			autocorrelationF32(autoCorr, win, shapeWinLength, shapeOrder+1)
		}

		autoCorr[0] += autoCorr[0]*float32(shapeWhiteNoiseFraction) + 1.0

		nrg := schurF32(rc, autoCorr, shapeOrder)
		for i := range ar {
			ar[i] = 0
		}
		k2aF32(ar, rc, shapeOrder)

		g := float32(0)
		if nrg > 0 {
			g = sqrt32(nrg)
		}

		if e.warpingQ16 > 0 {
			g *= warpedGainF32(ar, warping, shapeOrder)
		}

		bwexpanderF32(ar, shapeOrder, BWExp)
		if e.warpingQ16 > 0 {
			warpedTrue2MonicCoefsF32(ar, warping, float32(shapeCoefLimit), shapeOrder)
		} else {
			limitCoefsF32(ar, float32(shapeCoefLimit), shapeOrder)
		}

		gains[k] = g
		base := k * maxShapeLpcOrder
		for i := 0; i < shapeOrder; i++ {
			// Match libopus: silk_float2int(AR[i] * 8192.0f) - multiply in float32.
			arShpQ13[base+i] = int16(float32ToInt32RoundEven(ar[i] * 8192.0))
		}
		for i := shapeOrder; i < maxShapeLpcOrder; i++ {
			arShpQ13[base+i] = 0
		}
	}

	// Match libopus: gain_mult = (silk_float)pow(2.0f, -0.16f * SNR_adj_dB)
	gainMult := exp2F32(-0.16 * SNRAdjDB)
	gainAdd := exp2F32(0.16 * float32(minQGainDb))

	for k := range numSubframes {
		// Match libopus two-step operation:
		//   psEncCtrl->Gains[k] *= gain_mult;   // step 1: multiply with intermediate rounding
		//   psEncCtrl->Gains[k] += gain_add;     // step 2: add
		// Go's compiler can fuse sequential *= then += into a single FMADDS
		// instruction (one rounding), but clang compiles these as separate
		// FMUL + FADD (two roundings). Use noFMA32 to force intermediate
		// rounding and match the C behavior exactly.
		gains[k] = noFMA32(gains[k], gainMult) + gainAdd
	}

	return gains, arShpQ13
}

// warpedAutocorrelationFLP32 mirrors silk/float/warped_autocorrelation_FLP.c:
// input/output are silk_float, while the local state/correlation accumulators are C double.
func warpedAutocorrelationFLP32(out, state, in []float32, warping float32, length, order int) {
	if order&1 != 0 {
		order--
	}
	if order <= 0 {
		return
	}
	if order > maxShapeLpcOrder {
		order = maxShapeLpcOrder
	}

	var st [maxShapeLpcOrder + 1]silkCReal
	var corr [maxShapeLpcOrder + 1]silkCReal
	w := silkCReal(warping)

	// Clamp input slice so the compiler proves all in[n] accesses are in bounds.
	if length > len(in) {
		length = len(in)
	}
	in = in[:length]
	_ = st[order]   // BCE hint for inner loop array access
	_ = corr[order] // BCE hint for inner loop array access

	for _, sample := range in {
		tmp1 := silkCReal(sample)
		// First iteration (i=0): sets st[0] then uses it for all remaining.
		tmp2 := st[0] + w*st[1] - w*tmp1
		st[0] = tmp1
		st0 := tmp1 // Cache st[0] in a register for the inner loop.
		corr[0] += st0 * tmp1
		tmp1 = st[1] + w*st[2] - w*tmp2
		st[1] = tmp2
		corr[1] += st0 * tmp2
		for i := 2; i < order; i += 2 {
			tmp2 = st[i] + w*st[i+1] - w*tmp1
			st[i] = tmp1
			corr[i] += st0 * tmp1
			tmp1 = st[i+1] + w*st[i+2] - w*tmp2
			st[i+1] = tmp2
			corr[i+1] += st0 * tmp2
		}
		st[order] = tmp1
		corr[order] += st0 * tmp1
	}

	maxOut := min(order+1, len(out))
	for i := range maxOut {
		out[i] = float32(corr[i])
	}
	maxState := min(order+1, len(state))
	for i := range maxState {
		state[i] = float32(st[i])
	}
}

// warpedGainF32 matches libopus warped_gain() which operates in silk_float (float32).
func warpedGainF32(coefs []float32, lambda float32, order int) float32 {
	lambda = -lambda
	if order <= 0 {
		return 1.0
	}
	gain := coefs[order-1]
	for i := order - 2; i >= 0; i-- {
		gain = lambda*gain + coefs[i]
	}
	return 1.0 / (1.0 - lambda*gain)
}

// warpedTrue2MonicCoefsF32 matches libopus warped_true2monic_coefs() in silk_float (float32).
func warpedTrue2MonicCoefsF32(coefs []float32, lambda, limit float32, order int) {
	if order <= 0 {
		return
	}
	for i := order - 1; i > 0; i-- {
		coefs[i-1] -= lambda * coefs[i]
	}
	gain := (1.0 - lambda*lambda) / (1.0 + lambda*coefs[0])
	for i := range order {
		coefs[i] *= gain
	}

	for iter := range 10 {
		maxabs := float32(-1.0)
		ind := 0
		for i := range order {
			tmp := coefs[i]
			if tmp < 0 {
				tmp = -tmp
			}
			if tmp > maxabs {
				maxabs = tmp
				ind = i
			}
		}
		if maxabs <= limit {
			return
		}

		for i := 1; i < order; i++ {
			coefs[i-1] += lambda * coefs[i]
		}
		gain = 1.0 / gain
		for i := range order {
			coefs[i] *= gain
		}

		chirp := 0.99 - (0.8+0.1*float32(iter))*(maxabs-limit)/(maxabs*float32(ind+1))
		bwexpanderF32(coefs, order, chirp)

		for i := order - 1; i > 0; i-- {
			coefs[i-1] -= lambda * coefs[i]
		}
		gain = (1.0 - lambda*lambda) / (1.0 + lambda*coefs[0])
		for i := range order {
			coefs[i] *= gain
		}
	}
}

// limitCoefsF32 matches libopus limit_coefs() in silk_float (float32).
func limitCoefsF32(coefs []float32, limit float32, order int) {
	if order <= 0 {
		return
	}
	for iter := range 10 {
		maxabs := float32(-1.0)
		ind := 0
		for i := range order {
			tmp := coefs[i]
			if tmp < 0 {
				tmp = -tmp
			}
			if tmp > maxabs {
				maxabs = tmp
				ind = i
			}
		}
		if maxabs <= limit {
			return
		}
		chirp := 0.99 - (0.8+0.1*float32(iter))*(maxabs-limit)/(maxabs*float32(ind+1))
		bwexpanderF32(coefs, order, chirp)
	}
}
