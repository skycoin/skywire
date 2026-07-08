// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides energy encoding functions that mirror the decoder.

package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	lfeBandClamp     = 1e-4
	celtFloatEpsilon = 1e-15
)

// ComputeBandEnergies computes energy for each frequency band from MDCT coefficients.
// Returns energies in log2 scale, RELATIVE TO MEAN (same as libopus).
// energies[c*nbBands + band] = log2(amplitude) - eMeans[band]
//
// The energy computation extracts loudness per frequency band:
// 1. For each band, sum squares of MDCT coefficients
// 2. Convert to log2 scale: energy = 0.5 * log2(sumSq)
// 3. Subtract eMeans to make values mean-relative (like libopus amp2Log2)
//
// The decoder adds eMeans back during denormalization, recovering the original.
// This ensures encoder and decoder use matching gain values.
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c amp2Log2()
func (e *Encoder) ComputeBandEnergies(mdctCoeffs []float32, nbBands, frameSize int) []celtGLog {
	// Use default scratch buffer - caller should use ComputeBandEnergiesInto if they need
	// a specific destination buffer to avoid aliasing
	energiesLen := nbBands * int(e.channels)
	dst := ensureGLogSlice(&e.scratch.energies, energiesLen)
	e.ComputeBandEnergiesInto(mdctCoeffs, nbBands, frameSize, dst)
	return dst
}

// ComputeBandEnergiesF32 computes CELT band energies from float-build MDCT
// coefficients and returns the encoder scratch view.
func (e *Encoder) ComputeBandEnergiesF32(mdctCoeffs []float32, nbBands, frameSize int) []celtGLog {
	energiesLen := nbBands * int(e.channels)
	dst := ensureGLogSlice(&e.scratch.energies, energiesLen)
	e.ComputeBandEnergiesF32Into(mdctCoeffs, nbBands, frameSize, dst)
	return dst
}

// ComputeBandEnergiesInto computes band energies into the provided destination buffer.
// Use this instead of ComputeBandEnergies when you need to avoid buffer aliasing.
func (e *Encoder) ComputeBandEnergiesInto(mdctCoeffs []float32, nbBands, frameSize int, dst []celtGLog) {
	computeBandEnergiesGLogInto(mdctCoeffs, nbBands, frameSize, int(e.channels), dst)
}

// ComputeBandEnergiesF32Into computes CELT band energies into celt_glog-width
// scratch for callers that already carry float-build MDCT coefficients.
func (e *Encoder) ComputeBandEnergiesF32Into(mdctCoeffs []float32, nbBands, frameSize int, dst []celtGLog) {
	computeBandEnergiesGLogF32Into(mdctCoeffs, nbBands, frameSize, int(e.channels), 1<<GetModeConfig(frameSize).LM, dst)
}

// ComputeBandEnergiesFloat32Into computes CELT band energies in libopus
// float-build storage for callers that already carry celt_sig/celt_glog data.
func (e *Encoder) ComputeBandEnergiesFloat32Into(mdctCoeffs []float32, nbBands, frameSize int, dst []float32) {
	computeBandEnergiesFloat32Into(mdctCoeffs, nbBands, frameSize, int(e.channels), dst)
}

func computeBandEnergiesGLogInto(mdctCoeffs []float32, nbBands, frameSize, channels int, dst []celtGLog) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	coeffsPerChannel := frameSize
	if len(mdctCoeffs) < coeffsPerChannel*channels {
		if len(mdctCoeffs) < coeffsPerChannel {
			channels = 1
			coeffsPerChannel = len(mdctCoeffs)
		} else {
			channels = 1
		}
	}
	if len(dst) < nbBands*channels {
		return
	}

	silence := float32(0.5) * celtLog2(float32(1e-27))
	for c := 0; c < channels; c++ {
		channelStart := c * coeffsPerChannel
		channelEnd := channelStart + coeffsPerChannel
		if channelEnd > len(mdctCoeffs) {
			channelEnd = len(mdctCoeffs)
		}
		channelCoeffs := mdctCoeffs[channelStart:channelEnd]

		for band := 0; band < nbBands; band++ {
			start := ScaledBandStart(band, frameSize)
			end := ScaledBandEnd(band, frameSize)

			if start >= len(channelCoeffs) {
				energy := silence
				if band < len(eMeans) {
					energy -= float32(eMeans[band] * DB6)
				}
				dst[c*nbBands+band] = celtGLog(energy)
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energy := silence
				if band < len(eMeans) {
					energy -= float32(eMeans[band] * DB6)
				}
				dst[c*nbBands+band] = celtGLog(energy)
				continue
			}

			energy := computeBandRMS(channelCoeffs, start, end)
			if band < len(eMeans) {
				energy -= float32(eMeans[band] * DB6)
			}
			dst[c*nbBands+band] = celtGLog(energy)
		}
	}
}

// computeBandEnergiesGLogF32Into computes the per-band log2 amplitudes
// (amp2Log2 minus eMeans) for the encoder analysis. The band bin edges are
// eBands[i]*M where M = 1<<LM is the MDCT bin multiplier (libopus
// compute_band_energies: bins run from M*eBands[i] to M*eBands[i+1]). The
// caller passes binMul = 1<<lm so the same routine drives both the 48 kHz
// modes (shortMdctSize 120) and the native 96 kHz HD mode (shortMdctSize 240),
// where frameSize/120 would otherwise mis-scale the bin edges by 2x.
func computeBandEnergiesGLogF32Into(mdctCoeffs []float32, nbBands, frameSize, channels, binMul int, dst []celtGLog) {
	computeBandEnergiesGLogF32IntoEdges(mdctCoeffs, nbBands, frameSize, channels, binMul, dst, EBands[:], MaxBands)
}

