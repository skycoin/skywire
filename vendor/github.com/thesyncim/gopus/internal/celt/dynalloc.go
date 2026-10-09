// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file implements dynamic bit allocation analysis (dynalloc_analysis).

package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// EMeans contains the mean log-energy per band in libopus float-build width.
// These values are in log2 units (1.0 = 6 dB) and represent typical
// energy distribution across frequency bands.
// Source: libopus celt/quant_bands.c (float eMeans table, lines 56-62)
var EMeans = [25]celtGLog{
	6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
	4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
	4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
	4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
	3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
}

const leakBands = 19

// dynallocToneFrequencyBin mirrors the float expression in
// celt/celt_encoder.c:1210: QEXT_SCALE(tone_freq)*120 is evaluated in float,
// then division by M_PI and floor are evaluated in double. The caller scales
// native 96 kHz tone frequencies with the active mode's QEXT scale.
func dynallocToneFrequencyBin(toneFreq float32) int {
	frequencyTimes120 := float64(toneFreq * 120)
	return int(math.Floor(0.5 + frequencyTimes120/math.Pi))
}

func dynallocImportanceFromFollower(follower float32) int32 {
	if follower > 4.0 {
		follower = 4.0
	}
	imp := float32(0.5) + float32(13.0)*celtExp2(follower)
	return int32(floor32ToInt(imp))
}

// DynallocResult contains the output of dynalloc_analysis.
// These values are used for VBR target computation and bit allocation.
type DynallocResult struct {
	// MaxDepth is the maximum signal level relative to noise floor (in dB).
	// Used for floor_depth calculation in VBR.
	// Reference: libopus celt_encoder.c lines 1682-1693
	MaxDepth celtGLog

	// Offsets contains per-band allocation offsets for dynamic bit allocation.
	// Bands with high energy variance get extra bits.
	Offsets []int32

	// SpreadWeight contains per-band masking weights for spread decision.
	// Higher values indicate more perceptually important bands.
	SpreadWeight []int32

	// Importance contains per-band importance values (0-13 typically).
	// Used for bit allocation prioritization.
	Importance []int32

	// TotBoost is the total boost in bits (Q3 format).
	// Represents extra bits allocated beyond base target.
	TotBoost int
}

func medianOf3f(x []float32) float32 {
	if len(x) < 3 {
		if len(x) == 0 {
			return 0
		}
		return x[0]
	}

	var t0, t1, t2 float32
	if x[0] > x[1] {
		t0 = x[1]
		t1 = x[0]
	} else {
		t0 = x[0]
		t1 = x[1]
	}
	t2 = x[2]

	if t1 < t2 {
		return t1
	} else if t0 < t2 {
		return t2
	}
	return t0
}

func medianOf5f(x []float32) float32 {
	if len(x) < 5 {
		return medianOf3f(x)
	}

	var t0, t1, t2, t3, t4 float32
	t2 = x[2]

	if x[0] > x[1] {
		t0 = x[1]
		t1 = x[0]
	} else {
		t0 = x[0]
		t1 = x[1]
	}

	if x[3] > x[4] {
		t3 = x[4]
		t4 = x[3]
	} else {
		t3 = x[3]
		t4 = x[4]
	}

	if t0 > t3 {
		t0, t3 = t3, t0
		t1, t4 = t4, t1
	}

	if t2 > t1 {
		if t1 < t3 {
			if t2 < t3 {
				return t2
			}
			return t3
		}
		if t4 < t1 {
			return t4
		}
		return t1
	}
	if t2 < t3 {
		if t1 < t3 {
			return t1
		}
		return t3
	}
	if t2 < t4 {
		return t2
	}
	return t4
}

