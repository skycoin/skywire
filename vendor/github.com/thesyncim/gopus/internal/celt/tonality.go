// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides tonality analysis for VBR bit allocation decisions.

package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// tonalityAnalysisResult holds the results of tonality analysis.
// Tonality measures how "tonal" (pitched/harmonic) vs "noisy" (aperiodic) a signal is.
// This information is used by the VBR algorithm to allocate more bits to tonal signals
// which benefit more from accurate spectral representation.
type tonalityAnalysisResult struct {
	Tonality     float32   // Overall tonality (0=noise, 1=pure tone)
	SFM          float32   // Spectral Flatness Measure (0=tonal, 1=flat/noise)
	BandTonality []float32 // Per-band tonality estimates
	SpectralFlux float32   // Frame-to-frame spectral change (0=stationary, higher=transient)
}

// computeTonalityWithBands analyzes MDCT coefficients with explicit band count.
// This is the more precise version that takes explicit nbBands and frameSize.
//
// Parameters:
//   - mdctCoeffs: MDCT coefficients for one channel
//   - nbBands: number of frequency bands to analyze
//   - frameSize: frame size in samples (used to scale band boundaries)
//
// Returns: tonalityAnalysisResult with overall and per-band tonality
func computeTonalityWithBands(mdctCoeffs []float32, nbBands, frameSize int) tonalityAnalysisResult {
	result := tonalityAnalysisResult{
		Tonality:     0.5, // Default to mid-range if computation fails
		SFM:          0.5,
		BandTonality: make([]float32, nbBands),
		SpectralFlux: 0.0,
	}

	if len(mdctCoeffs) == 0 || nbBands <= 0 || frameSize <= 0 {
		return result
	}

	// Compute power spectrum
	powers := make([]float32, len(mdctCoeffs))
	for i, coeff := range mdctCoeffs {
		powers[i] = coeff * coeff
	}

	// Compute overall SFM
	result.SFM = computeSpectralFlatness(powers)
	result.Tonality = 1.0 - result.SFM

	// Clamp to valid range
	if result.Tonality < 0 {
		result.Tonality = 0
	}
	if result.Tonality > 1 {
		result.Tonality = 1
	}

	// Compute per-band tonality
	result.BandTonality = computePerBandTonality(mdctCoeffs, nbBands, frameSize)

	return result
}

// computePerBandTonality computes tonality for each CELT band.
func computePerBandTonality(mdctCoeffs []float32, nbBands, frameSize int) []float32 {
	bandTonality := make([]float32, nbBands)

	scale := max(
		// 1 for 2.5ms, 2 for 5ms, 4 for 10ms, 8 for 20ms
		frameSize/Overlap, 1)

	for band := range nbBands {
		if band+1 >= len(EBands) {
			break
		}

		startBin := EBands[band] * scale
		endBin := EBands[band+1] * scale

		if startBin >= len(mdctCoeffs) {
			bandTonality[band] = 0.5
			continue
		}
		if endBin > len(mdctCoeffs) {
			endBin = len(mdctCoeffs)
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			bandTonality[band] = 0.5
			continue
		}

		// Compute power spectrum for this band
		powers := make([]float32, bandWidth)
		for i := range bandWidth {
			idx := startBin + i
			if idx < len(mdctCoeffs) {
				powers[i] = mdctCoeffs[idx] * mdctCoeffs[idx]
			}
		}

		// Compute SFM for this band
		sfm := computeSpectralFlatness(powers)

		// Convert SFM to tonality
		bt := 1.0 - sfm
		if bt < 0 {
			bt = 0
		}
		if bt > 1 {
			bt = 1
		}
		bandTonality[band] = bt
	}

	return bandTonality
}

// tonalityScratch holds pre-allocated buffers for tonality analysis.
// This eliminates allocations in the hot path.
type tonalityScratch struct {
	Powers       []float32 // Power spectrum buffer (size: frameSize)
	BandTonality []float32 // Per-band tonality output (size: nbBands)
}

// ensureTonalityScratch ensures the scratch buffers are large enough.
func (s *tonalityScratch) ensureTonalityScratch(frameSize, nbBands int) {
	if cap(s.Powers) < frameSize {
		s.Powers = make([]float32, frameSize)
	} else {
		s.Powers = s.Powers[:frameSize]
	}
	if cap(s.BandTonality) < nbBands {
		s.BandTonality = make([]float32, nbBands)
	} else {
		s.BandTonality = s.BandTonality[:nbBands]
	}
}