// computeBandEnergiesGLogF32IntoEdges is the band-edge-parameterized form of
// computeBandEnergiesGLogF32Into. The standard, family, hybrid and QEXT paths
// pass the static EBands table (maxBands == MaxBands); a non-standard Opus
// Custom mode passes its per-mode edges (length nbBands+1) and nbEBands.
func computeBandEnergiesGLogF32IntoEdges(mdctCoeffs []float32, nbBands, frameSize, channels, binMul int, dst []celtGLog, edges []int, maxBands int) {
	if binMul <= 0 {
		binMul = 1
	}
	if len(edges) < 2 {
		edges = EBands[:]
		maxBands = MaxBands
	}
	if maxBands <= 0 || maxBands > len(edges)-1 {
		maxBands = len(edges) - 1
	}
	if nbBands > maxBands {
		nbBands = maxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	coeffsPerChannel := frameSize
	if len(mdctCoeffs) < coeffsPerChannel*channels {
		if len(mdctCoeffs) < coeffsPerChannel {
			channels = 1
			coeffsPerChannel = len(mdctCoeffs)
		} else {
			channels = 1
		}
	}
	if len(dst) < nbBands*channels {
		return
	}

	silence := float32(0.5) * celtLog2(float32(1e-27))
	for c := 0; c < channels; c++ {
		channelStart := c * coeffsPerChannel
		channelEnd := channelStart + coeffsPerChannel
		if channelEnd > len(mdctCoeffs) {
			channelEnd = len(mdctCoeffs)
		}
		channelCoeffs := mdctCoeffs[channelStart:channelEnd]

		for band := 0; band < nbBands; band++ {
			start := edges[band] * binMul
			end := edges[band+1] * binMul

			if start >= len(channelCoeffs) {
				energy := silence
				if band < len(eMeans) {
					energy -= float32(eMeans[band] * DB6)
				}
				dst[c*nbBands+band] = celtGLog(energy)
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energy := silence
				if band < len(eMeans) {
					energy -= float32(eMeans[band] * DB6)
				}
				dst[c*nbBands+band] = celtGLog(energy)
				continue
			}

			energy := computeBandRMSFloat32(channelCoeffs, start, end)
			if band < len(eMeans) {
				energy -= float32(eMeans[band] * DB6)
			}
			dst[c*nbBands+band] = celtGLog(energy)
		}
	}
}

func computeBandEnergiesFloat32Into(mdctCoeffs []float32, nbBands, frameSize, channels int, dst []float32) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	coeffsPerChannel := frameSize
	if len(mdctCoeffs) < coeffsPerChannel*channels {
		if len(mdctCoeffs) < coeffsPerChannel {
			channels = 1
			coeffsPerChannel = len(mdctCoeffs)
		} else {
			channels = 1
		}
	}
	if len(dst) < nbBands*channels {
		return
	}

	silence := float32(0.5) * celtLog2(float32(1e-27))
	for c := 0; c < channels; c++ {
		channelStart := c * coeffsPerChannel
		channelEnd := channelStart + coeffsPerChannel
		if channelEnd > len(mdctCoeffs) {
			channelEnd = len(mdctCoeffs)
		}
		channelCoeffs := mdctCoeffs[channelStart:channelEnd]

		for band := 0; band < nbBands; band++ {
			start := ScaledBandStart(band, frameSize)
			end := ScaledBandEnd(band, frameSize)

			if start >= len(channelCoeffs) {
				energy := silence
				if band < len(eMeans) {
					energy -= float32(eMeans[band] * DB6)
				}
				dst[c*nbBands+band] = energy
				continue
			}
			if end > len(channelCoeffs) {
				end = len(channelCoeffs)
			}
			if end <= start {
				energy := silence
				if band < len(eMeans) {
					energy -= float32(eMeans[band] * DB6)
				}
				dst[c*nbBands+band] = energy
				continue
			}

			energy := computeBandRMSFloat32(channelCoeffs, start, end)
			if band < len(eMeans) {
				energy -= float32(eMeans[band] * DB6)
			}
			dst[c*nbBands+band] = energy
		}
	}
}

func applyLFEBandLogEClamp(energies []celtGLog, nbBands, channels int) {
	if nbBands <= 2 || channels <= 0 {
		return
	}
	limitOffset := celtGLog(celtLog2(float32(lfeBandClamp)))
	floorAbs := celtGLog(celtLog2(float32(celtFloatEpsilon)))
	for c := range channels {
		base := c * nbBands
		if base+nbBands > len(energies) {
			return
		}
		baseAbs := energies[base]
		if len(eMeans) > 0 {
			baseAbs += celtGLog(eMeans[0] * DB6)
		}
		limitAbs := baseAbs + limitOffset
		for band := 2; band < nbBands; band++ {
			idx := base + band
			absE := energies[idx]
			if band < len(eMeans) {
				absE += celtGLog(eMeans[band] * DB6)
			}
			if absE > limitAbs {
				absE = limitAbs
			}
			if absE < floorAbs {
				absE = floorAbs
			}
			if band < len(eMeans) {
				absE -= celtGLog(eMeans[band] * DB6)
			}
			energies[idx] = absE
		}
	}
}

// computeBandRMS computes the per-band log2 amplitude from MDCT coefficients.
// Returns log2(sqrt(sum(x^2))) using the same epsilon as libopus.
// This matches libopus compute_band_energies() + amp2Log2() (float path).
func computeBandRMS(coeffs []float32, start, end int) float32 {
	if end <= start || start < 0 || end > len(coeffs) {
		return float32(0.5) * celtLog2(float32(1e-27))
	}

	c := coeffs[start:end:end]
	if celtFusedFloat {
		sumSq := float32(1e-27) + celtBandSumSqScalarNoFMA(c)
		return celtLog2(celtSqrt(sumSq))
	}

	// Compute sum of squares with the same accumulation order libopus uses
	// for celt_inner_prod() on the active architecture.
	sumSq := float32(1e-27) + celtInnerProdF32LibopusOrder(c)
	return float32(0.5) * celtLog2(sumSq)
}

