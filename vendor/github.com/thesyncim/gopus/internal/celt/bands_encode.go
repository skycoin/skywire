// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file contains band encoding: normalization and PVQ quantization.

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// NormalizeBands divides each band's MDCT coefficients by its energy,
// producing unit-norm shapes ready for PVQ quantization.
// Returns shapes[band] = normalized coefficients for that band.
//
// The decoder does: output = shape * gain (denormalization)
// So encoder does: shape = input / gain (normalization)
//
// Parameters:
//   - mdctCoeffs: MDCT coefficients for all bands concatenated
//   - energies: per-band energy values (log2 scale from coarse + fine energy)
//   - nbBands: number of bands to process
//   - frameSize: frame size in samples (120, 240, 480, 960)
//
// Returns: shapes[band] = normalized CELT vector with unit L2 norm
//
// Reference: RFC 6716 Section 4.3.4.1
func (e *Encoder) NormalizeBands(mdctCoeffs []CeltNorm, energies []celtGLog, nbBands, frameSize int) [][]CeltNorm {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil
	}
	if len(energies) < nbBands {
		return nil
	}

	shapes := make([][]CeltNorm, nbBands)
	offset := 0

	for band := range nbBands {
		// Get band boundaries
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			shapes[band] = []CeltNorm{}
			continue
		}

		// Extract coefficients for this band
		if offset+n > len(mdctCoeffs) {
			// Not enough coefficients - use zeros
			shapes[band] = make([]CeltNorm, n)
			for i := range shapes[band] {
				shapes[band][i] = 0
			}
			offset += n
			continue
		}

		// Compute gain from energy (log2 units, 1 = 6 dB)
		// The decoder uses: e = decoded_energy + eMeans*DB6; gain = 2^(e/DB6)
		// The encoder must use the SAME formula for normalization, using the
		// quantized energy that was encoded to the bitstream.
		// This ensures: normalized * decoder_gain = original (up to quantization)
		e_val := energies[band]
		if band < len(eMeans) {
			e_val += celtGLog(eMeans[band] * DB6)
		}
		if e_val > celtGLog(32*DB6) {
			e_val = celtGLog(32 * DB6) // Match decoder's clamp (bands.go:102-103)
		}
		gain := celtExp2(float32(e_val) / float32(DB6))

		// Allocate shape vector
		shape := make([]CeltNorm, n)

		// Handle degenerate case: gain near zero
		if gain < 1e-15 {
			// Set shape to first-unit-vector [1, 0, 0, ...]
			shape[0] = 1.0
			for i := 1; i < n; i++ {
				shape[i] = 0.0
			}
			shapes[band] = shape
			offset += n
			continue
		}

		// Divide coefficients by gain
		allZero := true
		for i := range n {
			shape[i] = CeltNorm(float32(mdctCoeffs[offset+i]) / gain)
			v := float32(shape[i])
			if v < 0 {
				v = -v
			}
			if v > 1e-15 {
				allZero = false
			}
		}

		// Handle case where all coefficients are zero
		if allZero {
			// Set shape to first-unit-vector [1, 0, 0, ...]
			shape[0] = 1.0
			for i := 1; i < n; i++ {
				shape[i] = 0.0
			}
		} else {
			// Normalize to unit L2 norm for PVQ encoding
			// PVQ expects unit-norm input vectors. The decoder will reconstruct
			// the shape (also unit-norm) and then scale by gain to get the
			// original magnitude back.
			var norm float32
			for i := range n {
				v := float32(shape[i])
				norm += v * v
			}
			if norm > 1e-30 {
				norm = 1 / celtRSqrt(norm)
				for i := range n {
					shape[i] = CeltNorm(float32(shape[i]) / norm)
				}
			}
		}

		shapes[band] = shape
		offset += n
	}

	return shapes
}

