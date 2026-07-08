package silk

// LTPCoeffsArray is a fixed-size type for LTP coefficients to avoid allocations.
// Maximum 4 subframes, 5 taps each.
type LTPCoeffsArray [4][5]int8

// analyzeLTP computes LTP coefficients for each subframe.
// LTP predicts current samples from pitch-delayed past samples.
//
// Per draft-vos-silk-01 Section 2.1.2.6.
// Returns 5-tap LTP coefficients per subframe in Q7 format.
// Uses fixed-size array to avoid allocations.
func (e *Encoder) analyzeLTP(pcm []float32, pitchLags []int32, numSubframes int, periodicity int) LTPCoeffsArray {
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples

	var ltpCoeffs LTPCoeffsArray

	for sf := 0; sf < numSubframes && sf < 4; sf++ {
		start := sf * subframeSamples
		lag := int(pitchLags[sf])

		// Compute optimal LTP coefficients via least squares
		coeffs := computeLTPCoeffs(pcm, start, subframeSamples, lag)

		// Quantize to codebook into fixed array using periodicity-selected codebook
		quantizeLTPCoeffsInto(coeffs[:], periodicity, &ltpCoeffs[sf])
	}

	return ltpCoeffs
}

// computeLTPCoeffs computes 5-tap LTP coefficients for a subframe.
// Uses least-squares minimization of prediction error.
// Returns a fixed-size [5]float32 array to avoid heap allocation.
func computeLTPCoeffs(pcm []float32, start, length, lag int) [5]float32 {
	const numTaps = 5
	const halfTaps = 2

	// Compute autocorrelation matrix and cross-correlation vector
	// R[i][j] = sum(x[n-lag+i-2] * x[n-lag+j-2])
	// r[i] = sum(x[n] * x[n-lag+i-2])

	var R [numTaps][numTaps]float32
	var r [numTaps]float32

	for n := start; n < start+length; n++ {
		if n >= len(pcm) || n < lag+halfTaps {
			continue
		}

		x := pcm[n]

		for i := range numTaps {
			pastIdx := n - lag + i - halfTaps
			if pastIdx < 0 || pastIdx >= len(pcm) {
				continue
			}
			pastI := pcm[pastIdx]
			r[i] += x * pastI

			for j := range numTaps {
				pastJIdx := n - lag + j - halfTaps
				if pastJIdx < 0 || pastJIdx >= len(pcm) {
					continue
				}
				pastJ := pcm[pastJIdx]
				R[i][j] += pastI * pastJ
			}
		}
	}

	// Regularization for stability
	for i := range numTaps {
		R[i][i] += 1e-6
	}

	// Solve R * coeffs = r using Gaussian elimination
	coeffs := solveLTPSystem(R, r)

	return coeffs
}

// solveLTPSystem solves the 5x5 normal equations using Gaussian elimination.
func solveLTPSystem(R [5][5]float32, r [5]float32) [5]float32 {
	const n = 5

	// Augmented matrix
	var A [n][n + 1]float32
	for i := range n {
		for j := range n {
			A[i][j] = R[i][j]
		}
		A[i][n] = r[i]
	}

	// Forward elimination with partial pivoting
	for i := range n {
		// Find pivot
		maxRow := i
		for k := i + 1; k < n; k++ {
			if abs32(A[k][i]) > abs32(A[maxRow][i]) {
				maxRow = k
			}
		}
		A[i], A[maxRow] = A[maxRow], A[i]

		if abs32(A[i][i]) < 1e-10 {
			continue // Skip singular
		}

		// Eliminate column
		for k := i + 1; k < n; k++ {
			factor := A[k][i] / A[i][i]
			for j := i; j <= n; j++ {
				A[k][j] -= factor * A[i][j]
			}
		}
	}

	// Back substitution
	var coeffs [5]float32
	for i := n - 1; i >= 0; i-- {
		sum := A[i][n]
		for j := i + 1; j < n; j++ {
			sum -= A[i][j] * coeffs[j]
		}
		if abs32(A[i][i]) > 1e-10 {
			coeffs[i] = sum / A[i][i]
		}
	}

	return coeffs
}

// quantizeLTPCoeffs quantizes LTP coefficients to nearest codebook entry.
// Uses LTP codebook from codebook.go (LTPFilterLow/Mid/High).
// Returns Q7 format coefficients.
// Allocating version for backward compatibility.
func quantizeLTPCoeffs(coeffs []float32, periodicity int) []int8 {
	result := make([]int8, 5)
	var fixed [5]int8
	quantizeLTPCoeffsInto(coeffs, periodicity, &fixed)
	copy(result, fixed[:])
	return result
}