func computeBandRMSFloat32(coeffs []float32, start, end int) float32 {
	if end <= start || start < 0 || end > len(coeffs) {
		return float32(0.5) * celtLog2(float32(1e-27))
	}
	c := coeffs[start:end:end]
	if celtFusedFloat {
		sumSq := float32(1e-27) + celtBandSumSqScalarNoFMA(c)
		return celtLog2(celtSqrt(sumSq))
	}
	sumSq := float32(1e-27) + celtInnerProdF32LibopusOrder(c)
	return float32(0.5) * celtLog2(sumSq)
}

// celtSqrt mirrors libopus celt_sqrt in the float build: (float)sqrt((double)x).
// C promotes the float argument to double, takes the double-precision sqrt, then
// narrows back to float; the Go path mirrors that double round-trip via math.Sqrt.
func celtSqrt(x float32) float32 {
	return float32(math.Sqrt(float64(x)))
}

// celtBandSumSqScalarNoFMA accumulates the band sum-of-squares in scalar order,
// matching libopus celt_inner_prod_c (xy = MAC16_16(xy, x[i], x[i]), i.e. a plain
// float32 multiply followed by a float32 add per element). Materializing each
// square through a Float32bits round-trip stops a fused build (celtFusedFloat)
// from contracting sum + x*x into a single FMADD, so the band energy that feeds
// the dynalloc boost decision tracks the scalar reference instead of the NEON
// lane-ordered inner product. With the square materialized, the accumulating add
// has no multiply left to fuse, so it needs no separate barrier. Band energy is
// computed once per frame, so the lost FMA is negligible.
func celtBandSumSqScalarNoFMA(x []float32) float32 {
	var sum float32
	for i := range x {
		p := round32(x[i] * x[i])
		sum += p
	}
	return sum
}

func celtInnerProdF32LibopusOrder(x []float32) float32 {
	if celtUseFusedFloatMath {
		return celtInnerProdNeonStyleNorm(x, x)
	}
	if celtUseSSEFloatMath {
		return celtInnerProdSSEStyleNorm(x, x)
	}
	var sum float32
	for i := range x {
		sum = celtFloatMulAdd(x[i], x[i], sum)
	}
	return sum
}

func celtAbsInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// coarseLossDistortion mirrors libopus loss_distortion() for float mode.
// Keep the accumulator in float32 so delayedIntra matches libopus state carry.
func coarseLossDistortion(energies []celtGLog, oldEBands []celtGLog, nbBands, channels, oldStride int) float32 {
	if nbBands <= 0 || channels <= 0 {
		return 0
	}
	var dist float32
	for c := range channels {
		base := c * nbBands
		oldBase := c * oldStride
		for band := range nbBands {
			idx := base + band
			oldIdx := oldBase + band
			if idx >= len(energies) || oldIdx >= len(oldEBands) {
				continue
			}
			d := energies[idx] - oldEBands[oldIdx]
			dist += d * d
		}
	}
	dist /= 128.0
	if dist > 200 {
		return 200
	}
	return dist
}

// coarseLossDistortionRange mirrors libopus loss_distortion() when coarse
// quantization operates on a band subset [start,end) (hybrid mode).
func coarseLossDistortionRange(energies []celtGLog, oldEBands []celtGLog, start, end, nbBands, channels, oldStride int) float32 {
	if nbBands <= 0 || channels <= 0 {
		return 0
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end <= start {
		return 0
	}
	var dist float32
	for c := range channels {
		base := c * nbBands
		oldBase := c * oldStride
		for band := start; band < end; band++ {
			idx := base + band
			oldIdx := oldBase + band
			if idx >= len(energies) || oldIdx >= len(oldEBands) {
				continue
			}
			d := energies[idx] - oldEBands[oldIdx]
			dist += d * d
		}
	}
	dist /= 128.0
	if dist > 200 {
		return 200
	}
	return dist
}

func (e *Encoder) encodeCoarseEnergyPass(energies []celtGLog, startBand, nbBands int, intra bool, lm int, budget int, maxDecay32 float32, encodeIntraFlag bool) ([]celtGLog, int) {
	if e.rangeEncoder == nil {
		return energies, 0
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	quantizedEnergies := ensureGLogSlice(&e.scratch.quantizedEnergies, nbBands*channels)
	coarseError := ensureGLogSlice(&e.scratch.coarseError, nbBands*channels)
	for i := range coarseError {
		coarseError[i] = 0
	}

	if encodeIntraFlag && e.rangeEncoder.Tell()+3 <= budget {
		bit := 0
		if intra {
			bit = 1
		}
		e.rangeEncoder.EncodeBit(bit, 3)
	}

	var coef32, beta32 float32
	if intra {
		coef32 = 0
		beta32 = float32(BetaIntra)
	} else {
		coef32 = float32(AlphaCoef[lm])
		beta32 = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	if startBand < 0 {
		startBand = 0
	}
	var prevBandEnergy [2]float32
	badness := 0
	for band := startBand; band < nbBands; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}

			x := float32(energies[idx])
			oldEBand := float32(e.prevEnergy[c*e.predStride()+band])
			oldE := oldEBand
			minEnergy := float32(-9.0 * DB6)
			if oldE < minEnergy {
				oldE = minEnergy
			}

			predMul := noFMA32Mul(coef32, oldE)
			f := noFMA32Sub(noFMA32Sub(x, predMul), prevBandEnergy[c])
			qi := floor32ToInt(f/float32(DB6) + 0.5)
			qi0 := qi

			decayBound := oldEBand
			minDecay := float32(-28.0 * DB6)
			if decayBound < minDecay {
				decayBound = minDecay
			}
			decayBound -= maxDecay32
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / float32(DB6))
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			tell := e.rangeEncoder.Tell()
			bitsLeft := budget - tell - 3*channels*(nbBands-band)
			remaining := budget - tell
			if band != 0 && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}
			if e.lfe && band >= 2 && qi > 0 {
				qi = 0
			}

			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = e.encodeLaplace(qi, fs, decay)
			} else if remaining >= 2 {
				if qi > 1 {
					qi = 1
				}
				if qi < -1 {
					qi = -1
				}
				var s int
				if qi < 0 {
					s = -2*qi - 1
				} else {
					s = 2 * qi
				}
				e.rangeEncoder.EncodeICDF(s, smallEnergyICDF, 2)
			} else if remaining >= 1 {
				if qi > 0 {
					qi = 0
				}
				e.rangeEncoder.EncodeBit(-qi, 1)
			} else {
				qi = -1
			}
			badness += celtAbsInt(qi0 - qi)

			q := float32(qi) * float32(DB6)
			coarseError[idx] = celtGLog(f - q)
			quantizedEnergy := noFMA32Add(noFMA32Add(predMul, prevBandEnergy[c]), q)
			quantizedEnergies[idx] = celtGLog(quantizedEnergy)
			betaMul := noFMA32Mul(beta32, q)
			prevBandEnergy[c] = noFMA32Sub(noFMA32Add(prevBandEnergy[c], q), betaMul)
		}
	}

	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands; band++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) {
				continue
			}
			e.prevEnergy[c*e.predStride()+band] = quantizedEnergies[idx]
		}
	}

	if e.lfe {
		return quantizedEnergies, 0
	}
	return quantizedEnergies, badness
}

