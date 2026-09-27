// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides allocation trim analysis for optimal bit allocation.

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

// AllocTrimAnalysis computes the optimal allocation trim value for a CELT frame.
// The trim value biases bit allocation between lower and higher frequency bands.
// A higher trim value allocates more bits to lower frequencies.
//
// The algorithm considers:
// - Equivalent bitrate (lower bitrates favor lower trim)
// - Spectral tilt (energy distribution across bands)
// - TF estimate (transient characteristic)
// - Stereo correlation (for stereo signals)
// - Tonality slope (optional, from analysis)
//
// Parameters:
//   - normCoeffs: normalized MDCT coefficients (left channel for stereo, or mono)
//   - bandLogE: band log-energies [nbBands * channels]
//   - nbBands: number of frequency bands
//   - lm: log mode (frame size index)
//   - channels: 1 for mono, 2 for stereo
//   - normCoeffsRight: normalized right channel coefficients (nil for mono)
//   - intensity: intensity stereo band threshold (nbBands for no intensity stereo)
//   - tfEstimate: TF estimate from transient analysis (0.0-1.0)
//   - equivRate: equivalent bitrate in bits per second
//   - surroundTrim: surround mix trim adjustment (0 for non-surround)
//   - tonalitySlope: tonality slope from analysis (-1 to 1, 0 if not available)
//
// Returns: trim index in range [0, 10], where 5 is the neutral default
//
// Reference: libopus celt/celt_encoder.c alloc_trim_analysis()
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
) (int, allocTrimDetail) {
	detail := allocTrimDetail{}

	// Start with default trim of 5
	trim := opusVal16(5.0)
	detail.base = trim

	// At low bitrate, reducing the trim seems to help. At higher bitrates, it's less
	// clear what's best, so we're keeping it as it was before, at least for now.
	// Reference: libopus lines 877-883
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
		logXC := computeStereoCorrelationTrim(normCoeffs, normCoeffsRight, nbBands, lm, intensity)

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
	// Positive diff = more energy in lower bands (tilted down)
	// Negative diff = more energy in higher bands (tilted up)
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
func computeStereoCorrelationTrim(normL, normR []celtNorm, nbBands, lm, intensity int) opusVal16 {
	logXC, _ := computeStereoCorrelationLogs(normL, normR, nbBands, lm, intensity)
	return logXC
}

func computeStereoCorrelationLogs(normL, normR []celtNorm, nbBands, lm, intensity int) (opusVal16, opusVal16) {
	// Compute inter-channel correlation for low frequencies (first 8 bands)
	// libopus uses inner product of normalized coefficients between channels

	var sum opusVal16

	// Compute correlation for first 8 bands
	for band := 0; band < 8 && band < nbBands; band++ {
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
// compute_vbr(). The state is updated once per frame after alloc-trim analysis.
func UpdateStereoSaving(prev opusVal16, normL, normR []celtNorm, nbBands, lm, intensity int) opusVal16 {
	if len(normL) == 0 || len(normR) == 0 || nbBands <= 0 {
		return prev
	}
	if intensity < 0 {
		intensity = 0
	}
	if intensity > nbBands {
		intensity = nbBands
	}

	_, logXC2 := computeStereoCorrelationLogs(normL, normR, nbBands, lm, intensity)
	limit := opusVal16(-0.5) * logXC2
	next := prev + opusVal16(0.25)
	if next > limit {
		next = limit
	}
	if next < opusVal16(0) {
		next = 0
	}
	if next > opusVal16(1) {
		next = 1
	}
	return next
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

// ComputeEquivRate computes the equivalent bitrate for allocation trim analysis.
// This matches libopus computation in celt_encoder.c line 1925.
//
// Parameters:
//   - nbCompressedBytes: target compressed packet size in bytes
//   - channels: number of audio channels (1 or 2)
//   - lm: log mode (frame size index: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
//   - targetBitrate: target bitrate in bps (0 if using fixed packet size)
//
// Returns: equivalent bitrate in bits per second
//
// Reference: libopus celt/celt_encoder.c line 1925:
//
//	equiv_rate = ((opus_int32)nbCompressedBytes*8*50 << (3-LM)) - (40*C+20)*((400>>LM) - 50);
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
