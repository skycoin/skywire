package celt

func antiCollapseGLog(
	coeffsL, coeffsR []celtNorm,
	collapse []byte,
	lm int,
	channels int,
	start, end int,
	logE []celtGLog,
	prev1LogE, prev2LogE []celtGLog,
	pulses []int32,
	seed uint32,
) {
	antiCollapseGLogMode(coeffsL, coeffsR, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed, EBands[:], MaxBands)
}

// antiCollapseGLogMode is antiCollapseGLog parameterized by the per-mode band
// edges and band count. With edges==EBands[:] and nbEBands==MaxBands it is the
// static 48 kHz path verbatim. The prev1LogE/prev2LogE arrays keep the MaxBands
// per-channel stride the rest of the decoder uses to size them.
func antiCollapseGLogMode(
	coeffsL, coeffsR []celtNorm,
	collapse []byte,
	lm int,
	channels int,
	start, end int,
	logE []celtGLog,
	prev1LogE, prev2LogE []celtGLog,
	pulses []int32,
	seed uint32,
	edges []int,
	nbEBands int,
) {
	if channels < 1 || channels > 2 {
		return
	}
	if len(edges) < 2 || nbEBands <= 0 {
		edges = EBands[:]
		nbEBands = MaxBands
	}
	if start < 0 {
		start = 0
	}
	if end > nbEBands {
		end = nbEBands
	}
	if end <= start {
		return
	}
	if len(collapse) < channels*nbEBands {
		return
	}

	M := 1 << lm
	if M <= 0 {
		return
	}

	logEStride := end
	if channels > 0 && len(logE) > 0 {
		logEStride = max(len(logE)/channels, end)
	}

	for band := start; band < end; band++ {
		N0 := edges[band+1] - edges[band]
		if N0 <= 0 {
			continue
		}
		if band >= len(pulses) {
			break
		}

		depth := celtUdiv(1+int(pulses[band]), N0) >> lm
		thresh := float32(0.5) * celtExp2(float32(-0.125)*float32(depth))
		sqrt1 := celtRSqrt(float32(N0 << lm))
		bandOffset := edges[band] << lm
		bandLen := N0 << lm

		for c := range channels {
			logIdx := c*logEStride + band
			if logIdx >= len(logE) {
				continue
			}
			// oldLogE history is strided by the mode's nbEBands (libopus
			// oldLogE[i+c*m->nbEBands]): MaxBands for the static 48 kHz tables,
			// the per-mode nbEBands for a non-standard custom layout.
			prevStride := nbEBands
			prevIdx := c*prevStride + band
			if prevIdx >= len(prev1LogE) || prevIdx >= len(prev2LogE) {
				continue
			}

			prev1 := prev1LogE[prevIdx]
			prev2 := prev2LogE[prevIdx]
			if channels == 1 && len(prev1LogE) >= 2*prevStride && len(prev2LogE) >= 2*prevStride {
				alt1 := prev1LogE[prevStride+band]
				if alt1 > prev1 {
					prev1 = alt1
				}
				alt2 := prev2LogE[prevStride+band]
				if alt2 > prev2 {
					prev2 = alt2
				}
			}
			prevMin := prev1
			if prev2 < prevMin {
				prevMin = prev2
			}
			ediff := float32(logE[logIdx] - prevMin)
			if ediff < 0 {
				ediff = 0
			}

			r := float32(2.0) * celtExp2(-ediff)
			if lm == 3 {
				r *= 1.41421356
			}
			if r > thresh {
				r = thresh
			}
			r *= sqrt1
			if r <= 0 {
				continue
			}

			coeffs := coeffsL
			if c != 0 {
				coeffs = coeffsR
			}
			if bandOffset+bandLen > len(coeffs) {
				continue
			}

			mask := collapse[band*channels+c]
			renorm := false
			coeff := r
			for k := range M {
				if (mask & (1 << uint(k))) != 0 {
					continue
				}
				for j := range N0 {
					seed = seed*1664525 + 1013904223
					if (seed & 0x8000) != 0 {
						coeffs[bandOffset+(j<<lm)+k] = celtNorm(coeff)
					} else {
						coeffs[bandOffset+(j<<lm)+k] = celtNorm(-coeff)
					}
				}
				renorm = true
			}
			if renorm {
				renormalizeVector(coeffs[bandOffset:bandOffset+bandLen], 1.0)
			}
		}
	}
}
