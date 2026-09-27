package celt

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/util"
)

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// ComputeImportance computes per-band importance weights for TF analysis.
// Importance weights affect how much each band's TF decision matters in the Viterbi search.
//
// The importance is derived from:
// - Band log energies relative to a spectral follower curve
// - Noise floor based on band width and quantization depth
// - Masking effects from neighboring bands
//
// Higher importance means the band's TF decision has more perceptual impact.
// Default value is 13 (when no analysis is performed).
//
// Parameters:
//   - bandLogE: log-domain band energies (mean-relative, from ComputeBandEnergies)
//   - oldBandE: previous frame band energies (for temporal smoothing)
//   - nbBands: number of bands
//   - channels: number of audio channels
//   - lm: log mode (frame size index)
//   - lsbDepth: bit depth of input signal (typically 16 or 24)
//   - effectiveBytes: available bytes for encoding
//
// Returns: per-band importance weights (13 = neutral, higher = more important)
//
// Reference: libopus celt/celt_encoder.c dynalloc_analysis() importance calculation
func ComputeImportance(bandLogE, oldBandE []celtGLog, nbBands, channels, lm, lsbDepth, effectiveBytes int) []int32 {
	importance := make([]int32, nbBands)

	// Default importance when analysis is disabled (low bitrate or complexity)
	// libopus: if (effectiveBytes < (30 + 5*LM)) importance[i] = 13
	if effectiveBytes < 30+5*lm {
		for i := range nbBands {
			importance[i] = 13
		}
		return importance
	}

	// Compute noise floor per band
	// libopus: noise_floor[i] = 0.0625*logN[i] + 0.5 + (9-lsb_depth) - eMeans[i]/16 + 0.0062*(i+5)^2
	noiseFloor := make([]float32, nbBands)
	for i := range nbBands {
		logNVal := float32(0)
		if i < len(LogN) {
			logNVal = float32(LogN[i]) / 256.0 // LogN is in Q8
		}
		eMean := float32(0)
		if i < len(eMeans) {
			eMean = float32(eMeans[i])
		}
		// Noise floor formula from libopus (converted from fixed-point)
		noiseFloor[i] = 0.0625*logNVal + 0.5 + float32(9-lsbDepth) - eMean/16.0 + 0.0062*float32((i+5)*(i+5))
	}

	// Compute max depth across all bands and channels
	maxDepth := float32(-31.9)
	end := nbBands
	for c := range channels {
		for i := range end {
			idx := c*nbBands + i
			if idx < len(bandLogE) {
				depth := float32(bandLogE[idx]) - noiseFloor[i]
				if depth > maxDepth {
					maxDepth = depth
				}
			}
		}
	}

	// Compute follower curve (spectral envelope tracker)
	// This implements a simple masking model
	follower := make([]float32, nbBands)

	// For each channel, compute follower and combine
	for c := range channels {
		bandLogE3 := make([]float32, nbBands)
		f := make([]float32, nbBands)

		// Get band energies for this channel
		for i := range nbBands {
			idx := c*nbBands + i
			if idx < len(bandLogE) {
				bandLogE3[i] = float32(bandLogE[idx])
			}
			// For LM=0, use max of current and previous frame energies
			// (single-bin bands have high variance)
			if lm == 0 && i < 8 {
				oldIdx := c*MaxBands + i
				if oldIdx < len(oldBandE) && float32(oldBandE[oldIdx]) > bandLogE3[i] {
					bandLogE3[i] = float32(oldBandE[oldIdx])
				}
			}
		}

		// Forward pass: follower tracks rising edges with limited slope
		f[0] = bandLogE3[0]
		last := 0
		for i := 1; i < nbBands; i++ {
			// Track last band significantly higher than previous
			if bandLogE3[i] > bandLogE3[i-1]+0.5 {
				last = i
			}
			// Follower rises at most 1.5 dB per band
			if f[i-1]+1.5 < bandLogE3[i] {
				f[i] = f[i-1] + 1.5
			} else {
				f[i] = bandLogE3[i]
			}
		}

		// Backward pass: follower tracks falling edges
		for i := last - 1; i >= 0; i-- {
			// Follower falls at most 2 dB per band
			if f[i+1]+2.0 < f[i] {
				f[i] = f[i+1] + 2.0
			}
			if bandLogE3[i] < f[i] {
				f[i] = bandLogE3[i]
			}
		}

		// Clamp follower to noise floor
		for i := range nbBands {
			if f[i] < noiseFloor[i] {
				f[i] = noiseFloor[i]
			}
		}

		// Compute importance contribution from this channel
		// follower = max(0, bandLogE - follower)
		if channels == 2 {
			// Stereo: combine with cross-talk consideration (24 dB)
			otherC := 1 - c
			for i := range nbBands {
				otherIdx := otherC*nbBands + i
				if otherIdx < len(bandLogE) {
					// Consider 24 dB cross-talk between channels
					otherFollower := follower[i] - 4.0 // 4.0 ~ 24 dB
					if otherFollower > f[i] {
						f[i] = otherFollower
					}
				}
			}
		}

		// Store or combine follower values
		if c == 0 {
			copy(follower, f)
		} else {
			// For stereo, combine the excess energy from both channels
			for i := range nbBands {
				idx0 := i
				idx1 := nbBands + i
				excess0 := float32(0)
				excess1 := float32(0)
				if idx0 < len(bandLogE) {
					excess0 = float32(bandLogE[idx0]) - follower[i]
					if excess0 < 0 {
						excess0 = 0
					}
				}
				if idx1 < len(bandLogE) {
					excess1 = float32(bandLogE[idx1]) - f[i]
					if excess1 < 0 {
						excess1 = 0
					}
				}
				follower[i] = (excess0 + excess1) / 2.0
			}
		}
	}

	// If mono, compute excess energy
	if channels == 1 {
		for i := range nbBands {
			if i < len(bandLogE) {
				excess := float32(bandLogE[i]) - follower[i]
				if excess < 0 {
					excess = 0
				}
				follower[i] = excess
			} else {
				follower[i] = 0
			}
		}
	}

	// Convert follower to importance
	// libopus: importance[i] = floor(0.5 + 13 * celt_exp2_db(min(follower[i], 4.0)))
	for i := range nbBands {
		importance[i] = dynallocImportanceFromFollower(follower[i])
	}

	return importance
}

