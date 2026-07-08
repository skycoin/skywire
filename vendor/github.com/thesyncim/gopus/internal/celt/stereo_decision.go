package celt

var (
	// Reference: libopus celt_encoder.c intensity_thresholds/intensity_histeresis.
	celtIntensityThresholds = [...]int{
		1, 2, 3, 4, 5, 6, 7, 8, 16, 24, 36,
		44, 50, 56, 62, 67, 72, 79, 88, 106, 134,
	}
	celtIntensityHysteresis = [...]int{
		1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2,
		2, 2, 2, 3, 3, 4, 5, 6, 8, 8,
	}
)

// hysteresisDecisionInt mirrors libopus hysteresis_decision() for integer thresholds.
func hysteresisDecisionInt(val int, thresholds, hysteresis []int, prev int) int {
	n := len(thresholds)
	if n == 0 || len(hysteresis) < n {
		return 0
	}
	if prev < 0 {
		prev = 0
	}
	if prev > n {
		prev = n
	}

	i := 0
	for ; i < n; i++ {
		if val < thresholds[i] {
			break
		}
	}
	if i > prev && prev < n && val < thresholds[prev]+hysteresis[prev] {
		i = prev
	}
	if i < prev && prev > 0 && val > thresholds[prev-1]-hysteresis[prev-1] {
		i = prev
	}
	return i
}

// stereoAnalysisDecision mirrors libopus stereo_analysis() and returns dual-stereo usage.
func stereoAnalysisDecision(normL, normR []celtNorm, lm, nbBands int) bool {
	if lm < 0 {
		lm = 0
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if len(normL) == 0 || len(normR) == 0 || nbBands == 0 {
		return false
	}

	maxBand := 13
	if nbBands < maxBand {
		maxBand = nbBands
	}
	if maxBand <= 0 || len(EBands) <= 13 {
		return false
	}

	const eps = float32(1e-12)
	sumLR := eps
	sumMS := eps
	for band := 0; band < maxBand; band++ {
		bandStart := EBands[band] << lm
		bandEnd := EBands[band+1] << lm
		if bandStart >= len(normL) || bandStart >= len(normR) {
			break
		}
		if bandEnd > len(normL) {
			bandEnd = len(normL)
		}
		if bandEnd > len(normR) {
			bandEnd = len(normR)
		}
		for j := bandStart; j < bandEnd; j++ {
			l := normL[j]
			r := normR[j]
			m := l + r
			s := l - r
			sumLR += absCeltNorm(l) + absCeltNorm(r)
			sumMS += absCeltNorm(m) + absCeltNorm(s)
		}
	}
	sumMS *= 0.70710677 // sqrt(1/2)

	thetas := 13
	if lm <= 1 {
		thetas -= 8
	}
	base := EBands[13] << (lm + 1)
	return float32(base+thetas)*sumMS > float32(base)*sumLR
}
