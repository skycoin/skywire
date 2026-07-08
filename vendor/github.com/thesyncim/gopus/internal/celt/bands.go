package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// Band processing orchestration for CELT decoding.
// This file contains the top-level band decoding loop that processes all
// frequency bands, applying PVQ decoding for coded bands and folding for
// uncoded bands, then denormalizing by energy.
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/bands.c quant_all_bands()

// DecodeBands decodes all frequency bands from the bitstream.
// energies: per-band energy values (from coarse + fine energy decoding)
// bandBits: bits allocated per band (from bit allocation)
// nbBands: number of bands to decode
// stereo: true if stereo mode
// frameSize: frame size in samples (120, 240, 480, 960)
// Returns: MDCT coefficients (denormalized) of length frameSize.
//
// The output is zero-padded to frameSize. Band coefficients fill bins 0 to totalBins-1,
// where totalBins = sum(ScaledBandWidth(i, frameSize) for i in 0..nbBands-1).
// Upper bins (totalBins to frameSize-1) remain zero, representing highest frequencies.
// This ensures IMDCT receives exactly frameSize coefficients, producing correct sample count.
func (d *Decoder) DecodeBands(
	energies []celtGLog,
	bandBits []int,
	nbBands int,
	stereo bool,
	frameSize int,
) []celtNorm {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil
	}
	if len(energies) < nbBands {
		return nil
	}
	if len(bandBits) < nbBands {
		return nil
	}

	// Calculate total bins from bands (for band processing)
	totalBins := 0
	for band := range nbBands {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	if totalBins <= 0 {
		return nil
	}

	// Use pre-allocated coeffs buffer from scratch space
	// Upper bins (totalBins to frameSize-1) stay zero (highest frequencies)
	// This ensures IMDCT(frameSize) produces 2*frameSize samples
	coeffsSize := frameSize
	if stereo {
		coeffsSize = frameSize * 2 // Double for stereo
	}
	coeffs := d.scratchBands.ensureCoeffs(coeffsSize)
	// Zero the buffer (required since we reuse it)
	for i := range coeffs {
		coeffs[i] = 0
	}

	// Track coded bands for folding
	var collapseMask uint32
	// Use pre-allocated band vectors slice from scratch space
	bandVectors := d.scratchBands.ensureBandVectors(nbBands)

	offset := 0
	for band := range nbBands {
		n := ScaledBandWidth(band, frameSize) // Band width in MDCT bins
		if n <= 0 {
			continue
		}

		// Convert bits to pulse count
		k := bitsToK(bandBits[band], n)

		// Get pre-allocated storage for this band's shape vector
		shape := d.scratchBands.getBandStorage(band, n)
		if k > 0 {
			// Decode PVQ vector for this band into the pre-allocated buffer.
			d.decodePVQNormInto(band, n, k, shape)
			UpdateCollapseMask(&collapseMask, band)
		} else {
			// No pulses - fold from lower band into pre-allocated buffer
			srcBand := FindFoldSource(band, collapseMask, nil)
			if srcBand >= 0 && bandVectors[srcBand] != nil {
				d.foldBandNormInto(bandVectors[srcBand], n, shape)
			} else {
				// No source - generate noise into pre-allocated buffer
				d.foldBandNormInto(nil, n, shape)
			}
		}

		// Store vector reference for potential folding by later bands
		bandVectors[band] = shape

		gain := denormalizeBandGain(energies, band)

		// Apply gain to shape and write to output.
		out := coeffs[offset : offset+n]
		for i := range n {
			out[i] = celtNorm(float32(shape[i]) * gain)
		}

		offset += n
	}

	// Store collapse mask in decoder state (for anti-collapse in next frame)
	d.collapseMask = collapseMask

	return coeffs
}