func l1MetricNorm(tmp []celtNorm, N int, LM int, bias float32) float32 {
	n := min(N, len(tmp))
	var L1 float32
	if celtAbsSumUsesNeon {
		L1 = l1AbsSumNeon(tmp, n)
	} else {
		// Sixteen independent accumulators fill the FADD throughput slots:
		// 16 ops / 4 dispatch = 4 cycles per 16-element block, exactly matching
		// the 4-cycle FADD latency so no stall on any chain between iterations.
		// Each conditional negate emits a FABS-style instruction without branching.
		buf := tmp[:n]
		var a0, a1, a2, a3, a4, a5, a6, a7 float32
		var a8, a9, a10, a11, a12, a13, a14, a15 float32
		for len(buf) >= 16 {
			v0, v1, v2, v3 := buf[0], buf[1], buf[2], buf[3]
			v4, v5, v6, v7 := buf[4], buf[5], buf[6], buf[7]
			v8, v9, v10, v11 := buf[8], buf[9], buf[10], buf[11]
			v12, v13, v14, v15 := buf[12], buf[13], buf[14], buf[15]
			if v0 < 0 {
				v0 = -v0
			}
			if v1 < 0 {
				v1 = -v1
			}
			if v2 < 0 {
				v2 = -v2
			}
			if v3 < 0 {
				v3 = -v3
			}
			if v4 < 0 {
				v4 = -v4
			}
			if v5 < 0 {
				v5 = -v5
			}
			if v6 < 0 {
				v6 = -v6
			}
			if v7 < 0 {
				v7 = -v7
			}
			if v8 < 0 {
				v8 = -v8
			}
			if v9 < 0 {
				v9 = -v9
			}
			if v10 < 0 {
				v10 = -v10
			}
			if v11 < 0 {
				v11 = -v11
			}
			if v12 < 0 {
				v12 = -v12
			}
			if v13 < 0 {
				v13 = -v13
			}
			if v14 < 0 {
				v14 = -v14
			}
			if v15 < 0 {
				v15 = -v15
			}
			a0 += v0
			a1 += v1
			a2 += v2
			a3 += v3
			a4 += v4
			a5 += v5
			a6 += v6
			a7 += v7
			a8 += v8
			a9 += v9
			a10 += v10
			a11 += v11
			a12 += v12
			a13 += v13
			a14 += v14
			a15 += v15
			buf = buf[16:]
		}
		// 4-wide tail for remaining elements
		for len(buf) >= 4 {
			v0, v1, v2, v3 := buf[0], buf[1], buf[2], buf[3]
			if v0 < 0 {
				v0 = -v0
			}
			if v1 < 0 {
				v1 = -v1
			}
			if v2 < 0 {
				v2 = -v2
			}
			if v3 < 0 {
				v3 = -v3
			}
			a0 += v0
			a1 += v1
			a2 += v2
			a3 += v3
			buf = buf[4:]
		}
		for _, v := range buf {
			if v < 0 {
				v = -v
			}
			a0 += v
		}
		L1 = (a0 + a1 + a2 + a3) + (a4 + a5 + a6 + a7) + (a8 + a9 + a10 + a11) + (a12 + a13 + a14 + a15)
	}
	return L1 + float32(LM)*bias*L1
}

