package celt

import "github.com/thesyncim/gopus/internal/opusmath"

type allocTrimDetail struct {
	base     opusVal16
	stereo   opusVal16
	tilt     opusVal16
	surround celtGLog
	tf       opusVal16
	tonal    opusVal16
	raw      opusVal16
}

// AllocTrimAnalysis returns the CELT allocation trim index in [0, 10]. Higher
// values favor lower frequency bands. It follows alloc_trim_analysis in
// libopus celt/celt_encoder.c.
//
// normCoeffs contains normalized mono or left-channel MDCT coefficients;
// normCoeffsRight contains the right channel, or is nil for mono. bandLogE
// contains nbBands log energies per channel. intensity is the first band that
// uses intensity stereo, or nbBands when intensity stereo is disabled. lm is
// the log2 multiplier of the short-transform size.
//
// equivRate is the equivalent bitrate in bits per second. tfEstimate is the
// transient-analysis estimate in [0, 1]; surroundTrim is the surround allocation
// adjustment. A zero tonalitySlope disables the optional analysis adjustment.
func AllocTrimAnalysis(
	normCoeffs []celtNorm,
	bandLogE []celtGLog,
	nbBands int,
	lm int,
	channels int,
	normCoeffsRight []celtNorm,
	intensity int,
	tfEstimate opusVal16,
	equivRate int,
	surroundTrim celtGLog,
	tonalitySlope opusVal16,
) int {
	trimIndex, _ := allocTrimAnalysisDetailed(
		normCoeffs,
		bandLogE,
		nbBands,
		lm,
		channels,
		normCoeffsRight,
		intensity,
		tfEstimate,
		equivRate,
		surroundTrim,
		tonalitySlope,
		tonalitySlope != 0,
		EBands[:],
	)
	return trimIndex
}

func allocTrimAnalysisDetailed(
	normCoeffs []celtNorm,
	bandLogE []celtGLog,
	nbBands int,
	lm int,
	channels int,
	normCoeffsRight []celtNorm,
	intensity int,
	tfEstimate opusVal16,
	equivRate int,
	surroundTrim celtGLog,
	tonalitySlope opusVal16,
	analysisValid bool,
	edges []int,
) (int, allocTrimDetail) {
	detail := allocTrimDetail{}

	// Start with default trim of 5
	trim := opusVal16(5.0)
	detail.base = trim

	// Bitrates below 80 kbit/s reduce the baseline trim according to
	// celt/celt_encoder.c:alloc_trim_analysis.
	if equivRate < 64000 {
		trim = opusVal16(4.0)
		detail.base = trim
	} else if equivRate < 80000 {
		frac := opusVal16((equivRate - 64000) >> 10)
		trim = opusVal16(4.0) + opusVal16(1.0/16.0)*frac
		detail.base = trim
	}

	// Stereo correlation adjustment
	// Reference: libopus lines 884-920
	if channels == 2 && normCoeffsRight != nil && len(normCoeffs) > 0 && len(normCoeffsRight) > 0 {
		logXC := computeStereoCorrelationTrim(normCoeffs, normCoeffsRight, nbBands, lm, intensity, edges)

		// trim += max(-4, 0.75 * logXC)
		stereoAdjust := opusVal16(0.75) * logXC
		if stereoAdjust < opusVal16(-4.0) {
			stereoAdjust = opusVal16(-4.0)
		}
		trim += stereoAdjust
		detail.stereo = stereoAdjust
	}

	// Spectral tilt adjustment
	// Reference: libopus lines 922-931
	// The spectral tilt measures whether energy is concentrated in low or high frequencies.
	// Positive diff indicates a tilt toward higher bands; negative diff
	// indicates a tilt toward lower bands.
	var diff opusVal32
	end := min(nbBands, len(bandLogE)/channels)

	for c := range channels {
		for i := 0; i < end-1; i++ {
			idx := i + c*nbBands
			if idx < len(bandLogE) {
				// Weight each band by its position relative to center
				// Lower bands (small i) get negative weights, higher bands get positive weights
				// libopus: diff += (bandLogE[i+c*nbEBands] >> 5) * (2 + 2*i - end)
				weight := opusVal32(2 + 2*i - end)
				diff += opusVal32(bandLogE[idx]) * weight
			}
		}
	}
	diff /= opusVal32(channels * (end - 1))

	// Apply spectral tilt adjustment, clamped to [-2, 2] range
	// libopus: trim -= max(-2, min(2, (diff+1)/6))
	tiltAdjust := opusVal16((diff + opusVal32(1.0)) / opusVal32(6.0))
	if tiltAdjust < opusVal16(-2.0) {
		tiltAdjust = opusVal16(-2.0)
	}
	if tiltAdjust > opusVal16(2.0) {
		tiltAdjust = opusVal16(2.0)
	}

	trim -= tiltAdjust
	detail.tilt = tiltAdjust

	// Surround trim adjustment
	// Reference: libopus line 932
	// surround_trim is in dB, typically 0 for non-surround encoding
	trim -= surroundTrim
	detail.surround = surroundTrim

	// TF estimate adjustment
	// Reference: libopus line 933: trim -= 2*SHR16(tf_estimate, 14-8)
	// tf_estimate is in Q14 format in libopus, we use float [0, 1]
	// So: trim -= 2 * tf_estimate
	tfAdjust := opusVal16(2.0) * tfEstimate
	trim -= tfAdjust
	detail.tf = tfAdjust

	// Tonality slope adjustment (optional, from analysis)
	// Reference: libopus lines 935-939
	// tonality_slope ranges from about -0.25 to 0.25 in practice
	// libopus: trim -= max(-2, min(2, 2*(tonality_slope + 0.05)))
	if analysisValid {
		tonalAdjust := opusVal16(2.0) * (tonalitySlope + opusVal16(0.05))
		if tonalAdjust < opusVal16(-2.0) {
			tonalAdjust = opusVal16(-2.0)
		}
		if tonalAdjust > opusVal16(2.0) {
			tonalAdjust = opusVal16(2.0)
		}
		trim -= tonalAdjust
		detail.tonal = tonalAdjust
	}
	detail.raw = trim

	// Convert to integer with rounding and clamp to valid range
	// Reference: libopus lines 947-949
	trimIndex := min(max(int(trim+opusVal16(0.5)), 0), 10)

	return trimIndex, detail
}