// ComputeLinearBandAmplitudes computes linear band amplitudes directly from MDCT coefficients.
// This matches libopus compute_band_energies() which returns sqrt(sum of squares) per band.
// The result is in LINEAR scale (not log), ready to use as normalization divisor.
//
// CRITICAL: This function computes the ORIGINAL linear amplitude from MDCT coefficients,
// which must be used for normalization. Do NOT use log-domain energies converted back to linear,
// as that introduces quantization/roundtrip errors.
//
// Reference: libopus celt/bands.c compute_band_energies() (float path, lines 154-170)
func ComputeLinearBandAmplitudes(mdctCoeffs []float32, nbBands, frameSize int) []celtEner {
	bandE := make([]celtEner, nbBands)
	ComputeLinearBandAmplitudesInto(mdctCoeffs, nbBands, frameSize, bandE)
	return bandE
}

// ComputeLinearBandAmplitudesInto computes linear band amplitudes into the provided buffer.
// This is the zero-allocation version of ComputeLinearBandAmplitudes.
func ComputeLinearBandAmplitudesInto(mdctCoeffs []float32, nbBands, frameSize int, bandE []celtEner) {
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if frameSize <= 0 {
		return
	}
	offset := 0

	for band := range nbBands {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			bandE[band] = celtEner(1e-27) // epsilon like libopus
			continue
		}
		if offset+n > len(mdctCoeffs) {
			bandE[band] = celtEner(1e-27)
			offset += n
			continue
		}

		// Compute sum of squares with libopus epsilon
		// Reference: libopus bands.c line 164:
		//   sum = 1e-27f + celt_inner_prod(&X[...], &X[...], ...)
		coeffBand := mdctCoeffs[offset : offset+n : offset+n]
		sum := noFMA32Add(float32(1e-27), celtInnerProdF32LibopusOrder(coeffBand))

		// sqrt(sum) gives the amplitude (L2 norm)
		// Reference: libopus bands.c line 165:
		//   bandE[i+c*m->nbEBands] = celt_sqrt(sum)
		// Keep float-path precision: celt_ener is float in libopus.
		bandE[band] = celtEner(opusmath.SqrtF32(sum))
		offset += n
	}
}

// ComputeLinearBandAmplitudesIntoF32 computes float-build linear band
// amplitudes into the provided buffer.
func ComputeLinearBandAmplitudesIntoF32(mdctCoeffs []float32, nbBands, frameSize int, bandE []celtEner) {
	computeLinearBandAmplitudesIntoF32BinMul(mdctCoeffs, nbBands, frameSize/Overlap, bandE)
}

// computeLinearBandAmplitudesIntoF32BinMul computes per-band linear amplitudes
// using an explicit bin multiplier M (libopus M=1<<LM). The band edges are
// eBands[i]*M, matching compute_band_energies(). For the 48 kHz mode M equals
// frameSize/Overlap; for the native 96 kHz HD mode (frameSize 1920, overlap 240)
// M=1<<LM=8 rather than frameSize/120=16, so callers thread the real M.
func computeLinearBandAmplitudesIntoF32BinMul(mdctCoeffs []float32, nbBands, binMul int, bandE []celtEner) {
	computeLinearBandAmplitudesIntoF32BinMulWidths(mdctCoeffs, nbBands, binMul, bandE, eBandWidths[:], MaxBands)
}

// computeLinearBandAmplitudesIntoF32BinMulWidths is the band-width-parameterized
// form. The standard path passes eBandWidths (maxBands == MaxBands); a
// non-standard Opus Custom mode passes its per-mode band widths and nbEBands.
func computeLinearBandAmplitudesIntoF32BinMulWidths(mdctCoeffs []float32, nbBands, binMul int, bandE []celtEner, widths []int, maxBands int) {
	if len(widths) < 1 {
		widths = eBandWidths[:]
		maxBands = MaxBands
	}
	if maxBands <= 0 || maxBands > len(widths) {
		maxBands = len(widths)
	}
	if nbBands <= 0 || nbBands > maxBands {
		return
	}
	if binMul <= 0 {
		return
	}
	offset := 0

	for band := range nbBands {
		n := widths[band] * binMul
		if n <= 0 {
			bandE[band] = celtEner(1e-27)
			continue
		}
		if offset+n > len(mdctCoeffs) {
			bandE[band] = celtEner(1e-27)
			offset += n
			continue
		}

		coeffBand := mdctCoeffs[offset : offset+n : offset+n]
		sum := noFMA32Add(float32(1e-27), celtInnerProdF32LibopusOrder(coeffBand))
		bandE[band] = celtEner(opusmath.SqrtF32(sum))
		offset += n
	}
}