func tfAnalysisBias(tfEstimate opusVal16) opusVal16 {
	v := float32(0.5) - float32(tfEstimate)
	if v < -0.25 {
		v = -0.25
	}
	return opusVal16(0.04 * v)
}

func haar1Norm(x []celtNorm, n0, stride int) {
	n0 >>= 1
	if n0 <= 0 || stride <= 0 {
		return
	}
	const invSqrt2 = float32(0.7071067811865476)
	step := stride * 2
	switch stride {
	case 1:
		if 2*n0 <= len(x) {
			haar1Stride1NEON(x[:2*n0:2*n0], n0)
		}
		return
	case 2:
		if 4*n0 <= len(x) {
			haar1Stride2NEON(x[:4*n0:4*n0], n0)
		}
		return
	case 4:
		if 8*n0 <= len(x) {
			haar1Stride4NEON(x[:8*n0:8*n0], n0)
		}
		return
	}
	for i := range stride {
		idx0 := i
		idx1 := i + stride
		for j := 0; j < n0; j++ {
			tmp1 := noFMA32Mul(invSqrt2, float32(x[idx0]))
			tmp2 := noFMA32Mul(invSqrt2, float32(x[idx1]))
			x[idx0] = celtNorm(noFMA32Add(tmp1, tmp2))
			x[idx1] = celtNorm(noFMA32Sub(tmp1, tmp2))
			idx0 += step
			idx1 += step
		}
	}
}