func (e *Encoder) coarseNbAvailableBytesForBudget(budget int) int {
	nbAvailableBytes := budget / 8
	if e.coarseAvailableBytes > 0 {
		nbAvailableBytes = int(e.coarseAvailableBytes)
		maxBytes := budget / 8
		if nbAvailableBytes > maxBytes {
			nbAvailableBytes = maxBytes
		}
	}
	if nbAvailableBytes < 0 {
		nbAvailableBytes = 0
	}
	return nbAvailableBytes
}

// DecideIntraMode runs libopus-style two-pass intra/inter selection for coarse energy.
// It performs trial encodes on a saved range coder state and restores state before returning.
// startBand specifies the first band to encode (0 for CELT-only, 17 for hybrid mode).
// This matches libopus quant_coarse_energy which iterates from start to end.
func (e *Encoder) DecideIntraMode(energies []celtGLog, startBand, nbBands int, lm int) bool {
	if e.rangeEncoder == nil {
		return false
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return false
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := max(e.codedChannels(), 1)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && int(e.frameBits) < budget {
		budget = int(e.frameBits)
	}
	nbAvailableBytes := e.coarseNbAvailableBytesForBudget(budget)

	codedBands := max(nbBands-startBand, 0)
	twoPass := e.complexity >= 4
	delayedIntra := float32(e.delayedIntra)
	intra := e.forceIntra || (!twoPass &&
		delayedIntra > float32(2*channels*codedBands) &&
		nbAvailableBytes > codedBands*channels)

	intraBias := 0
	if channels > 0 {
		intraBias = int(float32(budget) * delayedIntra * float32(e.packetLoss) / float32(channels*512))
	}

	tell := e.rangeEncoder.Tell()
	if tell+3 > budget {
		return false
	}

	// Match libopus: without two-pass search, the threshold/force decision is final.
	if !twoPass || intra {
		return intra
	}

	maxDecay32 := float32(16.0 * DB6)
	// Match libopus quant_coarse_energy(): decay clamp is based on coded span
	// (end-start), not absolute end band index.
	if codedBands > 10 {
		limit := float32(0.125) * float32(nbAvailableBytes) * float32(DB6)
		if limit < maxDecay32 {
			maxDecay32 = limit
		}
	}
	if e.lfe {
		maxDecay32 = float32(3.0 * DB6)
	}

	startState := &e.scratch.coarseStartState
	e.rangeEncoder.SaveStateInto(startState)

	oldStart := ensureGLogSlice(&e.scratch.coarseOldStart, len(e.prevEnergy))
	copy(oldStart, e.prevEnergy)

	probIntra := eProbModel[lm][1][:]
	probInter := eProbModel[lm][0][:]
	workOldE := ensureGLogSlice(&e.scratch.quantizedEnergies, len(e.prevEnergy))
	workErr := ensureGLogSlice(&e.scratch.coarseError, len(e.prevEnergy))
	workEnergies := ensureGLogSlice(&e.scratch.coarseDecisionE, len(e.prevEnergy))
	for i := range workEnergies {
		workEnergies[i] = 0
	}
	// workOldE / workErr / workEnergies use the energy-prediction per-channel
	// stride: MaxBands for the static codec, the mode's nbEBands for a per-mode
	// custom layout (mono keeps c==0, so the two are identical).
	stride := e.predStride()
	for c := 0; c < channels; c++ {
		srcBase := c * nbBands
		dstBase := c * stride
		for band := 0; band < nbBands; band++ {
			srcIdx := srcBase + band
			if srcIdx >= len(energies) {
				break
			}
			workEnergies[dstBase+band] = energies[srcIdx]
		}
	}

	copy(workOldE, oldStart)
	if tell+3 <= budget {
		e.rangeEncoder.EncodeBit(1, 3)
	}
	badnessIntra := quantCoarseEnergyImpl(
		e.rangeEncoder,
		startBand,
		nbBands,
		workEnergies,
		workOldE,
		budget,
		tell,
		probIntra,
		workErr,
		channels,
		lm,
		true,
		maxDecay32,
		e.lfe,
		stride,
	)
	tellIntra := e.rangeEncoder.TellFrac()
	e.rangeEncoder.RestoreState(startState)
	copy(e.prevEnergy, oldStart)

	copy(workOldE, oldStart)
	if tell+3 <= budget {
		e.rangeEncoder.EncodeBit(0, 3)
	}
	badnessInter := quantCoarseEnergyImpl(
		e.rangeEncoder,
		startBand,
		nbBands,
		workEnergies,
		workOldE,
		budget,
		tell,
		probInter,
		workErr,
		channels,
		lm,
		false,
		maxDecay32,
		e.lfe,
		stride,
	)
	useIntra := badnessIntra < badnessInter
	if badnessIntra == badnessInter && e.rangeEncoder.TellFrac()+intraBias > tellIntra {
		useIntra = true
	}
	e.rangeEncoder.RestoreState(startState)
	copy(e.prevEnergy, oldStart)
	return useIntra
}

// EncodeCoarseEnergy encodes coarse (6dB step) band energies.
// This mirrors decoder's DecodeCoarseEnergy exactly (in reverse).
// intra=true: no inter-frame prediction (first frame or after loss)
// intra=false: uses alpha prediction from previous frame
//
// Returns the quantized energies (after encoding) for use by fine energy encoding.
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_coarse_energy()
func (e *Encoder) EncodeCoarseEnergy(energies []celtGLog, nbBands int, intra bool, lm int) []celtGLog {
	if e.rangeEncoder == nil {
		return energies
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	newDistortion := coarseLossDistortion(energies, e.prevEnergy, nbBands, channels, e.predStride())

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && int(e.frameBits) < budget {
		budget = int(e.frameBits)
	}

	// Max decay bound (full-band path: coded span is nbBands-startBand).
	maxDecay32 := float32(16.0 * DB6)
	nbAvailableBytes := e.coarseNbAvailableBytesForBudget(budget)
	if nbBands > 10 {
		limit := float32(0.125) * float32(nbAvailableBytes) * float32(DB6)
		if limit < maxDecay32 {
			maxDecay32 = limit
		}
	}
	if e.lfe {
		maxDecay32 = float32(3.0 * DB6)
	}

	quantizedEnergies, _ := e.encodeCoarseEnergyPass(energies, 0, nbBands, intra, lm, budget, maxDecay32, false)

	alpha32 := float32(AlphaCoef[lm])
	if intra {
		e.delayedIntra = opusVal32(newDistortion)
	} else {
		e.delayedIntra = opusVal32(alpha32*alpha32*float32(e.delayedIntra) + newDistortion)
	}

	return quantizedEnergies
}

// EncodeCoarseEnergyRange encodes coarse energies for bands in [start, end).
// This mirrors EncodeCoarseEnergy but only processes the specified band range.
// Bands outside the range keep their previous energy values.
func (e *Encoder) EncodeCoarseEnergyRange(energies []celtGLog, start, end int, intra bool, lm int) []celtGLog {
	if e.rangeEncoder == nil {
		return energies
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return energies
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	nbBands := end
	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}
	newDistortion := coarseLossDistortionRange(energies, e.prevEnergy, start, end, nbBands, channels, e.predStride())

	quantizedEnergies := ensureGLogSlice(&e.scratch.quantizedEnergies, nbBands*channels)
	coarseError := ensureGLogSlice(&e.scratch.coarseError, nbBands*channels)
	for i := range coarseError {
		coarseError[i] = 0
	}
	// Initialize with previous energies so bands outside [start,end) remain unchanged.
	for c := 0; c < channels; c++ {
		basePrev := c * e.predStride()
		base := c * nbBands
		for band := range nbBands {
			quantizedEnergies[base+band] = e.prevEnergy[basePrev+band]
		}
	}

	// Prediction coefficients.
	var coef32, beta32 float32
	if intra {
		coef32 = 0
		beta32 = float32(BetaIntra)
	} else {
		coef32 = float32(AlphaCoef[lm])
		beta32 = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := e.rangeEncoder.StorageBits()
	if e.frameBits > 0 && int(e.frameBits) < budget {
		budget = int(e.frameBits)
	}

	// Max decay bound (range path: clamp based on coded span, not absolute end band).
	maxDecay32 := float32(16.0 * DB6)
	nbAvailableBytes := e.coarseNbAvailableBytesForBudget(budget)
	if end-start > 10 {
		limit := float32(0.125) * float32(nbAvailableBytes) * float32(DB6)
		if limit < maxDecay32 {
			maxDecay32 = limit
		}
	}
	if e.lfe {
		maxDecay32 = float32(3.0 * DB6)
	}

	if nbBands == MaxBands {
		quantizedEnergies := ensureGLogSlice(&e.scratch.quantizedEnergies, len(e.prevEnergy))
		copy(quantizedEnergies, e.prevEnergy)
		coarseError := ensureGLogSlice(&e.scratch.coarseError, len(e.prevEnergy))
		for i := range coarseError {
			coarseError[i] = 0
		}

		prob := eProbModel[lm][0][:]
		if intra {
			prob = eProbModel[lm][1][:]
		}

		_ = quantCoarseEnergyImpl(
			e.rangeEncoder,
			start,
			end,
			energies,
			quantizedEnergies,
			budget,
			e.rangeEncoder.Tell(),
			prob,
			coarseError,
			channels,
			lm,
			intra,
			maxDecay32,
			e.lfe,
			MaxBands,
		)

		copy(e.prevEnergy, quantizedEnergies[:len(e.prevEnergy)])

		alpha32 := float32(AlphaCoef[lm])
		if intra {
			e.delayedIntra = opusVal32(newDistortion)
		} else {
			e.delayedIntra = opusVal32(alpha32*alpha32*float32(e.delayedIntra) + newDistortion)
		}

		return quantizedEnergies[:nbBands*channels]
	}

	var prevBandEnergy [2]float32
	for band := start; band < end; band++ {
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}
			x := float32(energies[idx])

			oldEBand := float32(e.prevEnergy[c*e.predStride()+band])
			oldE := oldEBand
			minEnergy := float32(-9.0 * DB6)
			if oldE < minEnergy {
				oldE = minEnergy
			}

			predMul := noFMA32Mul(coef32, oldE)
			f := noFMA32Sub(noFMA32Sub(x, predMul), prevBandEnergy[c])
			qi := floor32ToInt(f/float32(DB6) + 0.5)

			decayBound := oldEBand
			minDecay := float32(-28.0 * DB6)
			if decayBound < minDecay {
				decayBound = minDecay
			}
			decayBound -= maxDecay32
			if qi < 0 && x < decayBound {
				adjust := int((decayBound - x) / float32(DB6))
				qi += adjust
				if qi > 0 {
					qi = 0
				}
			}

			tell := e.rangeEncoder.Tell()
			bitsLeft := budget - tell - 3*channels*(end-band)
			if band != start && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}
			if e.lfe && band >= 2 && qi > 0 {
				qi = 0
			}

			if budget-tell >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = e.encodeLaplace(qi, fs, decay)
			} else if budget-tell >= 2 {
				if qi > 1 {
					qi = 1
				}
				if qi < -1 {
					qi = -1
				}
				var s int
				if qi < 0 {
					s = -2*qi - 1
				} else {
					s = 2 * qi
				}
				e.rangeEncoder.EncodeICDF(s, smallEnergyICDF, 2)
			} else if budget-tell >= 1 {
				if qi > 0 {
					qi = 0
				}
				e.rangeEncoder.EncodeBit(-qi, 1)
			} else {
				qi = -1
			}

			q := float32(qi) * float32(DB6)
			coarseError[idx] = celtGLog(f - q)
			energy := noFMA32Add(noFMA32Add(predMul, prevBandEnergy[c]), q)
			quantizedEnergies[idx] = celtGLog(energy)
			betaMul := noFMA32Mul(beta32, q)
			prevBandEnergy[c] = noFMA32Sub(noFMA32Add(prevBandEnergy[c], q), betaMul)
		}
	}

	alpha32 := float32(AlphaCoef[lm])
	if intra {
		e.delayedIntra = opusVal32(newDistortion)
	} else {
		e.delayedIntra = opusVal32(alpha32*alpha32*float32(e.delayedIntra) + newDistortion)
	}

	return quantizedEnergies
}