// quantizeLTPCoeffsInto quantizes LTP coefficients into a pre-allocated array.
// Zero-allocation version.
func quantizeLTPCoeffsInto(coeffs []float32, periodicity int, result *[5]int8) {
	const numTaps = 5

	// Clamp periodicity: 0 = low, 1 = mid, 2 = high
	if periodicity < 0 || periodicity > 2 {
		periodicity = 1
	}

	// Find best matching codebook entry
	bestIdx := 0
	bestDist := float32(3.4028234663852886e+38)

	switch periodicity {
	case 0:
		for idx := range len(LTPFilterLow) {
			var dist float32
			for tap := range numTaps {
				cbVal := float32(LTPFilterLow[idx][tap]) / 128.0
				diff := coeffs[tap] - cbVal
				dist += diff * diff
			}
			if dist < bestDist {
				bestDist = dist
				bestIdx = idx
			}
		}
		*result = LTPFilterLow[bestIdx]

	case 1:
		for idx := range len(LTPFilterMid) {
			var dist float32
			for tap := range numTaps {
				cbVal := float32(LTPFilterMid[idx][tap]) / 128.0
				diff := coeffs[tap] - cbVal
				dist += diff * diff
			}
			if dist < bestDist {
				bestDist = dist
				bestIdx = idx
			}
		}
		*result = LTPFilterMid[bestIdx]

	case 2:
		for idx := range len(LTPFilterHigh) {
			var dist float32
			for tap := range numTaps {
				cbVal := float32(LTPFilterHigh[idx][tap]) / 128.0
				diff := coeffs[tap] - cbVal
				dist += diff * diff
			}
			if dist < bestDist {
				bestDist = dist
				bestIdx = idx
			}
		}
		*result = LTPFilterHigh[bestIdx]
	}
}

// encodeLTPCoeffs encodes LTP indices to the bitstream.
// Per RFC 6716 Section 4.2.7.6.3 and libopus silk/encode_indices.c.
//
// Libopus encoding scheme:
// 1. Encode PER index (0-2) using silk_LTP_per_index_iCDF
// 2. For each subframe, encode LTP codebook index using silk_LTP_gain_iCDF_ptrs[PERIndex]
//
// PER index determines the codebook:
//   - 0: silk_LTP_gain_iCDF_0 (8 entries)
//   - 1: silk_LTP_gain_iCDF_1 (16 entries)
//   - 2: silk_LTP_gain_iCDF_2 (32 entries)
func (e *Encoder) encodeLTPCoeffs(perIndex int, ltpIndices []int8, numSubframes int) {
	if perIndex < 0 {
		perIndex = 0
	}
	if perIndex > 2 {
		perIndex = 2
	}

	// Step 1: Encode PER index using libopus silk_LTP_per_index_iCDF
	e.rangeEncoder.EncodeICDF(perIndex, silk_LTP_per_index_iCDF, 8)

	// Step 2: Encode LTP codebook index for each subframe
	gainICDF := silk_LTP_gain_iCDF_ptrs[perIndex]

	for sf := 0; sf < numSubframes && sf < len(ltpIndices); sf++ {
		cbIdx := max(int(ltpIndices[sf]), 0)
		maxIdx := len(gainICDF) - 1
		if cbIdx > maxIdx {
			cbIdx = maxIdx
		}
		e.rangeEncoder.EncodeICDF(cbIdx, gainICDF, 8)
	}
}

// findLTPCodebookIndex finds the codebook index for given coefficients.
func findLTPCodebookIndex(coeffs [5]int8, periodicity int) int {
	const numTaps = 5

	switch periodicity {
	case 0:
		for idx := range len(LTPFilterLow) {
			match := true
			for tap := range numTaps {
				if coeffs[tap] != LTPFilterLow[idx][tap] {
					match = false
					break
				}
			}
			if match {
				return idx
			}
		}
	case 1:
		for idx := range len(LTPFilterMid) {
			match := true
			for tap := range numTaps {
				if coeffs[tap] != LTPFilterMid[idx][tap] {
					match = false
					break
				}
			}
			if match {
				return idx
			}
		}
	case 2:
		for idx := range len(LTPFilterHigh) {
			match := true
			for tap := range numTaps {
				if coeffs[tap] != LTPFilterHigh[idx][tap] {
					match = false
					break
				}
			}
			if match {
				return idx
			}
		}
	}

	return 0 // Default to first entry if no match
}

// determinePeriodicity determines LTP periodicity level based on signal characteristics.
// Returns 0 (low), 1 (mid), or 2 (high) periodicity.
func (e *Encoder) determinePeriodicity(pcm []float32, pitchLags []int32) int {
	// Compute average normalized autocorrelation at pitch lag
	var totalCorr float32
	var count int

	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples

	for sf, lag32 := range pitchLags {
		lag := int(lag32)
		start := sf * subframeSamples
		end := min(start+subframeSamples, len(pcm))

		var corr, energy1, energy2 float32
		for i := start; i < end && i >= lag; i++ {
			s := pcm[i]
			past := pcm[i-lag]
			corr += s * past
			energy1 += s * s
			energy2 += past * past
		}

		if energy1 > 1e-10 && energy2 > 1e-10 {
			totalCorr += corr / sqrt32(energy1*energy2)
			count++
		}
	}

	if count == 0 {
		return 1 // Default to mid
	}

	avgCorr := totalCorr / float32(count)

	// Classify periodicity based on correlation strength
	if avgCorr < 0.5 {
		return 0 // Low periodicity
	} else if avgCorr < 0.8 {
		return 1 // Mid periodicity
	}
	return 2 // High periodicity
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