// TFAnalysis performs time-frequency analysis to determine optimal TF resolution per band.
// It uses a Viterbi algorithm to find the best TF resolution settings.
//
// Parameters:
//   - X: normalized MDCT coefficients
//   - N0: total number of coefficients (per channel)
//   - nbEBands: number of bands to analyze
//   - isTransient: whether frame uses short blocks
//   - lm: log mode (frame size index)
//   - tfEstimate: estimate of temporal vs frequency content (0.0 = time, 1.0 = freq)
//   - effectiveBytes: available bytes for encoding (affects lambda)
//   - importance: per-band importance weights (nil for uniform)
//
// Returns:
//   - tfRes: per-band TF resolution flags (0 or 1)
//   - tfSelect: TF select flag for bitstream
//
// Reference: libopus celt/celt_encoder.c tf_analysis()
func TFAnalysis(X []celtNorm, N0, nbEBands int, isTransient bool, lm int, tfEstimate opusVal16, effectiveBytes int, importance []int32) (tfRes []int32, tfSelect int) {
	tfRes = make([]int32, nbEBands)

	// Note: Unlike earlier versions, we do NOT skip TF analysis for LM=0.
	// libopus runs tf_analysis for all LM values when enable_tf_analysis is true.
	// For LM=0 (2.5ms frames), the tf_select_table values still allow meaningful
	// TF changes: when per_band_flag=1, the TF value can be -1 instead of 0.

	// Compute lambda (transition cost) based on available bits
	// Higher lambda = more expensive to change TF resolution between bands
	lambda := int32(80)
	if effectiveBytes > 0 {
		lambda = int32(max(80, 20480/effectiveBytes+2))
	}

	// Compute bias: slightly prefer frequency resolution when uncertain
	// bias = 0.04 * max(-0.25, 0.5 - tfEstimate)
	// Keep TF metric arithmetic in float32 to mirror libopus float path.
	bias := float32(tfAnalysisBias(tfEstimate))

	// Compute per-band metric
	metric := make([]int32, nbEBands)
	tmp := make([]celtNorm, 0, (EBands[nbEBands]-EBands[nbEBands-1])<<lm)

	for i := range nbEBands {
		bandStart := EBands[i] << lm
		bandEnd := EBands[i+1] << lm
		N := bandEnd - bandStart

		// Check if band is too narrow to be split
		narrow := (EBands[i+1] - EBands[i]) == 1

		// Copy band coefficients to tmp
		if cap(tmp) < N {
			tmp = make([]celtNorm, N)
		} else {
			tmp = tmp[:N]
		}

		for j := 0; j < N && bandStart+j < len(X); j++ {
			tmp[j] = X[bandStart+j]
		}

		// Compute initial L1 metric
		var initLM int
		if isTransient {
			initLM = lm
		}
		L1 := l1MetricNorm(tmp, N, initLM, bias)
		bestL1 := L1
		bestLevel := 0

		// Check the -1 case for transients (more time resolution)
		if isTransient && !narrow {
			tmp1 := make([]celtNorm, N)
			copy(tmp1, tmp)
			haar1Norm(tmp1, N>>lm, 1<<lm)
			L1 = l1MetricNorm(tmp1, N, lm+1, bias)
			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = -1
			}
		}

		// Try different Haar levels
		maxK := lm
		if !isTransient && !narrow {
			maxK = lm + 1
		}
		for k := 0; k < maxK; k++ {
			var B int
			if isTransient {
				B = lm - k - 1
			} else {
				B = k + 1
			}

			haar1Norm(tmp, N>>k, 1<<k)
			L1 = l1MetricNorm(tmp, N, B, bias)

			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = k + 1
			}
		}

		// Convert to metric in Q1 format
		if isTransient {
			metric[i] = int32(2 * bestLevel)
		} else {
			metric[i] = int32(-2 * bestLevel)
		}

		if narrow && (metric[i] == 0 || metric[i] == int32(-2*lm)) {
			metric[i]--
		}
	}

	// Search for optimal tf resolution using Viterbi algorithm
	// First, determine tf_select by comparing costs for both options
	tfSelect = 0
	selcost := [2]int32{}

	for sel := range 2 {
		imp0 := int32(13)
		if importance != nil && len(importance) > 0 {
			imp0 = importance[0]
		}
		isTransientInt := boolToInt(isTransient)

		cost0 := imp0 * util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+0]))
		lambdaInit := lambda
		if isTransient {
			lambdaInit = 0
		}
		cost1 := imp0*util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+1])) + lambdaInit

		for i := 1; i < nbEBands; i++ {
			imp := int32(13)
			if importance != nil && i < len(importance) {
				imp = importance[i]
			}

			curr0 := minInt32(cost0, cost1+lambda)
			curr1 := minInt32(cost0+lambda, cost1)
			cost0 = curr0 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+0]))
			cost1 = curr1 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+1]))
		}

		selcost[sel] = minInt32(cost0, cost1)
	}

	// Only allow tf_select=1 for transients (conservative approach per libopus)
	if selcost[1] < selcost[0] && isTransient {
		tfSelect = 1
	}

	// Viterbi forward pass with selected tf_select
	isTransientInt := boolToInt(isTransient)
	path0 := make([]int32, nbEBands)
	path1 := make([]int32, nbEBands)

	imp0 := int32(13)
	if importance != nil && len(importance) > 0 {
		imp0 = importance[0]
	}

	cost0 := imp0 * util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+0]))
	lambdaInit := lambda
	if isTransient {
		lambdaInit = 0
	}
	cost1 := imp0*util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+1])) + lambdaInit

	for i := 1; i < nbEBands; i++ {
		imp := int32(13)
		if importance != nil && i < len(importance) {
			imp = importance[i]
		}

		// Path for state 0
		from0 := cost0
		from1 := cost1 + lambda
		var curr0 int32
		if from0 < from1 {
			curr0 = from0
			path0[i] = 0
		} else {
			curr0 = from1
			path0[i] = 1
		}

		// Path for state 1
		from0 = cost0 + lambda
		from1 = cost1
		var curr1 int32
		if from0 < from1 {
			curr1 = from0
			path1[i] = 0
		} else {
			curr1 = from1
			path1[i] = 1
		}

		cost0 = curr0 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+0]))
		cost1 = curr1 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+1]))
	}

	// Determine final state
	if cost0 < cost1 {
		tfRes[nbEBands-1] = 0
	} else {
		tfRes[nbEBands-1] = 1
	}

	// Viterbi backward pass
	for i := nbEBands - 2; i >= 0; i-- {
		if tfRes[i+1] == 1 {
			tfRes[i] = path1[i+1]
		} else {
			tfRes[i] = path0[i+1]
		}
	}

	return tfRes, tfSelect
}