// encodeLaplace encodes a Laplace-distributed integer using the range encoder.
// This is the inverse of decoder's decodeLaplace.
// Uses symmetric Laplace encoding: 0, +1, -1, +2, -2, ...
//
// Parameters:
//   - val: the integer value to encode
//   - decay: controls the distribution spread
//
// Reference: libopus celt/laplace.c ec_laplace_encode()
func (e *Encoder) encodeLaplace(val int, fs int, decay int) int {
	re := e.rangeEncoder
	if re == nil {
		return val
	}

	fl := 0
	if val != 0 {
		s := 0
		if val < 0 {
			s = -1
		}
		absVal := (val + s) ^ s
		fl = fs
		fs = ec_laplace_get_freq1(fs, decay)
		i := 1
		for fs > 0 && i < absVal {
			fs *= 2
			fl += fs + 2*laplaceMinP
			fs = (fs * decay) >> 15
			i++
		}
		if fs == 0 {
			ndiMax := (laplaceFS - fl + laplaceMinP - 1) >> laplaceLogMinP
			ndiMax = (ndiMax - s) >> 1
			di := absVal - i
			if di > ndiMax-1 {
				di = ndiMax - 1
			}
			fl += (2*di + 1 + s) * laplaceMinP
			if laplaceFS-fl < laplaceMinP {
				fs = laplaceFS - fl
			} else {
				fs = laplaceMinP
			}
			absVal = i + di
			val = (absVal + s) ^ s
		} else {
			fs += laplaceMinP
			if s == 0 {
				fl += fs
			}
		}
	}
	if fl+fs > laplaceFS {
		fs = laplaceFS - fl
	}
	// Use EncodeBin with 15 bits (laplaceFS = 1 << 15 = 32768) to match libopus ec_encode_bin
	// This is critical for bit-exact encoding since EncodeBin uses shift (rng >> bits)
	// instead of division (rng / ft) which can give different results.
	re.EncodeBin(uint32(fl), uint32(fl+fs), laplaceFTBits)
	return val
}