// DecodeBandsStereo decodes all frequency bands in stereo mode.
// energiesL/R: per-band energies for left/right channels
// bandBits: bits allocated per band
// nbBands: number of bands
// frameSize: frame size in samples
// intensity: intensity stereo start band (-1 if not used)
// Returns: left and right channel MDCT coefficients, each of length frameSize.
//
// In stereo mode, bands can use:
// 1. Dual stereo: separate PVQ vectors for L and R
// 2. Mid-side: decode mid/side and rotate to L/R
// 3. Intensity: copy mono to both with sign flag
//
// Output is zero-padded to frameSize per channel for correct IMDCT operation.
//
// Reference: libopus celt/bands.c quant_all_bands() stereo path
func (d *Decoder) DecodeBandsStereo(
	energiesL, energiesR []celtGLog,
	bandBits []int,
	nbBands int,
	frameSize int,
	intensity int,
) (left, right []celtNorm) {
	if nbBands <= 0 || nbBands > MaxBands {
		return nil, nil
	}

	// Calculate total bins from bands (for band processing)
	totalBins := 0
	for band := range nbBands {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	if totalBins <= 0 {
		return nil, nil
	}

	// Use pre-allocated buffers from scratch space
	// Upper bins (totalBins to frameSize-1) stay zero (highest frequencies)
	left = d.scratchBands.ensureCoeffs(frameSize * 2)[:frameSize]
	right = left[frameSize : frameSize*2]
	// Zero the buffers (required since we reuse them)
	for i := range left {
		left[i] = 0
	}
	for i := range right {
		right[i] = 0
	}

	// Track coded bands for folding
	var collapseMask uint32
	// Use pre-allocated band vectors slices from scratch space
	bandVectorsL, bandVectorsR := d.scratchBands.ensureBandVectorsStereo(nbBands)

	offset := 0
	for band := range nbBands {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		// Convert bits to pulse count
		k := bitsToK(bandBits[band], n)

		// Get pre-allocated storage for this band's shape vectors
		shapeL := d.scratchBands.getBandStorageL(band, n)
		shapeR := d.scratchBands.getBandStorageR(band, n)

		if band >= intensity && intensity >= 0 {
			// Intensity stereo: decode mono and duplicate with sign
			if k > 0 {
				// Use pvqNorm as temporary for mid shape
				shapeMid := d.scratchBands.ensurePVQNorm(n)
				d.decodePVQNormInto(band, n, k, shapeMid)
				d.decodeIntensityStereoNormInto(shapeMid, shapeL, shapeR)
				UpdateCollapseMask(&collapseMask, band)
			} else {
				// Fold from lower band
				srcBand := FindFoldSource(band, collapseMask, nil)
				var src []celtNorm
				if srcBand >= 0 {
					src = bandVectorsL[srcBand]
				}
				// Use foldResult as temporary for mid shape
				shapeMid := d.scratchBands.ensureFoldResult(n)
				d.foldBandNormInto(src, n, shapeMid)
				copy(shapeL, shapeMid)
				copy(shapeR, shapeMid)
			}
		} else if k > 0 {
			// Mid-side or dual stereo
			// For simplicity, use mid-side when bits are allocated
			// Decode mid shape
			kMid := max(k/2, 1)
			kSide := k - kMid

			// Use pvqNorm as temporary for mid shape
			shapeMid := d.scratchBands.ensurePVQNorm(n)
			d.decodePVQNormInto(band, n, kMid, shapeMid)

			// Use foldResult as temporary for side shape
			shapeSide := d.scratchBands.ensureFoldResult(n)
			if kSide > 0 {
				d.decodePVQNormInto(band, n, kSide, shapeSide)
			} else {
				for i := range n {
					shapeSide[i] = 0
				}
			}

			// Decode theta for M/S mixing
			itheta := d.DecodeStereoTheta(8) // 8 quantization steps
			midGain, sideGain := ThetaToGains(itheta, 8)

			// Apply rotation directly into pre-allocated buffers
			applyMidSideRotationNormInto(shapeMid, shapeSide, midGain, sideGain, shapeL, shapeR)

			UpdateCollapseMask(&collapseMask, band)
		} else {
			// Fold from lower band into pre-allocated buffers
			srcBand := FindFoldSource(band, collapseMask, nil)
			var srcL, srcR []celtNorm
			if srcBand >= 0 {
				srcL = bandVectorsL[srcBand]
				srcR = bandVectorsR[srcBand]
			}
			d.foldBandNormInto(srcL, n, shapeL)
			d.foldBandNormInto(srcR, n, shapeR)
		}

		// Store vector references for folding
		bandVectorsL[band] = shapeL
		bandVectorsR[band] = shapeR

		gainL := denormalizeBandGain(energiesL, band)
		gainR := denormalizeBandGain(energiesR, band)

		leftOut := left[offset : offset+n]
		rightOut := right[offset : offset+n]
		for i := range n {
			leftOut[i] = celtNorm(float32(shapeL[i]) * gainL)
			rightOut[i] = celtNorm(float32(shapeR[i]) * gainR)
		}

		offset += n
	}

	d.collapseMask = collapseMask

	return left, right
}

// bitsToK computes the number of PVQ pulses from bit allocation.
// bits: number of bits allocated to this band in Q3
// n: band width (number of MDCT bins)
// Returns: number of pulses K for PVQ coding.
//
// This mirrors libopus bits2pulses() and get_pulses() using a cached
// PVQ rate table for the given band width.
//
// Reference: libopus celt/rate.h bits2pulses() / get_pulses()
func bitsToK(bits, n int) int {
	if bits <= 0 || n <= 0 {
		return 0
	}
	band, lm, ok := bandFromWidth(n)
	if !ok {
		return 0
	}
	q := bitsToPulses(band, lm, bits)
	return getPulses(q)
}

func bandFromWidth(width int) (band int, lm int, ok bool) {
	if width <= 0 {
		return 0, 0, false
	}
	// Mapping preserves prior lookup behavior from init-built tables:
	// first match wins with lm ascending.
	switch width {
	case 1:
		return 0, 0, true
	case 2:
		return 9, 0, true
	case 4:
		return 11, 0, true
	case 8:
		return 14, 0, true
	case 10:
		return 16, 0, true
	case 12:
		return 17, 0, true
	case 16:
		return 18, 0, true
	case 20:
		return 16, 1, true
	case 21:
		return 19, 0, true
	case 24:
		return 17, 1, true
	case 26:
		return 20, 0, true
	case 32:
		return 18, 1, true
	case 40:
		return 16, 2, true
	case 42:
		return 19, 1, true
	case 48:
		return 17, 2, true
	case 52:
		return 20, 1, true
	case 64:
		return 18, 2, true
	case 80:
		return 16, 3, true
	case 84:
		return 19, 2, true
	case 96:
		return 17, 3, true
	case 104:
		return 20, 2, true
	case 128:
		return 18, 3, true
	case 168:
		return 19, 3, true
	case 208:
		return 20, 3, true
	}
	for lm = 0; lm <= 3; lm++ {
		for band = range MaxBands {
			if (EBands[band+1]-EBands[band])<<lm == width {
				return band, lm, true
			}
		}
	}
	return 0, 0, false
}

// kToBits computes the approximate bits needed to code K pulses in N dimensions.
// k: number of pulses
// n: number of dimensions
// Returns: approximate bits needed.
//
// Reference: libopus celt/rate.c pulses2bits()
func kToBits(k, n int) int {
	if k <= 0 {
		return 0
	}
	if n <= 0 {
		return 0
	}

	// For small values, use exact codebook size.
	if k <= 8 && n <= 16 {
		v := PVQ_V(n, k)
		if v <= 1 {
			return 0
		}
		return ilog2(int(v - 1))
	}

	// Approximate for larger values to avoid PVQ_V overflow.
	// bits ~= n * log2(1 + 2k/n) + k (sign bits)
	bits := float32(n)*opusmath.Log2F32(1.0+2.0*float32(k)/float32(n)) + float32(k)
	if bits < 0 {
		return 0
	}
	return int(bits + 0.5)
}

// ilog2 returns floor(log2(x)) for x > 0, or 0 for x <= 0.
func ilog2(x int) int {
	if x <= 0 {
		return 0
	}
	n := 0
	for x > 1 {
		x >>= 1
		n++
	}
	return n
}

// DenormalizeBand scales a normalized band vector by its energy.
// shape: normalized vector (unit L2 norm)
// energy: band energy in log2 units (1 = 6 dB)
// Returns: denormalized MDCT coefficients.
//
// This matches libopus celt/bands.c denormalise_bands().
func DenormalizeBand(shape []celtNorm, energy celtGLog) []celtNorm {
	if len(shape) == 0 {
		return nil
	}

	gain := denormalizeEnergyGain(energy)
	result := make([]celtNorm, len(shape))
	for i, x := range shape {
		result[i] = celtNorm(float32(x) * gain)
	}
	return result
}

func denormalizeNormCoeffsDownsample(coeffs []celtNorm, energies []celtGLog, nbBands, frameSize, downsample int) {
	if len(coeffs) == 0 || len(energies) == 0 || nbBands <= 0 || frameSize <= 0 {
		return
	}
	coeffsLen := min(len(coeffs), frameSize)
	scaleWidth := frameSize / Overlap
	offset := 0
	for band := range nbBands {
		width := EBands[band+1] - EBands[band]
		if scaleWidth > 0 {
			width *= scaleWidth
		}
		if width <= 0 {
			continue
		}
		end := offset + width
		if end > coeffsLen {
			end = coeffsLen
		}
		if end > offset {
			gain := denormalizeBandGain(energies, band)
			for i := offset; i < end; i++ {
				coeffs[i] = celtNorm(float32(coeffs[i]) * gain)
			}
		}
		offset += width
	}
	clearDenormalizedDownsampleTailNorm(coeffs, nbBands, scaleWidth, downsample, EBands[:])
}

func clearDenormalizedDownsampleTailNorm(coeffs []celtNorm, nbBands, scaleWidth, downsample int, edges []int) {
	if len(coeffs) == 0 || nbBands <= 0 || scaleWidth <= 0 || len(edges) < nbBands+1 {
		return
	}
	bound := edges[nbBands] * scaleWidth
	if downsample > 1 {
		if limit := len(coeffs) / downsample; bound > limit {
			bound = limit
		}
	}
	if bound < 0 {
		bound = 0
	}
	if bound < len(coeffs) {
		clear(coeffs[bound:])
	}
}

func denormalizeBandGain(energies []celtGLog, band int) float32 {
	e := float32(energies[band])
	if band < len(eMeans) {
		e += float32(eMeans[band])
	}
	if e > 32 {
		e = 32
	}
	return celtExp2(e)
}

func denormalizeEnergyGain(energy celtGLog) float32 {
	e := float32(energy)
	if e > 32 {
		e = 32
	}
	return celtExp2(e)
}

// ComputeBandEnergy computes the per-band log2 amplitude.
// coeffs: MDCT coefficients for the band
// Returns: log2(sqrt(sum(x^2))) with libopus epsilon
func ComputeBandEnergy(coeffs []celtNorm) celtGLog {
	if len(coeffs) == 0 {
		return celtGLog(float32(0.5) * celtLog2(float32(1e-27)))
	}

	sumSq := float32(1e-27)
	for _, x := range coeffs {
		v := float32(x)
		sumSq += v * v
	}

	// log2(sqrt(sumSq)) = log2(amp) with libopus FLOAT_APPROX log2.
	amp := opusmath.SqrtF32(sumSq)
	return celtGLog(celtLog2(amp))
}