// median5f is median_of_5 (celt/celt_encoder.c) on five values, with
// medianOf5f's comparisons.
func median5f(x0, x1, x2, x3, x4 float32) float32 {
	t0, t1 := x0, x1
	if x0 > x1 {
		t0, t1 = x1, x0
	}
	t3, t4 := x3, x4
	if x3 > x4 {
		t3, t4 = x4, x3
	}
	if t0 > t3 {
		t3 = t0
		t1, t4 = t4, t1
	}
	t2 := x2
	if t2 > t1 {
		if t1 < t3 {
			if t2 < t3 {
				return t2
			}
			return t3
		}
		if t4 < t1 {
			return t4
		}
		return t1
	}
	if t2 < t3 {
		if t1 < t3 {
			return t1
		}
		return t3
	}
	if t2 < t4 {
		return t2
	}
	return t4
}

func computeNoiseFloor32(i, lsbDepth int, logN int16) float32 {
	eMean := float32(0.0)
	if i < len(EMeans) {
		eMean = float32(EMeans[i])
	}
	// GCONST(.0062f)*(i+5)*(i+5) multiplies left to right, rounding after
	// each product.
	return 0.0625*float32(logN) + 0.5 + float32(9-lsbDepth) - eMean + float32(0.0062*float32(i+5))*float32(i+5)
}