// EncodeFineEnergy encodes fine energy refinement bits.
// This adds fractional precision to coarse energy values.
// fineBits[band] specifies bits allocated for refinement (0 = no refinement).
//
// This mirrors decoder's DecodeFineEnergy exactly (in reverse).
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_fine_energy()
func (e *Encoder) EncodeFineEnergy(energies []celtGLog, quantizedCoarse []celtGLog, nbBands int, fineBits []int32) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}

		ft := 1 << bits
		scale32 := float32(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			// Keep fine-energy quantization in float32 precision to mirror libopus float path.
			fine := energies[idx] - quantizedCoarse[idx]

			// Quantize to fineBits[band] levels
			q := max(
				// Clamp to valid range
				floor32ToInt((fine+0.5)*scale32), 0)
			if q >= ft {
				q = ft - 1
			}

			// Encode raw bits to match decoder
			re.EncodeRawBits(uint32(q), uint(bits))

			// Apply decoded offset to quantized energies for remainder coding
			offset := (float32(q)+0.5)/scale32 - 0.5
			quantizedCoarse[idx] = celtGLog(quantizedCoarse[idx] + offset)
		}
	}
}

// encodeFineEnergyFromError mirrors libopus quant_fine_energy() with prev_quant=NULL.
// It consumes and updates errorVals in-place so the same residual state can be used
// by energy finalisation and next-frame energyError clipping.
func (e *Encoder) encodeFineEnergyFromError(quantizedEnergies []celtGLog, nbBands int, fineBits []int32, errorVals []celtGLog) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := int(e.channels)
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}
		// Match libopus quant_fine_energy(): if there is not enough storage
		// left to code this band's fine bits for all channels, skip the band.
		if re.Tell()+channels*int(bits) > re.StorageBits() {
			continue
		}
		extra := 1 << uint(bits)
		scale32 := float32(extra)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			// libopus float: q2 = floor((error + 0.5) * extra)
			err := float32(errorVals[idx])
			q2 := max(floor32ToInt((err+0.5)*scale32), 0)
			if q2 > extra-1 {
				q2 = extra - 1
			}

			re.EncodeRawBits(uint32(q2), uint(bits))

			offset := (float32(q2)+0.5)*float32(uint(1)<<(14-bits))*(1.0/16384.0) - 0.5
			quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
			errorVals[idx] = celtGLog(err - offset)
		}
	}
}