// computeStereoCorrelationTrim computes the stereo correlation adjustment for alloc_trim.
// It measures inter-channel correlation to estimate mid-side coding savings.
//
// Reference: libopus celt/celt_encoder.c alloc_trim_analysis() lines 884-920
func computeStereoCorrelationTrim(normL, normR []celtNorm, nbBands, lm, intensity int, edges []int) opusVal16 {
	logXC, _ := computeStereoCorrelationLogs(normL, normR, nbBands, lm, intensity, edges)
	return logXC
}

func computeStereoCorrelationLogs(normL, normR []celtNorm, nbBands, lm, intensity int, edges []int) (opusVal16, opusVal16) {
	// Compute inter-channel correlation for low frequencies (first 8 bands)
	// libopus uses inner product of normalized coefficients between channels

	var sum opusVal16

	// Compute correlation for first 8 bands
	for band := 0; band < 8 && band < nbBands; band++ {
		bandStart := edges[band] << lm
		bandEnd := edges[band+1] << lm

		if bandStart >= len(normL) || bandStart >= len(normR) {
			break
		}
		if bandEnd > len(normL) {
			bandEnd = len(normL)
		}
		if bandEnd > len(normR) {
			bandEnd = len(normR)
		}

		partial := celtInnerProdNorm(normL, normR, bandStart, bandEnd)
		sum += partial
	}
	// Match libopus: always divide by 8 low bands in the average.
	sum *= opusVal16(1.0 / 8.0)

	// Clamp sum to [-1, 1]
	if sum > opusVal16(1.0) {
		sum = opusVal16(1.0)
	}
	if sum < opusVal16(-1.0) {
		sum = opusVal16(-1.0)
	}
	if sum < 0 {
		sum = -sum
	}

	// Also compute minimum correlation across higher bands (up to intensity threshold)
	minXC := sum
	for band := 8; band < intensity && band < nbBands; band++ {
		bandStart := edges[band] << lm
		bandEnd := edges[band+1] << lm

		if bandStart >= len(normL) || bandStart >= len(normR) {
			break
		}
		if bandEnd > len(normL) {
			bandEnd = len(normL)
		}
		if bandEnd > len(normR) {
			bandEnd = len(normR)
		}

		partial := celtInnerProdNorm(normL, normR, bandStart, bandEnd)
		if partial < 0 {
			partial = -partial
		}

		if partial < minXC {
			minXC = partial
		}
	}
	if minXC > opusVal16(1.0) {
		minXC = opusVal16(1.0)
	}

	// Compute log correlation: log2(1.001 - sum^2)
	// This gives a negative value; higher correlation = more negative
	logXCArg := opusVal32(1.001) - opusVal32(sum)*opusVal32(sum)
	logXC := opusVal16(opusmath.CeltLog2(logXCArg))
	logXC2Arg := opusVal32(1.001) - opusVal32(minXC)*opusVal32(minXC)
	logXC2 := opusVal16(opusmath.CeltLog2(logXC2Arg))
	halfLogXC := opusVal16(0.5) * logXC
	if halfLogXC > logXC2 {
		logXC2 = halfLogXC
	}

	return logXC, logXC2
}