// DynallocAnalysis performs dynamic allocation analysis to compute:
// 1. maxDepth: signal depth relative to noise floor (for VBR floor_depth)
// 2. offsets: per-band bit allocation offsets
// 3. spread_weight: per-band masking weights for spread decision
// 4. importance: per-band importance for allocation prioritization
// 5. tot_boost: total boost bits for VBR target
//
// Parameters:
//   - bandLogE: current frame band energies (log2 domain), [channels * nbBands]
//   - bandLogE2: secondary band energies (from second MDCT for transients), [channels * nbBands]
//   - oldBandE: previous frame band energies, [channels * nbBands]
//   - nbBands: number of frequency bands
//   - start: starting band (usually 0)
//   - end: ending band (usually nbBands or less)
//   - channels: number of audio channels (1 or 2)
//   - lsbDepth: bit depth of input (16-24)
//   - lm: log2 of frame size multiplier (0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
//   - logN: per-band log2 of width in Q8 format
//   - effectiveBytes: total available bytes for encoding
//   - isTransient: true if frame is transient
//   - vbr: true if using variable bitrate
//   - constrainedVBR: true if using constrained VBR
//   - toneFreq: detected tone frequency in radians/sample (-1 if none)
//   - toneishness: tone purity metric (0.0-1.0)
//
// Reference: libopus celt/celt_encoder.c lines 1049-1273
func DynallocAnalysis(
	bandLogE, bandLogE2 []celtGLog,
	oldBandE []celtGLog,
	nbBands, start, end, channels, lsbDepth, lm int,
	logN []int16,
	effectiveBytes int,
	isTransient, vbr, constrainedVBR, lfe bool,
	toneFreq, toneishness float32,
	surroundDynalloc []celtGLog,
	analysisValid bool,
	analysisLeakBoost []uint8,
) DynallocResult {
	result := DynallocResult{
		MaxDepth:     celtGLog(-31.9),
		Offsets:      make([]int32, nbBands),
		SpreadWeight: make([]int32, nbBands),
		Importance:   make([]int32, nbBands),
		TotBoost:     0,
	}

	bandLogE32 := bandLogE
	var bandLogE2_32 []celtGLog
	if bandLogE2 != nil {
		bandLogE2_32 = bandLogE2
	}
	var oldBandE32 []celtGLog
	if oldBandE != nil {
		oldBandE32 = oldBandE
	}

	// Compute noise floor per band
	noiseFloor := make([]float32, end)
	for i := range end {
		logNVal := int16(0)
		if i < len(logN) {
			logNVal = logN[i]
		}
		noiseFloor[i] = computeNoiseFloor32(i, lsbDepth, logNVal)
	}

	// Compute maxDepth: max(bandLogE - noiseFloor) across all bands and channels
	maxDepth32 := float32(result.MaxDepth)
	for c := range channels {
		for i := range end {
			idx := c*nbBands + i
			if idx < len(bandLogE32) {
				maxDepth32 = opusmath.MaxF32(maxDepth32, bandLogE32[idx]-noiseFloor[i])
			}
		}
	}
	result.MaxDepth = celtGLog(maxDepth32)

	// Compute spread_weight using a simple masking model
	// Reference: libopus lines 1082-1117
	{
		mask := make([]float32, nbBands)
		sig := make([]float32, nbBands)

		// Initialize mask with signal relative to noise floor
		for i := range end {
			if i < len(bandLogE32) {
				mask[i] = bandLogE32[i] - noiseFloor[i]
			}
		}

		// For stereo, take max across channels
		if channels == 2 {
			for i := range end {
				idx := nbBands + i
				if idx < len(bandLogE32) {
					ch2Val := bandLogE32[idx] - noiseFloor[i]
					if ch2Val > mask[i] {
						mask[i] = ch2Val
					}
				}
			}
		}

		copy(sig, mask)

		// Forward masking: mask[i] = max(mask[i], mask[i-1] - 2)
		for i := 1; i < end; i++ {
			if mask[i-1]-2.0 > mask[i] {
				mask[i] = mask[i-1] - 2.0
			}
		}

		// Backward masking: mask[i] = max(mask[i], mask[i+1] - 3)
		for i := end - 2; i >= 0; i-- {
			if mask[i+1]-3.0 > mask[i] {
				mask[i] = mask[i+1] - 3.0
			}
		}

		// Compute SMR (Signal to Mask Ratio) and spread weight
		for i := range end {
			// Mask is never more than 72 dB below peak and never below noise floor
			maskThresh := float32(0)
			if maxDepth32-12.0 > mask[i] {
				maskThresh = maxDepth32 - 12.0
			} else {
				maskThresh = mask[i]
			}
			if maskThresh < 0 {
				maskThresh = 0
			}
			smr := sig[i] - maskThresh

			// Clamp shift to [0, 5] range
			shift := min(max(-floor32ToInt(0.5+smr), 0), 5)
			result.SpreadWeight[i] = 32 >> shift
		}
	}

	// Make sure dynamic allocation doesn't bust the budget
	// Enable starting at 24 kb/s for 20ms frames, 96 kb/s for 2.5ms frames
	// Reference: libopus line 1121: if (effectiveBytes >= (30 + 5*LM) && !lfe)
	minBytes := 30 + 5*lm
	if effectiveBytes >= minBytes && !lfe {
		// Compute follower (smoothed band energies for dynamic allocation)
		follower := make([]float32, channels*nbBands)
		last := 0

		for c := range channels {
			// Use bandLogE2 (secondary MDCT for transients) or fallback to bandLogE
			bandLogE3 := make([]float32, end)
			for i := range end {
				idx := c*nbBands + i
				if bandLogE2_32 != nil && idx < len(bandLogE2_32) {
					bandLogE3[i] = bandLogE2_32[idx]
				} else if idx < len(bandLogE32) {
					bandLogE3[i] = bandLogE32[idx]
				}
			}

			// For 2.5ms frames (LM=0), first 8 bands have high variance
			// Take max with previous energy for stability
			if lm == 0 {
				for i := 0; i < min(8, end); i++ {
					idx := c*nbBands + i
					if oldBandE32 != nil && idx < len(oldBandE32) {
						if oldBandE32[idx] > bandLogE3[i] {
							bandLogE3[i] = oldBandE32[idx]
						}
					}
				}
			}

			f := follower[c*nbBands : (c+1)*nbBands]
			if end > 0 {
				f[0] = bandLogE3[0]
			}

			// Forward pass: find last band at least 3dB higher than previous
			for i := 1; i < end; i++ {
				if bandLogE3[i] > bandLogE3[i-1]+0.5 {
					last = i
				}
				if f[i-1]+1.5 < bandLogE3[i] {
					f[i] = f[i-1] + 1.5
				} else {
					f[i] = bandLogE3[i]
				}
			}

			// Backward pass: smooth from the last significant band
			for i := last - 1; i >= 0; i-- {
				fwd := f[i+1] + 2.0
				if fwd > bandLogE3[i] {
					fwd = bandLogE3[i]
				}
				if fwd < f[i] {
					f[i] = fwd
				}
			}

			// Apply median filter to avoid unnecessary dynalloc triggering
			offset := 1.0
			for i := 2; i < end-2; i++ {
				medVal := medianOf5f(bandLogE3[i-2:])
				if medVal-float32(offset) > f[i] {
					f[i] = medVal - float32(offset)
				}
			}

			// Handle edge bands with median of 3
			if end >= 3 {
				tmp := medianOf3f(bandLogE3[0:3]) - float32(offset)
				if tmp > f[0] {
					f[0] = tmp
				}
				if tmp > f[1] {
					f[1] = tmp
				}

				tmp = medianOf3f(bandLogE3[end-3:end]) - float32(offset)
				if tmp > f[end-2] {
					f[end-2] = tmp
				}
				if tmp > f[end-1] {
					f[end-1] = tmp
				}
			}

			// Clamp to noise floor
			for i := range end {
				if noiseFloor[i] > f[i] {
					f[i] = noiseFloor[i]
				}
			}
		}

		// For stereo: consider cross-talk (24 dB)
		if channels == 2 {
			for i := start; i < end; i++ {
				// Cross-channel influence
				ch0 := follower[i]
				ch1 := follower[nbBands+i]
				if ch0-4.0 > ch1 {
					follower[nbBands+i] = ch0 - 4.0
				}
				if ch1-4.0 > ch0 {
					follower[i] = ch1 - 4.0
				}

				// Combine channels: average of (bandLogE - follower) for each channel
				boost0 := float32(0.0)
				boost1 := float32(0.0)
				if i < len(bandLogE32) {
					boost0 = bandLogE32[i] - follower[i]
					if boost0 < 0 {
						boost0 = 0
					}
				}
				if nbBands+i < len(bandLogE32) {
					boost1 = bandLogE32[nbBands+i] - follower[nbBands+i]
					if boost1 < 0 {
						boost1 = 0
					}
				}
				follower[i] = (boost0 + boost1) / 2.0
			}
		} else {
			for i := start; i < end; i++ {
				if i < len(bandLogE32) {
					follower[i] = bandLogE32[i] - follower[i]
					if follower[i] < 0 {
						follower[i] = 0
					}
				}
			}
		}

		for i := start; i < end; i++ {
			if i < len(surroundDynalloc) {
				v := float32(surroundDynalloc[i])
				if v > follower[i] {
					follower[i] = v
				}
			}
		}

		// Compute importance weights
		for i := start; i < end; i++ {
			result.Importance[i] = int32(dynallocImportanceFromFollower(follower[i]))
		}

		// For non-transient CBR/CVBR frames, libopus halves dynalloc.
		if (!vbr || constrainedVBR) && !isTransient {
			for i := start; i < end; i++ {
				follower[i] *= 0.5
			}
		}

		// Frequency-dependent weighting
		for i := start; i < end; i++ {
			if i < 8 {
				follower[i] *= 2.0
			}
			if i >= 12 {
				follower[i] /= 2.0
			}
		}

		// Compensate for Opus under-allocation on tones.
		if toneishness > 0.98 && toneFreq >= 0 {
			freqBin := dynallocToneFrequencyBin(toneFreq)
			for i := start; i < end; i++ {
				if freqBin >= EBands[i] && freqBin <= EBands[i+1] {
					follower[i] += 2.0
				}
				if freqBin >= EBands[i]-1 && freqBin <= EBands[i+1]+1 {
					follower[i] += 1.0
				}
				if freqBin >= EBands[i]-2 && freqBin <= EBands[i+1]+2 {
					follower[i] += 1.0
				}
				if freqBin >= EBands[i]-3 && freqBin <= EBands[i+1]+3 {
					follower[i] += 0.5
				}
			}
			if end > start && freqBin >= EBands[end] {
				follower[end-1] += 2.0
				if end-2 >= start {
					follower[end-2] += 1.0
				}
			}
		}

		if analysisValid {
			// Match libopus dynalloc: follower += analysis->leak_boost/64 on the
			// first LEAK_BANDS when analysis is valid.
			leakEnd := min(end, leakBands)
			if leakEnd > start {
				for i := start; i < leakEnd; i++ {
					if i < len(analysisLeakBoost) {
						follower[i] += float32(analysisLeakBoost[i]) * (1.0 / 64.0)
					}
				}
			}
		}

		// Clamp follower and compute offsets/boost
		// Reference: libopus celt_encoder.c lines 1232-1265
		//
		// IMPORTANT: In libopus FLOAT mode, SHR32 is a NO-OP!
		// From arch.h line 313: #define SHR32(a,shift) (a)
		//
		// So in float mode:
		//   follower[i] = MIN(follower[i], 4.0);  // clamp to 4.0
		//   follower[i] = SHR32(follower[i], 8);  // NO-OP in float mode
		//   boost = (int)SHR32(follower[i]*..., DB_SHIFT-8);  // NO-OP in float mode
		//
		// This means boost = (int)(follower * factor) directly, no scaling.
		// The boost value is the NUMBER OF QUANTA to allocate (a count).
		totBoost := 0
		for i := start; i < end; i++ {
			if follower[i] > 4.0 {
				follower[i] = 4.0
			}

			// In float mode, SHR32(follower, 8) is a no-op
			followerVal := follower[i]

			// Compute band width
			width := channels * ScaledBandWidth(i, 120<<lm) // 120 is base frame size
			if width <= 0 {
				width = 1
			}

			var boost, boostBits int

			// Different scaling based on band width
			// Reference: libopus lines 1242-1252
			// In float mode, SHR32 is a no-op, so:
			// - width < 6: boost = (int)follower
			// - width > 48: boost = (int)(follower * 8)
			// - else: boost = (int)(follower * width / 6)
			if width < 6 {
				boost = int(followerVal)
				boostBits = boost * width << bitRes
			} else if width > 48 {
				boost = int(followerVal * 8.0)
				boostBits = (boost * width << bitRes) / 8
			} else {
				boost = int(followerVal * float32(width) / 6.0)
				boostBits = boost * 6 << bitRes
			}

			// For CBR and non-transient CVBR, limit dynalloc to 2/3 of bits
			if (!vbr || (constrainedVBR && !isTransient)) &&
				(totBoost+boostBits)>>bitRes>>3 > 2*effectiveBytes/3 {
				cap := (2 * effectiveBytes / 3) << bitRes << 3
				result.Offsets[i] = int32(cap - totBoost)
				totBoost = cap
				break
			} else {
				result.Offsets[i] = int32(boost)
				totBoost += boostBits
			}
		}
		result.TotBoost = totBoost
	} else {
		// Not enough bits for dynalloc, set uniform importance
		for i := start; i < end; i++ {
			result.Importance[i] = 13
		}
	}

	return result
}