// EncodeFineEnergyRange encodes fine energies for bands in [start, end).
func (e *Encoder) EncodeFineEnergyRange(energies []celtGLog, quantizedCoarse []celtGLog, start, end int, fineBits []int32) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}

	nbBands := end
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := start; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}

		ft := 1 << bits
		scale32 := float32(ft)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedCoarse) {
				continue
			}

			fine := energies[idx] - quantizedCoarse[idx]
			q := max(floor32ToInt((fine+0.5)*scale32), 0)
			if q >= ft {
				q = ft - 1
			}

			re.EncodeRawBits(uint32(q), uint(bits))

			offset := (float32(q)+0.5)/scale32 - 0.5
			quantizedCoarse[idx] = celtGLog(quantizedCoarse[idx] + offset)
		}
	}
}

// EncodeFineEnergyRangeFromError mirrors libopus quant_fine_energy() for hybrid
// range coding, consuming the in-place coarse error residual state.
func (e *Encoder) EncodeFineEnergyRangeFromError(quantizedEnergies []celtGLog, start, end int, fineBits []int32) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}

	nbBands := end
	if nbBands > len(fineBits) {
		nbBands = len(fineBits)
	}
	channels := max(int(e.channels), 1)

	required := nbBands * channels
	if len(quantizedEnergies) < required {
		channels = 1
		required = nbBands
	}

	errorVals := ensureGLogSliceNoClear(&e.scratch.coarseError, required)
	re := e.rangeEncoder

	for band := start; band < nbBands; band++ {
		bits := fineBits[band]
		if bits <= 0 {
			continue
		}
		// Match libopus: if there is not enough storage left for this band's
		// fine bits for all channels, skip this band.
		if re.Tell()+channels*int(bits) > re.StorageBits() {
			continue
		}
		extra := 1 << uint(bits)
		scale32 := float32(extra)
		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			err := float32(errorVals[idx])
			q2 := max(floor32ToInt((err+0.5)*scale32), 0)
			if q2 > extra-1 {
				q2 = extra - 1
			}

			re.EncodeRawBits(uint32(q2), uint(bits))

			offset := (float32(q2)+0.5)/scale32 - 0.5
			quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
			errorVals[idx] = celtGLog(err - offset)
		}
	}
}

// EncodeEnergyRemainder encodes any leftover precision bits.
// Called after PVQ bands decoded, uses leftover bits from bit allocation.
// This mirrors decoder's DecodeEnergyRemainder exactly (in reverse).
//
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c quant_energy_finalise()
func (e *Encoder) EncodeEnergyRemainder(energies []celtGLog, quantizedEnergies []celtGLog, nbBands int, remainderBits []int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(remainderBits) {
		nbBands = len(remainderBits)
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels {
		channels = 1
	}

	for c := 0; c < channels; c++ {
		for band := 0; band < nbBands; band++ {
			bits := remainderBits[band]
			if bits <= 0 {
				continue
			}

			idx := c*nbBands + band
			if idx >= len(energies) || idx >= len(quantizedEnergies) {
				continue
			}

			// Compute residual from already-quantized energy
			residual := energies[idx] - quantizedEnergies[idx]

			// Each bit provides finer precision
			// Encode single bit for each remainder bit
			for i := 0; i < bits && i < 8; i++ {
				// Precision for this bit level
				precision := celtGLog(DB6) / celtGLog(uint(1)<<(i+2))

				// Decide bit based on sign of residual
				var bit int
				if residual > 0 {
					bit = 1
				} else {
					bit = 0
				}

				// Encode the bit
				e.rangeEncoder.EncodeBit(bit, 1)

				// Update residual based on decision
				if bit == 1 {
					residual -= precision
				} else {
					residual += precision
				}
			}
		}
	}
}

// EncodeEnergyFinalise consumes leftover bits for additional energy refinement.
// This mirrors decoder's DecodeEnergyFinalise (libopus quant_energy_finalise).
// energies: original target energies
// quantizedEnergies: current quantized energies (coarse + fine)
// fineQuant/finePriority: allocation outputs
// bitsLeft: remaining whole bits available in the packet
func (e *Encoder) EncodeEnergyFinalise(energies []celtGLog, quantizedEnergies []celtGLog, nbBands int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	channels := int(e.channels)
	if len(energies) < nbBands*channels || len(quantizedEnergies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := range 2 {
		for band := 0; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(energies) || idx >= len(quantizedEnergies) {
					continue
				}
				errorVal := energies[idx] - quantizedEnergies[idx]
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				bitsLeft--
			}
		}
	}
}

// encodeEnergyFinaliseFromError mirrors libopus quant_energy_finalise().
// It consumes the remaining bit budget using the in-place residual state.
func (e *Encoder) encodeEnergyFinaliseFromError(quantizedEnergies []celtGLog, nbBands int, fineQuant []int32, finePriority []int32, bitsLeft int, errorVals []celtGLog) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	channels := int(e.channels)
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := range 2 {
		for band := 0; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
					continue
				}

				q2 := 0
				if float32(errorVals[idx]) >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				offset := (float32(q2) - 0.5) * float32(uint(1)<<(14-fineQuant[band]-1)) * (1.0 / 16384.0)
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				errorVals[idx] = celtGLog(float32(errorVals[idx]) - offset)
				bitsLeft--
			}
		}
	}
}

// EncodeEnergyFinaliseRange consumes leftover bits for energy refinement in [start, end).
func (e *Encoder) EncodeEnergyFinaliseRange(energies []celtGLog, quantizedEnergies []celtGLog, start, end int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	nbBands := end
	channels := int(e.channels)
	if len(energies) < nbBands*channels || len(quantizedEnergies) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for prio := range 2 {
		for band := start; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(energies) || idx >= len(quantizedEnergies) {
					continue
				}
				errorVal := energies[idx] - quantizedEnergies[idx]
				q2 := 0
				if errorVal >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)
				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				bitsLeft--
			}
		}
	}
}