// TFAnalysisScratch holds pre-allocated buffers for TF analysis.
//
// Metric and the Viterbi path arrays are no longer fields: they are addressed
// only by index inside TFAnalysisWithScratch and never escape, so they live on
// the stack there (see the band-count guarded block in that function). TfRes is
// returned to the caller, and Tmp/Tmp1 are handed to the haar1/l1 kernels, so
// those stay pooled here.
type TFAnalysisScratch struct {
	Tmp   []celtNorm // Band coefficients working buffer
	Tmp1  []celtNorm // Copy for transient analysis
	TfRes []int32    // Output buffer
}

// EnsureTFAnalysisScratch ensures scratch buffers are large enough.
func (s *TFAnalysisScratch) EnsureTFAnalysisScratch(nbEBands, maxBandWidth int) {
	if cap(s.Tmp) < maxBandWidth {
		s.Tmp = make([]celtNorm, maxBandWidth)
	} else {
		s.Tmp = s.Tmp[:maxBandWidth]
	}
	if cap(s.Tmp1) < maxBandWidth {
		s.Tmp1 = make([]celtNorm, maxBandWidth)
	} else {
		s.Tmp1 = s.Tmp1[:maxBandWidth]
	}
	if cap(s.TfRes) < nbEBands {
		s.TfRes = make([]int32, nbEBands)
	} else {
		s.TfRes = s.TfRes[:nbEBands]
	}
}