// UpdateStereoSaving updates the running stereo_saving estimate used by libopus
// compute_vbr(). The state is updated once per frame after alloc-trim analysis:
// stereo_saving = min(stereo_saving+0.25, -logXC2/2) (celt/celt_encoder.c:919).
// The state is unbounded; compute_vbr caps the value it reads at 1.
func UpdateStereoSaving(prev opusVal16, normL, normR []celtNorm, nbBands, lm, intensity int, edges []int) OpusVal16 {
	if len(normL) == 0 || len(normR) == 0 || nbBands <= 0 {
		return prev
	}
	if intensity < 0 {
		intensity = 0
	}
	if intensity > nbBands {
		intensity = nbBands
	}

	_, logXC2 := computeStereoCorrelationLogs(normL, normR, nbBands, lm, intensity, edges)
	return min(prev+0.25, -(0.5 * logXC2))
}

func celtInnerProdNorm(x, y []celtNorm, start, end int) opusVal16 {
	if end > len(x) {
		end = len(x)
	}
	if end > len(y) {
		end = len(y)
	}
	if start < 0 {
		start = 0
	}
	if start >= end {
		return 0
	}
	// libopus float builds alias celt_inner_prod_norm_shift() to
	// celt_inner_prod(); keep alloc_trim_analysis() on the same per-arch order.
	return opusVal16(celtInnerProdLibopusOrder(x[start:end], y[start:end]))
}

// ComputeEquivRate computes equiv_rate, the equivalent 20 ms bitrate the
// intensity hysteresis, allocation trim and signal-bandwidth floor read.
// This matches libopus celt_encoder.c:1925-1927.
//
// Parameters:
//   - nbCompressedBytes: payload budget of the frame in bytes
//   - channels: number of coded channels (1 or 2)
//   - lm: log mode (frame size index: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
//   - targetBitrate: st->bitrate in bps; BitrateMax (or any non-positive value)
//     codes a fixed packet size
//
// Returns: equivalent bitrate in bits per second
//
// Reference: libopus celt/celt_encoder.c line 1925:
//
//	equiv_rate = ((opus_int32)nbCompressedBytes*8*50 << (3-LM)) - (40*C+20)*((400>>LM) - 50);
//	if (st->bitrate != OPUS_BITRATE_MAX)
//	   equiv_rate = IMIN(equiv_rate, st->bitrate - (40*C+20)*((400>>LM) - 50));
func ComputeEquivRate(nbCompressedBytes, channels, lm, targetBitrate int) int {
	// Base computation from packet size
	// 50 is the frame rate for 20ms frames at 48kHz
	// (3-LM) scales for shorter frames
	equivRate := (nbCompressedBytes * 8 * 50) << (3 - lm)

	// Subtract overhead
	// (40*C+20) is the overhead per frame in bits (approx header size)
	// ((400>>LM) - 50) is the frame rate difference from base 50fps
	overhead := (40*channels + 20) * ((400 >> lm) - 50)
	equivRate -= overhead

	// If we have a target bitrate, take the minimum
	if targetBitrate > 0 {
		bitrateEquiv := targetBitrate - (40*channels+20)*((400>>lm)-50)
		if bitrateEquiv < equivRate {
			equivRate = bitrateEquiv
		}
	}

	return equivRate
}