func applyLFELinearBandEClamp(bandE []celtEner, nbBands, channels int) {
	if nbBands <= 2 || channels <= 0 {
		return
	}
	for c := range channels {
		base := c * nbBands
		if base+nbBands > len(bandE) {
			return
		}
		limit := float32(lfeBandClamp) * float32(bandE[base])
		for band := 2; band < nbBands; band++ {
			idx := base + band
			v := float32(bandE[idx])
			if v > limit {
				v = limit
			}
			if v < float32(celtFloatEpsilon) {
				v = float32(celtFloatEpsilon)
			}
			bandE[idx] = celtEner(v)
		}
	}
}

// NormalizeBandsToArrayInto normalizes bands into a provided contiguous array.
// This is the zero-allocation version - caller provides norm and bandE buffers.
//
// Parameters:
//   - mdctCoeffs: MDCT coefficients to normalize
//   - nbBands: number of bands
//   - frameSize: frame size in samples
//   - norm: output buffer (length >= frameSize)
//   - bandE: scratch buffer for celt_ener band amplitudes (length >= nbBands)
//
// Reference: libopus celt/bands.c normalise_bands() (float path, lines 172-187)
func NormalizeBandsToArrayInto(mdctCoeffs []float32, nbBands, frameSize int, norm []celtNorm, bandE []celtEner) {
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if frameSize <= 0 {
		return
	}

	// Compute linear band amplitudes directly from MDCT coefficients
	ComputeLinearBandAmplitudesInto(mdctCoeffs, nbBands, frameSize, bandE)
	normalizeBandsWithBandEInto(mdctCoeffs, nbBands, frameSize, norm, bandE)
}

// NormalizeBandsToArrayIntoF32 normalizes float-build MDCT coefficients into a
// provided contiguous array.
func NormalizeBandsToArrayIntoF32(mdctCoeffs []float32, nbBands, frameSize int, norm []celtNorm, bandE []celtEner) {
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if frameSize <= 0 {
		return
	}

	ComputeLinearBandAmplitudesIntoF32(mdctCoeffs, nbBands, frameSize, bandE)
	normalizeBandsWithBandEIntoF32(mdctCoeffs, nbBands, frameSize, norm, bandE)
}

// NormalizeBandsToArrayIntoF32BinMul normalizes float-build MDCT coefficients
// using an explicit bin multiplier M=1<<LM. Band edges are eBands[i]*M, matching
// compute_band_energies()/normalise_bands(). The 48 kHz path uses M=frameSize/120
// via NormalizeBandsToArrayIntoF32; the native 96 kHz HD mode uses M=1<<LM.
func NormalizeBandsToArrayIntoF32BinMul(mdctCoeffs []float32, nbBands, binMul int, norm []celtNorm, bandE []celtEner) {
	if nbBands <= 0 || nbBands > MaxBands || binMul <= 0 {
		return
	}
	computeLinearBandAmplitudesIntoF32BinMul(mdctCoeffs, nbBands, binMul, bandE)
	normalizeBandsWithBandEIntoF32BinMul(mdctCoeffs, nbBands, binMul, norm, bandE)
}

// normalizeBandsToArrayIntoF32BinMulWidths is the band-width-parameterized form
// used by a non-standard Opus Custom mode whose per-mode band widths differ from
// the static eBandWidths table.
func normalizeBandsToArrayIntoF32BinMulWidths(mdctCoeffs []float32, nbBands, binMul int, norm []celtNorm, bandE []celtEner, widths []int, maxBands int) {
	if nbBands <= 0 || binMul <= 0 {
		return
	}
	computeLinearBandAmplitudesIntoF32BinMulWidths(mdctCoeffs, nbBands, binMul, bandE, widths, maxBands)
	normalizeBandsWithBandEIntoF32BinMulWidths(mdctCoeffs, nbBands, binMul, norm, bandE, widths)
}