// TFAnalysisWithScratch is the zero-allocation version of TFAnalysis.
func TFAnalysisWithScratch(X []celtNorm, N0, nbEBands int, isTransient bool, lm int, tfEstimate opusVal16, effectiveBytes int, importance []int32, scratch *TFAnalysisScratch) (tfRes []int32, tfSelect int) {
	if scratch == nil {
		return TFAnalysis(X, N0, nbEBands, isTransient, lm, tfEstimate, effectiveBytes, importance)
	}

	// Compute max band width for scratch sizing
	maxBandWidth := 0
	for i := 0; i < nbEBands && i+1 < len(EBands); i++ {
		bw := (EBands[i+1] - EBands[i]) << lm
		if bw > maxBandWidth {
			maxBandWidth = bw
		}
	}
	scratch.EnsureTFAnalysisScratch(nbEBands, maxBandWidth)

	tfRes = scratch.TfRes[:nbEBands]
	for i := range tfRes {
		tfRes[i] = 0
	}

	// Compute lambda
	lambda := int32(80)
	if effectiveBytes > 0 {
		lambda = int32(max(80, 20480/effectiveBytes+2))
	}

	// Keep TF metric arithmetic in float32 to mirror libopus float path.
	bias := float32(tfAnalysisBias(tfEstimate))

	// metric and the Viterbi path arrays are addressed only by index here and
	// never escape, so keep them on the stack for the common (<= MaxBands) band
	// counts. Non-standard custom/QEXT layouts (rare) fall back to a heap slice.
	var metricArr, path0Arr, path1Arr [MaxBands]int32
	var metric, path0, path1 []int32
	if nbEBands <= MaxBands {
		metric = metricArr[:nbEBands]
		path0 = path0Arr[:nbEBands]
		path1 = path1Arr[:nbEBands]
	} else {
		metric = make([]int32, nbEBands)
		path0 = make([]int32, nbEBands)
		path1 = make([]int32, nbEBands)
	}
	tmp := scratch.Tmp

	for i := range nbEBands {
		bandStart := EBands[i] << lm
		bandEnd := EBands[i+1] << lm
		N := bandEnd - bandStart

		narrow := (EBands[i+1] - EBands[i]) == 1

		// Use scratch buffer
		tmpSlice := tmp[:N]
		for j := 0; j < N && bandStart+j < len(X); j++ {
			tmpSlice[j] = X[bandStart+j]
		}

		var initLM int
		if isTransient {
			initLM = lm
		}
		L1 := l1MetricNorm(tmpSlice, N, initLM, bias)
		bestL1 := L1
		bestLevel := 0

		if isTransient && !narrow {
			// Use scratch tmp1 instead of allocating
			tmp1 := scratch.Tmp1[:N]
			copy(tmp1, tmpSlice)
			haar1Norm(tmp1, N>>lm, 1<<lm)
			L1 = l1MetricNorm(tmp1, N, lm+1, bias)
			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = -1
			}
		}

		maxK := lm
		if !isTransient && !narrow {
			maxK = lm + 1
		}
		for k := 0; k < maxK; k++ {
			var B int
			if isTransient {
				B = lm - k - 1
			} else {
				B = k + 1
			}

			haar1Norm(tmpSlice, N>>k, 1<<k)
			L1 = l1MetricNorm(tmpSlice, N, B, bias)

			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = k + 1
			}
		}

		if isTransient {
			metric[i] = int32(2 * bestLevel)
		} else {
			metric[i] = int32(-2 * bestLevel)
		}

		if narrow && (metric[i] == 0 || metric[i] == int32(-2*lm)) {
			metric[i]--
		}
	}

	// Search for optimal tf resolution using Viterbi
	tfSelect = 0
	selcost := [2]int32{}

	for sel := range 2 {
		imp0 := int32(13)
		if importance != nil && len(importance) > 0 {
			imp0 = importance[0]
		}
		isTransientInt := boolToInt(isTransient)

		cost0 := imp0 * util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+0]))
		lambdaInit := lambda
		if isTransient {
			lambdaInit = 0
		}
		cost1 := imp0*util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+1])) + lambdaInit

		for i := 1; i < nbEBands; i++ {
			imp := int32(13)
			if importance != nil && i < len(importance) {
				imp = importance[i]
			}

			curr0 := minInt32(cost0, cost1+lambda)
			curr1 := minInt32(cost0+lambda, cost1)
			cost0 = curr0 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+0]))
			cost1 = curr1 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*sel+1]))
		}

		selcost[sel] = minInt32(cost0, cost1)
	}

	if selcost[1] < selcost[0] && isTransient {
		tfSelect = 1
	}

	// Viterbi forward pass
	isTransientInt := boolToInt(isTransient)

	imp0 := int32(13)
	if importance != nil && len(importance) > 0 {
		imp0 = importance[0]
	}

	cost0 := imp0 * util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+0]))
	lambdaInit := lambda
	if isTransient {
		lambdaInit = 0
	}
	cost1 := imp0*util.Abs(metric[0]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+1])) + lambdaInit

	for i := 1; i < nbEBands; i++ {
		imp := int32(13)
		if importance != nil && i < len(importance) {
			imp = importance[i]
		}

		from0 := cost0
		from1 := cost1 + lambda
		var curr0 int32
		if from0 < from1 {
			curr0 = from0
			path0[i] = 0
		} else {
			curr0 = from1
			path0[i] = 1
		}

		from0 = cost0 + lambda
		from1 = cost1
		var curr1 int32
		if from0 < from1 {
			curr1 = from0
			path1[i] = 0
		} else {
			curr1 = from1
			path1[i] = 1
		}

		cost0 = curr0 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+0]))
		cost1 = curr1 + imp*util.Abs(metric[i]-2*int32(tfSelectTable[lm][4*isTransientInt+2*tfSelect+1]))
	}

	if cost0 < cost1 {
		tfRes[nbEBands-1] = 0
	} else {
		tfRes[nbEBands-1] = 1
	}

	// Viterbi backward pass
	for i := nbEBands - 2; i >= 0; i-- {
		if tfRes[i+1] == 1 {
			tfRes[i] = path1[i+1]
		} else {
			tfRes[i] = path0[i+1]
		}
	}

	return tfRes, tfSelect
}