// computeTonalityWithBandsScratch analyzes MDCT coefficients with explicit band count using pre-allocated scratch buffers.
// This is the zero-allocation version.
func computeTonalityWithBandsScratch[S ~float32](mdctCoeffs []S, nbBands, frameSize int, scratch *tonalityScratch) tonalityAnalysisResult {
	result := tonalityAnalysisResult{
		Tonality:     0.5,
		SFM:          0.5,
		BandTonality: nil,
		SpectralFlux: 0.0,
	}

	if len(mdctCoeffs) == 0 || nbBands <= 0 || frameSize <= 0 || scratch == nil {
		return result
	}

	// Ensure scratch buffers are sized
	scratch.ensureTonalityScratch(len(mdctCoeffs), nbBands)

	// Compute power spectrum into scratch buffer
	powers := scratch.Powers[:len(mdctCoeffs)]
	for i, coeff := range mdctCoeffs {
		coeff32 := float32(coeff)
		powers[i] = coeff32 * coeff32
	}

	// Compute overall SFM
	result.SFM = computeSpectralFlatness(powers)
	result.Tonality = 1.0 - result.SFM

	// Clamp to valid range
	if result.Tonality < 0 {
		result.Tonality = 0
	}
	if result.Tonality > 1 {
		result.Tonality = 1
	}

	// Reuse the precomputed power spectrum for per-band SFM.
	computePerBandTonalityScratch(powers, nbBands, frameSize, scratch)
	result.BandTonality = scratch.BandTonality[:nbBands]

	return result
}

// computePerBandTonalityScratch computes tonality for each CELT band using the
// already-computed power spectrum and pre-allocated scratch buffers.
func computePerBandTonalityScratch(powers []float32, nbBands, frameSize int, scratch *tonalityScratch) {
	bandTonality := scratch.BandTonality[:nbBands]

	scale := max(frameSize/Overlap, 1)

	for band := range nbBands {
		if band+1 >= len(EBands) {
			break
		}

		startBin := EBands[band] * scale
		endBin := EBands[band+1] * scale

		if startBin >= len(powers) {
			bandTonality[band] = 0.5
			continue
		}
		if endBin > len(powers) {
			endBin = len(powers)
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			bandTonality[band] = 0.5
			continue
		}

		// Compute SFM directly from the corresponding power slice.
		sfm := computeSpectralFlatness(powers[startBin:endBin])

		// Convert SFM to tonality
		bt := 1.0 - sfm
		if bt < 0 {
			bt = 0
		}
		if bt > 1 {
			bt = 1
		}
		bandTonality[band] = bt
	}
}

// computeSpectralFlux computes the frame-to-frame spectral change.
// This measures how much the spectrum has changed between consecutive frames.
// Low flux indicates a stationary tone, high flux indicates transients or noise.
//
// Parameters:
//   - currentEnergies: current frame band energies (log-domain)
//   - previousEnergies: previous frame band energies (log-domain)
//   - nbBands: number of bands to compare
//
// Returns: normalized spectral flux in range [0, 1]
//
// Reference: libopus uses similar metrics for transient detection
func computeSpectralFlux(currentEnergies, previousEnergies []float32, nbBands int) float32 {
	if len(currentEnergies) == 0 || len(previousEnergies) == 0 || nbBands <= 0 {
		return 0.0
	}

	var flux float32
	var count int

	// Sum of squared differences in log-energy
	for i := range nbBands {
		if i >= len(currentEnergies) || i >= len(previousEnergies) {
			break
		}

		// Use log energies for perceptual relevance
		// Add small epsilon to avoid log(0)
		const epsilon float32 = 1e-10
		currentLog := safeLog(currentEnergies[i] + epsilon)
		prevLog := safeLog(previousEnergies[i] + epsilon)

		diff := currentLog - prevLog
		flux += diff * diff
		count++
	}

	if count == 0 {
		return 0.0
	}

	// Normalize by number of bands
	flux = flux / float32(count)

	// Apply soft saturation to map to [0, 1] range
	const fluxScale float32 = 4.0
	normalizedFlux := 1.0 - opusmath.ExpF32(-flux/fluxScale)

	return normalizedFlux
}

func computeSpectralFluxGLog(currentEnergies []celtGLog, previousEnergies []celtGLog, nbBands int) float32 {
	if len(currentEnergies) == 0 || len(previousEnergies) == 0 || nbBands <= 0 {
		return 0.0
	}

	var flux float32
	var count int
	for i := range nbBands {
		if i >= len(currentEnergies) || i >= len(previousEnergies) {
			break
		}
		const epsilon float32 = 1e-10
		currentLog := safeLog(float32(currentEnergies[i]) + epsilon)
		prevLog := safeLog(float32(previousEnergies[i]) + epsilon)
		diff := currentLog - prevLog
		flux += diff * diff
		count++
	}
	if count == 0 {
		return 0.0
	}

	flux = flux / float32(count)
	const fluxScale float32 = 4.0
	return 1.0 - opusmath.ExpF32(-flux/fluxScale)
}