func normalizeBandsWithBandEInto(mdctCoeffs []float32, nbBands, frameSize int, norm []celtNorm, bandE []celtEner) {
	offset := 0
	for band := range nbBands {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(mdctCoeffs) {
			offset += n
			continue
		}

		// Get linear amplitude for this band
		// Reference: libopus bands.c line 182:
		//   opus_val16 g = 1.f/(1e-27f+bandE[i+c*m->nbEBands])
		amplitude := float32(bandE[band])
		if amplitude < float32(1e-27) {
			amplitude = float32(1e-27)
		}
		g := float32(1.0) / amplitude

		// Normalize: X[j] = freq[j] * g
		// Reference: libopus bands.c lines 183-184:
		//   for (j=M*eBands[i];j<M*eBands[i+1];j++)
		//      X[j+c*N] = freq[j+c*N]*g;
		for i := range n {
			// Match libopus float path: freq and gains are float.
			norm[offset+i] = celtNorm(mdctCoeffs[offset+i] * g)
		}
		offset += n
	}
}

func normalizeBandsWithBandEIntoF32(mdctCoeffs []float32, nbBands, frameSize int, norm []celtNorm, bandE []celtEner) {
	normalizeBandsWithBandEIntoF32BinMul(mdctCoeffs, nbBands, frameSize/Overlap, norm, bandE)
}

func normalizeBandsWithBandEIntoF32BinMul(mdctCoeffs []float32, nbBands, binMul int, norm []celtNorm, bandE []celtEner) {
	normalizeBandsWithBandEIntoF32BinMulWidths(mdctCoeffs, nbBands, binMul, norm, bandE, eBandWidths[:])
}

func normalizeBandsWithBandEIntoF32BinMulWidths(mdctCoeffs []float32, nbBands, binMul int, norm []celtNorm, bandE []celtEner, widths []int) {
	if binMul <= 0 {
		return
	}
	if len(widths) < 1 {
		widths = eBandWidths[:]
	}
	offset := 0
	for band := 0; band < nbBands && band < len(widths); band++ {
		n := widths[band] * binMul
		if n <= 0 {
			continue
		}
		if offset+n > len(mdctCoeffs) {
			offset += n
			continue
		}

		amplitude := float32(bandE[band])
		if amplitude < float32(1e-27) {
			amplitude = float32(1e-27)
		}
		g := float32(1.0) / amplitude

		for i := range n {
			norm[offset+i] = celtNorm(mdctCoeffs[offset+i] * g)
		}
		offset += n
	}
}

// NormalizeBandsToArray normalizes bands into a single contiguous array (length = frameSize).
// This mirrors libopus normalise_bands(): divide by the per-band LINEAR amplitude.
//
// CRITICAL FIX: This function now uses LINEAR band amplitudes computed directly from MDCT
// coefficients, NOT log-domain energies converted back to linear. The log-domain roundtrip
// was introducing quantization errors that corrupted PVQ encoding.
//
// The energies parameter is now IGNORED - we compute linear amplitudes directly from mdctCoeffs.
// This matches libopus which calls compute_band_energies() to get linear bandE, then uses
// that directly in normalise_bands().
//
// Reference: libopus celt/bands.c normalise_bands() (float path, lines 172-187)
func (e *Encoder) NormalizeBandsToArray(mdctCoeffs []float32, energies []celtGLog, nbBands, frameSize int) []celtNorm {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil
	}
	if frameSize <= 0 {
		return nil
	}

	// Use scratch buffers (NormalizeBandsToArrayInto writes all norm elements).
	norm := ensureNormSliceNoClear(&e.scratch.normL, frameSize)
	bandE := ensureEnerSlice(&e.scratch.bandE, nbBands)

	NormalizeBandsToArrayInto(mdctCoeffs, nbBands, frameSize, norm, bandE)

	return norm
}