// DynallocScratch holds pre-allocated buffers for DynallocAnalysis.
type DynallocScratch struct {
	// Result arrays (caller provides these in result struct)
	Offsets      []int32
	SpreadWeight []int32
	Importance   []int32

	// Libopus-width scratch buffers.
	NoiseFloor []float32

	// Masking model buffers
	Mask      []float32
	Sig       []float32
	Follower  []float32
	BandLogE3 []float32

	// Zero-padded copies of short band-energy inputs.
	padE, padE2 []celtGLog
}

// EnsureDynallocScratch ensures scratch buffers are large enough.
func (s *DynallocScratch) EnsureDynallocScratch(nbBands, channels int) {
	maxSize := nbBands * channels
	if cap(s.Offsets) < nbBands {
		s.Offsets = make([]int32, nbBands)
	} else {
		s.Offsets = s.Offsets[:nbBands]
	}
	if cap(s.SpreadWeight) < nbBands {
		s.SpreadWeight = make([]int32, nbBands)
	} else {
		s.SpreadWeight = s.SpreadWeight[:nbBands]
	}
	if cap(s.Importance) < nbBands {
		s.Importance = make([]int32, nbBands)
	} else {
		s.Importance = s.Importance[:nbBands]
	}
	if cap(s.NoiseFloor) < nbBands {
		s.NoiseFloor = make([]float32, nbBands)
	} else {
		s.NoiseFloor = s.NoiseFloor[:nbBands]
	}
	if cap(s.Mask) < nbBands {
		s.Mask = make([]float32, nbBands)
	} else {
		s.Mask = s.Mask[:nbBands]
	}
	if cap(s.Sig) < nbBands {
		s.Sig = make([]float32, nbBands)
	} else {
		s.Sig = s.Sig[:nbBands]
	}
	if cap(s.Follower) < maxSize {
		s.Follower = make([]float32, maxSize)
	} else {
		s.Follower = s.Follower[:maxSize]
	}
	if cap(s.BandLogE3) < nbBands {
		s.BandLogE3 = make([]float32, nbBands)
	} else {
		s.BandLogE3 = s.BandLogE3[:nbBands]
	}
}