// TFEncodeWithSelect encodes TF resolution with a specific tf_select value.
// This is called after TFAnalysis to encode the computed TF decisions.
//
// Parameters:
//   - re: range encoder
//   - start, end: band range
//   - isTransient: whether frame uses short blocks
//   - tfRes: per-band TF resolution flags (0 or 1)
//   - lm: log mode (frame size index)
//   - tfSelect: TF select flag from TFAnalysis
//
// Reference: libopus celt/celt_encoder.c tf_encode()
func TFEncodeWithSelect(re *rangecoding.Encoder, start, end int, isTransient bool, tfRes []int32, lm int, tfSelect int) {
	if re == nil {
		return
	}

	budget := re.StorageBits()
	tell := re.Tell()
	logp := 4
	if isTransient {
		logp = 2
	}

	// Reserve bit for tf_select if LM > 0 and there's budget
	tfSelectRsv := lm > 0 && tell+logp+1 <= int(budget)
	if tfSelectRsv {
		budget--
	}

	curr := 0
	tfChanged := 0

	for i := start; i < end; i++ {
		if tell+logp <= int(budget) {
			// Encode XOR of current tf_res with previous
			change := int(tfRes[i]) ^ curr
			re.EncodeBit(change, uint(logp))
			tell = re.Tell()
			curr = int(tfRes[i])
			tfChanged |= curr
		} else {
			// Not enough budget, force to current value
			tfRes[i] = int32(curr)
		}

		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}

	// Encode tf_select if reserved and it makes a difference
	isTransientInt := boolToInt(isTransient)
	if tfSelectRsv && tfSelectTable[lm][4*isTransientInt+0+tfChanged] != tfSelectTable[lm][4*isTransientInt+2+tfChanged] {
		re.EncodeBit(tfSelect, 1)
	} else {
		tfSelect = 0
	}

	// Convert tfRes to actual TF change values using tfSelectTable
	for i := start; i < end; i++ {
		idx := 4*isTransientInt + 2*tfSelect + int(tfRes[i])
		tfRes[i] = int32(tfSelectTable[lm][idx])
	}
}

// tfEncode encodes time-frequency resolution flags for each band with the
// default tf_select of 0. This is the inverse of tfDecode.
//
// It delegates to TFEncodeWithSelect so the entropy budget guard, tf_select
// reservation, and the tfSelectTable rewrite all match libopus tf_encode()
// (celt/celt_encoder.c) exactly. The budget guard is load-bearing at minimum
// packet sizes: when the coder runs out of bits mid-band, libopus stops coding
// tf change bits and forces tf_res[i] to the running value, so an encoder that
// keeps coding would overrun and desynchronise the decoder's allocation.
//
// Parameters:
//   - re: range encoder
//   - start, end: band range
//   - isTransient: whether frame uses short blocks
//   - tfRes: per-band TF resolution values (optional, nil = all zeros)
//   - lm: log mode (frame size index)
func tfEncode(re *rangecoding.Encoder, start, end int, isTransient bool, tfRes []int32, lm int) {
	if re == nil {
		return
	}
	if tfRes == nil {
		zero := make([]int32, end)
		TFEncodeWithSelect(re, start, end, isTransient, zero, lm, 0)
		return
	}
	TFEncodeWithSelect(re, start, end, isTransient, tfRes, lm, 0)
}

func tfDecode(start, end int, isTransient bool, tfRes []int32, lm int, rd *rangecoding.Decoder) {
	if rd == nil {
		return
	}
	budget := rd.StorageBits()
	tell := rd.Tell()
	logp := 4
	if isTransient {
		logp = 2
	}
	tfSelectRsv := lm > 0 && tell+logp+1 <= budget
	if tfSelectRsv {
		budget--
	}
	tfChanged := 0
	curr := 0
	for i := start; i < end; i++ {
		if tell+logp <= budget {
			curr ^= rd.DecodeBit(uint(logp))
			tell = rd.Tell()
			if curr != 0 {
				tfChanged = 1
			}
		}
		tfRes[i] = int32(curr)
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}
	tfSelect := 0
	if tfSelectRsv {
		idx0 := tfSelectTable[lm][4*boolToInt(isTransient)+0+tfChanged]
		idx1 := tfSelectTable[lm][4*boolToInt(isTransient)+2+tfChanged]
		if idx0 != idx1 {
			tfSelect = rd.DecodeBit(1)
		}
	}
	for i := start; i < end; i++ {
		idx := 4*boolToInt(isTransient) + 2*tfSelect + int(tfRes[i])
		tfRes[i] = int32(tfSelectTable[lm][idx])
	}
}

func tfDecode32(start, end int, isTransient bool, tfRes []int32, lm int, rd *rangecoding.Decoder) {
	tfDecode(start, end, isTransient, tfRes, lm, rd)
}