// computeSpectralFlatness computes the Spectral Flatness Measure (SFM).
// SFM = geometric_mean(|X|^2) / arithmetic_mean(|X|^2)
// For computational stability, this is computed as:
// SFM = exp2(mean(log2(|X|^2))) / mean(|X|^2)
//
// Returns value in [0, 1] where 1 = perfectly flat (noise), 0 = perfectly peaked (tone)
func computeSpectralFlatness(powers []float32) float32 {
	n := len(powers)
	if n == 0 {
		return 1.0
	}

	const (
		epsilon                 float32 = 1e-20
		invalidSpectralFlatness float32 = 0.5
		maxAnalysisBandEnergy   float32 = 1e9
	)

	// Single-pass: accumulate both fast log2 sum and arithmetic sum.
	var sumLog2, sum float32
	for _, v := range powers {
		// libopus src/analysis.c:tonality_analysis rejects NaN or >=1e9
		// band energies before using them in later tonality math.
		if math.Float32bits(v)&0x7fffffff > 0x7f800000 {
			return invalidSpectralFlatness
		}
		if v < epsilon {
			v = epsilon
		}
		sumLog2 += fastLog2(v)
		sum += v
		if !(sum < maxAnalysisBandEnergy) {
			return invalidSpectralFlatness
		}
	}

	arithMean := sum / float32(n)
	if arithMean <= 0 || math.Float32bits(arithMean)&0x7fffffff > 0x7f800000 {
		return 1.0
	}

	geoMean := opusmath.Exp2F32(sumLog2 / float32(n))
	sfm := geoMean / arithMean
	if math.Float32bits(sfm)&0x7fffffff > 0x7f800000 {
		return invalidSpectralFlatness
	}

	if sfm < 0 {
		sfm = 0
	}
	if sfm > 1 {
		sfm = 1
	}
	return sfm
}

// fastLog2 computes log2(x) using IEEE 754 bit extraction with a polynomial
// correction for the mantissa. ~5 digits of precision, sufficient for
// spectral flatness and tonality analysis.
func fastLog2(x float32) float32 {
	bits := math.Float32bits(x)
	// Extract exponent: integer part of log2
	exp := int32((bits>>23)&0xFF) - 127
	// Normalize mantissa to [1, 2)
	bits = (bits & 0x007fffff) | 0x3f800000
	m := math.Float32frombits(bits) - 1.0
	// Minimax polynomial for log2(1+m), m in [0, 1)
	// Max error ~3e-5 over [0,1)
	return float32(exp) + m*(1.4426950408889634+m*(-0.7213475204444817+m*(0.4808983469629878+m*(-0.3606737602222408))))
}

// geometricMean computes the geometric mean of positive values.
// Uses exp2(mean(fastLog2(x))) for numerical stability.
func geometricMean(values []float32) float32 {
	if len(values) == 0 {
		return 0
	}

	const epsilon float32 = 1e-20

	var sumLog2 float32
	for _, v := range values {
		if v < epsilon {
			v = epsilon
		}
		sumLog2 += fastLog2(v)
	}

	meanLog2 := sumLog2 / float32(len(values))
	return opusmath.Exp2F32(meanLog2)
}

// arithmeticMean computes the arithmetic mean of values.
func arithmeticMean(values []float32) float32 {
	if len(values) == 0 {
		return 0
	}

	var sum float32
	for _, v := range values {
		sum += v
	}

	return sum / float32(len(values))
}

// safeLog computes natural logarithm with protection against negative/zero values.
func safeLog(x float32) float32 {
	const epsilon float32 = 1e-20
	if x < epsilon {
		x = epsilon
	}
	return opusmath.LogF32(x)
}

// computeTonalityFromNormalized computes tonality from pre-normalized MDCT coefficients.
// This is useful when normalization has already been done (as in encode_frame.go).
//
// Parameters:
//   - normCoeffs: normalized MDCT coefficients (unit energy per band)
//   - nbBands: number of frequency bands
//   - frameSize: frame size for scaling band boundaries
//
// Returns: tonalityAnalysisResult
func computeTonalityFromNormalized(normCoeffs []celtNorm, nbBands, frameSize int) tonalityAnalysisResult {
	return computeTonalityWithBandsScratch(normCoeffs, nbBands, frameSize, &tonalityScratch{})
}