// DynallocAnalysisWithScratch is the zero-allocation version of DynallocAnalysis:
// dynalloc_analysis from celt_encoder.c over the caller's scratch. bandLogE and
// bandLogE2 hold channels bands of stride nbBands (a nil bandLogE2 reads
// bandLogE); missing trailing energies read as zero.
func DynallocAnalysisWithScratch(
	bandLogE, bandLogE2 []celtGLog,
	oldBandE []celtGLog,
	nbBands, start, end, channels, lsbDepth, lm int,
	logN []int16,
	effectiveBytes int,
	isTransient, vbr, constrainedVBR, lfe bool,
	toneFreq, toneishness float32,
	surroundDynalloc []celtGLog,
	analysisValid bool,
	analysisLeakBoost []uint8,
	scratch *DynallocScratch,
	edges []int,
) DynallocResult {
	if scratch == nil {
		scratch = &DynallocScratch{}
	}
	nbBands = max(nbBands, 0)
	scratch.EnsureDynallocScratch(nbBands, channels)
	end = min(end, nbBands)
	start = min(max(start, 0), end)

	result := DynallocResult{
		MaxDepth:     celtGLog(-31.9),
		Offsets:      scratch.Offsets[:nbBands],
		SpreadWeight: scratch.SpreadWeight[:nbBands],
		Importance:   scratch.Importance[:nbBands],
	}
	clear(result.Offsets)
	clear(result.SpreadWeight)
	clear(result.Importance)
	if end <= 0 {
		return result
	}

	// Every loop below indexes [0,end) of a channel, so each channel view is
	// sliced to end once.
	total := channels * nbBands
	if bandLogE2 == nil {
		bandLogE2 = bandLogE
	}
	bandLogE = scratch.dynallocPadded(bandLogE, total, &scratch.padE)
	bandLogE2 = scratch.dynallocPadded(bandLogE2, total, &scratch.padE2)
	e0 := bandLogE[:end]
	var e1 []celtGLog
	if channels == 2 {
		e1 = bandLogE[nbBands:][:end]
	}

	noiseFloor := scratch.NoiseFloor[:end]
	for i := range noiseFloor {
		logNVal := int16(0)
		if i < len(logN) {
			logNVal = logN[i]
		}
		noiseFloor[i] = computeNoiseFloor32(i, lsbDepth, logNVal)
	}

	maxDepth := float32(result.MaxDepth)
	for c := range channels {
		ec := bandLogE[c*nbBands:][:end]
		for i, v := range ec {
			maxDepth = opusmath.MaxF32(maxDepth, v-noiseFloor[i])
		}
	}
	result.MaxDepth = celtGLog(maxDepth)

	// Masking model for the spreading decision. The MAXG/MING steps are
	// conditional moves with C's operand order: MAXG(a, b) is a > b ? a : b.
	mask := scratch.Mask[:end]
	sig := scratch.Sig[:end]
	for i, v := range e0 {
		mask[i] = v - noiseFloor[i]
	}
	if channels == 2 {
		for i, v := range e1[:len(mask)] {
			mask[i] = opusmath.MaxF32(mask[i], v-noiseFloor[i])
		}
	}
	copy(sig, mask)
	for i := 1; i < end; i++ {
		mask[i] = opusmath.MaxF32(mask[i], mask[i-1]-2.0)
	}
	for i, next := end-2, mask[end-1]; i >= 0; i-- {
		next = opusmath.MaxF32(mask[i], next-3.0)
		mask[i] = next
	}
	floorDepth := opusmath.MaxF32(0, maxDepth-12.0)
	spreadWeight := result.SpreadWeight[:end]
	for i, v := range sig {
		smr := v - opusmath.MaxF32(floorDepth, mask[i])
		shift := min(max(-floor32ToInt(0.5+smr), 0), 5)
		spreadWeight[i] = 32 >> shift
	}

	// Dynamic allocation, budget permitting (effectiveBytes >= 30 + 5*LM).
	if effectiveBytes < 30+5*lm || lfe {
		for i := start; i < end; i++ {
			result.Importance[i] = 13
		}
		return result
	}

	follower := scratch.Follower[:total]
	clear(follower)
	last := 0
	bandLogE3 := scratch.BandLogE3[:end]
	for c := range channels {
		copy(bandLogE3, bandLogE2[c*nbBands:][:end])
		if lm == 0 {
			// 2.5 ms frames: the first 8 bands have one bin each, so their
			// energy takes the max with the previous frame's.
			for i := range min(8, end) {
				if idx := c*nbBands + i; idx < len(oldBandE) && oldBandE[idx] > bandLogE3[i] {
					bandLogE3[i] = oldBandE[idx]
				}
			}
		}

		// The forward and backward follower passes carry the neighbouring
		// value in a register; each step keeps libopus's operand order.
		f := follower[c*nbBands:][:end]
		prevE, prevF := bandLogE3[0], bandLogE3[0]
		f[0] = prevF
		for i := 1; i < len(f); i++ {
			cur := bandLogE3[i]
			if cur > prevE+0.5 {
				last = i
			}
			prevF = opusmath.MinF32(prevF+1.5, cur)
			f[i] = prevF
			prevE = cur
		}
		if last > 0 {
			fl, el := f[:last+1], bandLogE3[:last+1]
			next := fl[last]
			for i := last - 1; i >= 0; i-- {
				next = opusmath.MinF32(fl[i], opusmath.MinF32(next+2.0, el[i]))
				fl[i] = next
			}
		}

		const offset = float32(1.0)
		if end >= 5 {
			// median_of_5 over the sliding window bandLogE3[i-2..i+2].
			x0, x1, x2, x3 := bandLogE3[0], bandLogE3[1], bandLogE3[2], bandLogE3[3]
			ahead := bandLogE3[4:]
			fm := f[2:][:len(ahead)]
			for j, x4 := range ahead {
				fm[j] = opusmath.MaxF32(fm[j], median5f(x0, x1, x2, x3, x4)-offset)
				x0, x1, x2, x3 = x1, x2, x3, x4
			}
		}
		if end >= 3 {
			tmp := medianOf3f(bandLogE3[0:3]) - offset
			if tmp > f[0] {
				f[0] = tmp
			}
			if tmp > f[1] {
				f[1] = tmp
			}
			tmp = medianOf3f(bandLogE3[end-3:end]) - offset
			if tmp > f[end-2] {
				f[end-2] = tmp
			}
			if tmp > f[end-1] {
				f[end-1] = tmp
			}
		}
		for i, nf := range noiseFloor {
			f[i] = opusmath.MaxF32(f[i], nf)
		}
	}

	f0 := follower[start:end]
	if channels == 2 {
		// Consider 24 dB cross-talk between the channels.
		f1 := follower[nbBands:][start:end]
		l, r := e0[start:end], e1[start:end]
		for i := range f0 {
			ch0, ch1 := f0[i], f1[i]
			if ch0-4.0 > ch1 {
				ch1 = ch0 - 4.0
				f1[i] = ch1
			}
			if ch1-4.0 > ch0 {
				ch0 = ch1 - 4.0
			}
			boost0 := l[i] - ch0
			if boost0 < 0 {
				boost0 = 0
			}
			boost1 := r[i] - ch1
			if boost1 < 0 {
				boost1 = 0
			}
			f0[i] = (boost0 + boost1) / 2.0
		}
	} else {
		l := e0[start:end]
		for i := range f0 {
			v := l[i] - f0[i]
			if v < 0 {
				v = 0
			}
			f0[i] = v
		}
	}

	for i := start; i < min(end, len(surroundDynalloc)); i++ {
		if v := float32(surroundDynalloc[i]); v > follower[i] {
			follower[i] = v
		}
	}

	importance := result.Importance[start:end]
	for i, v := range f0 {
		importance[i] = dynallocImportanceFromFollower(v)
	}

	// Non-transient CBR/CVBR frames halve the dynalloc contribution.
	if (!vbr || constrainedVBR) && !isTransient {
		for i := range f0 {
			f0[i] *= 0.5
		}
	}
	for j := range f0 {
		if start+j < 8 {
			f0[j] *= 2.0
		}
		if start+j >= 12 {
			f0[j] /= 2.0
		}
	}

	// Compensate for Opus' under-allocation on tones.
	if toneishness > 0.98 && toneFreq >= 0 {
		freqBin := dynallocToneFrequencyBin(toneFreq)
		for i := start; i < end; i++ {
			if freqBin >= edges[i] && freqBin <= edges[i+1] {
				follower[i] += 2.0
			}
			if freqBin >= edges[i]-1 && freqBin <= edges[i+1]+1 {
				follower[i] += 1.0
			}
			if freqBin >= edges[i]-2 && freqBin <= edges[i+1]+2 {
				follower[i] += 1.0
			}
			if freqBin >= edges[i]-3 && freqBin <= edges[i+1]+3 {
				follower[i] += 0.5
			}
		}
		if end > start && freqBin >= edges[end] {
			follower[end-1] += 2.0
			if end-2 >= start {
				follower[end-2] += 1.0
			}
		}
	}

	if analysisValid {
		// follower += analysis->leak_boost/64 on the first LEAK_BANDS.
		if stop := min(end, leakBands, len(analysisLeakBoost)); stop > start {
			fl, lb := follower[start:stop], analysisLeakBoost[start:stop]
			for j := range fl {
				fl[j] += float32(lb[j]) * (1.0 / 64.0)
			}
		}
	}

	totBoost := 0
	bandEdges := edges[start : end+1]
	offsets := result.Offsets[start:end]
	lo := bandEdges[0]
	for i, hi := range bandEdges[1:][:len(f0)] {
		followerVal := f0[i]
		if followerVal > 4.0 {
			followerVal = 4.0
			f0[i] = followerVal
		}
		width := max(channels*((hi-lo)<<lm), 1)
		lo = hi

		var boost, boostBits int
		if width < 6 {
			boost = int(followerVal)
			boostBits = boost * width << bitRes
		} else if width > 48 {
			boost = int(followerVal * 8.0)
			boostBits = (boost * width << bitRes) / 8
		} else {
			boost = int(followerVal * float32(width) / 6.0)
			boostBits = boost * 6 << bitRes
		}

		// CBR and non-transient CVBR frames limit dynalloc to 2/3 of the bits.
		if (!vbr || (constrainedVBR && !isTransient)) &&
			(totBoost+boostBits)>>bitRes>>3 > 2*effectiveBytes/3 {
			cap := (2 * effectiveBytes / 3) << bitRes << 3
			offsets[i] = int32(cap - totBoost)
			totBoost = cap
			break
		}

		offsets[i] = int32(boost)
		totBoost += boostBits
	}
	result.TotBoost = totBoost
	return result
}

// dynallocPadded returns x when it holds n values, or else x's values
// followed by zeros in buf.
func (s *DynallocScratch) dynallocPadded(x []celtGLog, n int, buf *[]celtGLog) []celtGLog {
	if len(x) >= n {
		return x[:n]
	}
	p := ensureGLogSlice(buf, n)
	copy(p, x)
	clear(p[len(x):])
	return p
}