// EncodeEnergyFinaliseRangeFromError mirrors libopus quant_energy_finalise() for
// hybrid range coding, consuming the in-place residual state from coarse/fine.
func (e *Encoder) EncodeEnergyFinaliseRangeFromError(quantizedEnergies []celtGLog, start, end int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if e.rangeEncoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}

	nbBands := end
	channels := max(int(e.channels), 1)

	required := nbBands * channels
	if len(quantizedEnergies) < required {
		channels = 1
		required = nbBands
	}

	errorVals := ensureGLogSliceNoClear(&e.scratch.coarseError, required)
	re := e.rangeEncoder

	for prio := range 2 {
		for band := start; band < nbBands && bitsLeft >= channels; band++ {
			if band >= len(fineQuant) || band >= len(finePriority) {
				continue
			}
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := 0; c < channels; c++ {
				idx := c*nbBands + band
				if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
					continue
				}

				q2 := 0
				if float32(errorVals[idx]) >= 0 {
					q2 = 1
				}
				re.EncodeRawBits(uint32(q2), 1)

				offset := (float32(q2) - 0.5) / float32(uint(1)<<(fineQuant[band]+1))
				quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
				errorVals[idx] = celtGLog(float32(errorVals[idx]) - offset)
				bitsLeft--
			}
		}
	}
}

// EncodeCoarseEnergyWithEncoder encodes coarse energies using an explicit range encoder.
// This variant allows passing a range encoder directly rather than using e.rangeEncoder.
func (e *Encoder) EncodeCoarseEnergyWithEncoder(re *rangecoding.Encoder, energies []celtGLog, nbBands int, intra bool, lm int) []celtGLog {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	return e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
}

// EncodeFineEnergyWithEncoder encodes fine energies using an explicit range encoder.
func (e *Encoder) EncodeFineEnergyWithEncoder(re *rangecoding.Encoder, energies []celtGLog, quantizedCoarse []celtGLog, nbBands int, fineBits []int32) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
}

func (e *Encoder) encodeFineEnergyFromErrorWithEncoder(re *rangecoding.Encoder, quantizedEnergies []celtGLog, nbBands int, fineBits []int32, errorVals []celtGLog) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.encodeFineEnergyFromError(quantizedEnergies, nbBands, fineBits, errorVals)
}

// encodeFineEnergyFromErrorWithPrev mirrors libopus quant_fine_energy() when
// prevQuant is non-nil and extraQuant carries the incremental QEXT refinement.
func (e *Encoder) encodeFineEnergyFromErrorWithPrev(quantizedEnergies []celtGLog, nbBands int, prevQuant, extraQuant []int32, errorVals []celtGLog) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(extraQuant) {
		nbBands = len(extraQuant)
	}

	channels := int(e.channels)
	if len(quantizedEnergies) < nbBands*channels || len(errorVals) < nbBands*channels {
		channels = 1
	}

	re := e.rangeEncoder

	for band := 0; band < nbBands; band++ {
		extraBits := extraQuant[band]
		if extraBits <= 0 {
			continue
		}
		if re.Tell()+channels*int(extraBits) > re.StorageBits() {
			continue
		}

		prevBits := 0
		if prevQuant != nil && band < len(prevQuant) {
			prevBits = int(prevQuant[band])
		}
		extra := 1 << uint(extraBits)
		scale32 := float32(extra)
		prevScale32 := float32(uint(1) << prevBits)

		for c := 0; c < channels; c++ {
			idx := c*nbBands + band
			if idx >= len(quantizedEnergies) || idx >= len(errorVals) {
				continue
			}

			err := float32(errorVals[idx])
			q2 := max(floor32ToInt((err*prevScale32+0.5)*scale32), 0)
			if q2 > extra-1 {
				q2 = extra - 1
			}

			re.EncodeRawBits(uint32(q2), uint(extraBits))

			offset := ((float32(q2)+0.5)/scale32 - 0.5) / prevScale32
			quantizedEnergies[idx] = celtGLog(quantizedEnergies[idx] + offset)
			errorVals[idx] = celtGLog(err - offset)
		}
	}
}

func (e *Encoder) encodeFineEnergyFromErrorWithPrevWithEncoder(re *rangecoding.Encoder, quantizedEnergies []celtGLog, nbBands int, prevQuant, extraQuant []int32, errorVals []celtGLog) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.encodeFineEnergyFromErrorWithPrev(quantizedEnergies, nbBands, prevQuant, extraQuant, errorVals)
}

func (e *Encoder) encodeFineEnergyRangeFromErrorWithEncoder(re *rangecoding.Encoder, quantizedEnergies []celtGLog, start, end int, fineBits []int32) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeFineEnergyRangeFromError(quantizedEnergies, start, end, fineBits)
}

// EncodeEnergyRemainderWithEncoder encodes remainder bits using an explicit range encoder.
func (e *Encoder) EncodeEnergyRemainderWithEncoder(re *rangecoding.Encoder, energies []celtGLog, quantizedEnergies []celtGLog, nbBands int, remainderBits []int) {
	oldRE := e.rangeEncoder
	e.rangeEncoder = re
	defer func() { e.rangeEncoder = oldRE }()

	e.EncodeEnergyRemainder(energies, quantizedEnergies, nbBands, remainderBits)
}

// EncodeCoarseEnergyHybrid encodes coarse energies for hybrid mode.
// Only encodes bands from startBand onwards (typically band 17).
func (e *Encoder) EncodeCoarseEnergyHybrid(energies []celtGLog, nbBands int, intra bool, lm int, startBand int) []celtGLog {
	if e.rangeEncoder == nil || nbBands == 0 {
		return make([]celtGLog, nbBands*int(e.channels))
	}

	return e.EncodeCoarseEnergyRange(energies, startBand, nbBands, intra, lm)
}

// EncodeFineEnergyHybrid encodes fine energies for hybrid mode.
// Only encodes bands from startBand onwards.
func (e *Encoder) EncodeFineEnergyHybrid(energies []celtGLog, quantizedCoarse []celtGLog, nbBands int, fineBits []int32, startBand int) {
	if e.rangeEncoder == nil || nbBands == 0 {
		return
	}

	e.EncodeFineEnergyRange(energies, quantizedCoarse, startBand, nbBands, fineBits)
}