// vectorToPulses converts a normalized float vector to an integer pulse vector.
// The result has L1 norm (sum of absolute values) equal to k.
// This is the encoder's inverse of decoder's pulse-to-vector reconstruction.
//
// Parameters:
//   - shape: normalized float vector (should have unit L2 norm)
//   - k: target L1 norm (number of pulses)
//
// Returns: integer pulse vector where sum(|pulses[i]|) == k
//
// Algorithm:
// 1. Compute L1 norm of shape
// 2. Scale shape so L1 norm = k
// 3. Round each component to nearest integer
// 4. Distribute remaining pulses to minimize distortion
//
// Reference: libopus celt/vq.c alg_quant()
func vectorToPulses(shape []CeltNorm, k int) []int {
	n := len(shape)
	if n == 0 || k <= 0 {
		return make([]int, n)
	}

	pulses := make([]int, n)

	// Compute L1 norm of shape
	var l1norm float32
	for _, x := range shape {
		v := float32(x)
		if v < 0 {
			v = -v
		}
		l1norm += v
	}

	// Handle degenerate case
	if l1norm < 1e-15 {
		// Put all pulses in first position
		pulses[0] = k
		return pulses
	}

	// Scale factor to make L1 norm = k
	scale := float32(k) / l1norm

	// Scaled values and track rounding errors
	type errorEntry struct {
		idx   int
		error float32 // Error from rounding (positive = rounded down too much)
		sign  int     // Sign of the original value
	}
	errors := make([]errorEntry, n)

	currentL1 := 0
	for i, x := range shape {
		scaled := float32(x) * scale
		sign := 1
		if scaled < 0 {
			sign = -1
			scaled = -scaled
		}

		// Round to nearest integer
		rounded := max(int(scaled+0.5), 0)

		pulses[i] = sign * rounded
		currentL1 += rounded

		// Track error: how much we lost by rounding
		// Positive error = we rounded down (want to add pulse)
		// Negative error = we rounded up (want to remove pulse)
		error := scaled - float32(rounded)
		errors[i] = errorEntry{idx: i, error: error, sign: sign}
	}

	// Distribute remaining pulses to minimize distortion
	remaining := k - currentL1

	// While we need to add pulses
	for remaining > 0 {
		// Find position with largest positive error (rounded down the most)
		bestIdx := -1
		bestError := float32(-1.0)
		for i, e := range errors {
			if e.error > bestError {
				bestError = e.error
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			// No good candidate, just add to first position
			bestIdx = 0
		}

		// Add pulse with correct sign
		if pulses[bestIdx] >= 0 {
			pulses[bestIdx]++
		} else {
			pulses[bestIdx]--
		}
		errors[bestIdx].error -= 1.0
		remaining--
	}

	// While we need to remove pulses
	for remaining < 0 {
		// Find position with most negative error (rounded up the most)
		bestIdx := -1
		bestError := float32(1.0)
		for i, e := range errors {
			absPulse := pulses[i]
			if absPulse < 0 {
				absPulse = -absPulse
			}
			if absPulse > 0 && e.error < bestError {
				bestError = e.error
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			// Find any position with pulses
			for i := range n {
				if pulses[i] != 0 {
					bestIdx = i
					break
				}
			}
		}

		if bestIdx < 0 {
			break // No pulses to remove
		}

		// Remove pulse with correct sign
		if pulses[bestIdx] > 0 {
			pulses[bestIdx]--
		} else if pulses[bestIdx] < 0 {
			pulses[bestIdx]++
		}
		errors[bestIdx].error += 1.0
		remaining++
	}

	return pulses
}

// bitsToKEncode converts allocated bits to pulse count for encoding.
// This mirrors the decoder's bitsToK function.
//
// Parameters:
//   - bits: number of bits allocated to this band
//   - n: band width (number of MDCT bins)
//
// Returns: number of pulses K for PVQ coding.
func bitsToKEncode(bits, n int) int {
	// Use the same algorithm as decoder's bitsToK
	return bitsToK(bits, n)
}

// EncodeBandPVQ encodes a normalized band shape using PVQ.
// k is the number of pulses (determined by bit allocation via bitsToKEncode).
//
// Parameters:
//   - shape: normalized band shape (unit L2 norm)
//   - n: band width (number of MDCT bins)
//   - k: number of pulses
//
// The encoded data consists of a single PVQ index encoded uniformly
// with V(n,k) possible values.
//
// Reference: libopus celt/bands.c quant_band()
func (e *Encoder) EncodeBandPVQ(shape []CeltNorm, n, k int) {
	if e.rangeEncoder == nil || k <= 0 || n <= 0 {
		return
	}

	// Ensure shape has correct length
	if len(shape) != n {
		// Pad or truncate
		newShape := make([]CeltNorm, n)
		copy(newShape, shape)
		shape = newShape
	}

	// Convert shape to pulses
	pulses := vectorToPulses(shape, k)

	// Encode to CWRS index using existing EncodePulses function
	index := EncodePulses(pulses, n, k)

	// Get the number of possible codewords
	vSize := PVQ_V(n, k)
	if vSize == 0 {
		return
	}

	// Encode index uniformly
	e.rangeEncoder.EncodeUniform(index, vSize)
}

// EncodeBands encodes all bands using PVQ.
// shapesL, shapesR: normalized band shapes for Left/Right (R is nil for mono)
// bandBits: bit allocation per band from ComputeAllocation
// nbBands: number of bands
// frameSize: frame size in samples (120, 240, 480, 960)
//
// For each band:
// - If bits <= 0: skip (band will be folded by decoder)
// - Otherwise: compute k from bits and encode via EncodeBandPVQ
// - For stereo, bits are split between L and R (Dual Stereo)
//
// Reference: libopus celt/bands.c quant_all_bands()
func (e *Encoder) EncodeBands(shapesL, shapesR [][]CeltNorm, bandBits []int, nbBands, frameSize int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if len(shapesL) < nbBands || len(bandBits) < nbBands {
		return
	}

	stereo := shapesR != nil && len(shapesR) >= nbBands

	for band := range nbBands {
		bits := bandBits[band]

		// If no bits allocated, skip this band (decoder will fold from other bands)
		if bits <= 0 {
			continue
		}

		// Get band width
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		if stereo {
			// Dual stereo: split bits
			// Note: This allocation must match decoder's dual stereo split
			bitsL := bits / 2
			bitsR := bits - bitsL

			// Encode Left
			kL := bitsToKEncode(bitsL, n)
			if kL > 0 && len(shapesL[band]) > 0 {
				e.EncodeBandPVQ(shapesL[band], n, kL)
			}

			// Encode Right
			kR := bitsToKEncode(bitsR, n)
			if kR > 0 && len(shapesR[band]) > 0 {
				e.EncodeBandPVQ(shapesR[band], n, kR)
			}
		} else {
			// Mono
			k := bitsToKEncode(bits, n)
			if k <= 0 {
				continue
			}

			// Get shape for this band
			shape := shapesL[band]
			if len(shape) == 0 {
				continue
			}

			// Encode the band using PVQ
			e.EncodeBandPVQ(shape, n, k)
		}
	}
}

// EncodeBandsHybrid encodes bands for hybrid mode (starting from startBand).
// In hybrid mode, bands 0 to startBand-1 are handled by SILK.
// Only bands from startBand onwards are PVQ encoded.
//
// Reference: RFC 6716 Section 3.2 - Hybrid mode uses start_band=17 for CELT
func (e *Encoder) EncodeBandsHybrid(shapesL, shapesR [][]CeltNorm, bandBits []int, nbBands, frameSize, startBand int) {
	if e.rangeEncoder == nil {
		return
	}
	if nbBands <= 0 || nbBands > MaxBands {
		return
	}
	if len(shapesL) < nbBands || len(bandBits) < nbBands {
		return
	}

	stereo := shapesR != nil && len(shapesR) >= nbBands

	// Only encode bands from startBand onwards
	for band := startBand; band < nbBands; band++ {
		bits := bandBits[band]

		// If no bits allocated, skip this band (decoder will fold from other bands)
		if bits <= 0 {
			continue
		}

		// Get band width
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		if stereo {
			// Dual stereo: split bits
			bitsL := bits / 2
			bitsR := bits - bitsL

			// Encode Left
			kL := bitsToKEncode(bitsL, n)
			if kL > 0 && len(shapesL[band]) > 0 {
				e.EncodeBandPVQ(shapesL[band], n, kL)
			}

			// Encode Right
			kR := bitsToKEncode(bitsR, n)
			if kR > 0 && len(shapesR[band]) > 0 {
				e.EncodeBandPVQ(shapesR[band], n, kR)
			}
		} else {
			// Mono
			k := bitsToKEncode(bits, n)
			if k <= 0 {
				continue
			}

			// Get shape for this band
			shape := shapesL[band]
			if len(shape) == 0 {
				continue
			}

			// Encode the band using PVQ
			e.EncodeBandPVQ(shape, n, k)
		}
	}
}
