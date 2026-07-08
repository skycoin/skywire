package celt

// Band folding for uncoded bands in CELT.
// When a band receives zero bit allocation (k=0 pulses), its shape is
// reconstructed by "folding" from a lower coded band with pseudo-random
// sign variations. This provides perceptually acceptable noise fill.
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/bands.c

// FoldBand generates a normalized celt_norm vector by folding from a lower band.
func FoldBand(lowband []celtNorm, n int, seed *uint32) []celtNorm {
	if n <= 0 {
		return nil
	}

	result := make([]celtNorm, n)

	if len(lowband) == 0 {
		for i := range n {
			*seed = *seed*1664525 + 1013904223
			result[i] = celtNorm(float32(int32(*seed)) / float32(1<<31))
		}
	} else {
		for i := range n {
			sign := float32(1.0)
			if *seed&0x8000 != 0 {
				sign = -1.0
			}
			*seed = *seed*1664525 + 1013904223
			result[i] = celtNorm(sign * lowband[i%len(lowband)])
		}
	}

	normalizeNormVectorInPlace(result)
	return result
}

func (d *Decoder) foldBandNormInto(lowband []celtNorm, n int, dst []celtNorm) {
	if n <= 0 || len(dst) < n {
		return
	}

	if len(lowband) == 0 {
		// No source band available - generate pseudo-random noise
		// Uses LCG (Linear Congruential Generator) matching libopus
		for i := range n {
			d.rng = d.rng*1664525 + 1013904223 // LCG constants
			// Convert to signed float in approximately [-1, 1]
			dst[i] = celtNorm(float32(int32(d.rng)) / float32(1<<31))
		}
	} else {
		// Copy from lower band with pseudo-random sign flips
		// The sign is determined by bit 15 of the RNG state
		for i := range n {
			// Determine sign from current seed
			sign := float32(1.0)
			if d.rng&0x8000 != 0 {
				sign = -1.0
			}
			// Advance RNG
			d.rng = d.rng*1664525 + 1013904223

			// Copy from lowband with wrapping if target is larger
			dst[i] = celtNorm(sign * lowband[i%len(lowband)])
		}
	}

	// Normalize to unit energy in place
	normalizeNormVectorInPlace(dst[:n])
}

func normalizeNormVectorInPlace(v []celtNorm) {
	if len(v) == 0 {
		return
	}

	// libopus renormalise_vector() (celt/vq.c) computes
	//   E = EPSILON + celt_inner_prod_norm(X, X, N)
	// where celt_inner_prod_norm == celt_inner_prod, whose accumulation order is
	// architecture-specific: the pinned arm64 build (OPUS_ARM_PRESUME_NEON_INTR)
	// uses celt_inner_prod_neon's 4-lane FMA reduction, while x86 uses its SSE
	// variant. celtInnerProdLibopusOrder reproduces the matching tree per arch.
	// EPSILON (1e-15f) is added last, then the vector is scaled by 1/sqrt(E).
	energy := float32(1e-15) + celtInnerProdLibopusOrder(v, v)

	scale := celtRSqrt(energy)
	for i := range v {
		v[i] = celtNorm(v[i] * scale)
	}
}

// FindFoldSource finds the band to fold from for an uncoded band.
// targetBand: the band index that needs folding
// codedMask: bitmask of bands that have been coded (received pulses)
// bandWidths: array of band widths (from eBands table)
// Returns: index of source band to fold from, or -1 if none available.
//
// The algorithm searches backwards from targetBand to find the most recent
// coded band that can serve as a reasonable source.
func FindFoldSource(targetBand int, codedMask uint32, bandWidths []int) int {
	if targetBand <= 0 || codedMask == 0 {
		return -1
	}

	// Search backwards for a coded band
	for b := targetBand - 1; b >= 0; b-- {
		if codedMask&(1<<b) != 0 {
			// Found a coded band
			return b
		}
	}

	return -1 // No coded band found
}

// UpdateCollapseMask marks a band as having received pulses.
// mask: pointer to collapse mask (modified in place)
// band: band index to mark as coded
//
// The collapse mask tracks which bands received non-zero bit allocation.
// This is used for anti-collapse processing in transient frames.
func UpdateCollapseMask(mask *uint32, band int) {
	if band < 0 || band >= 32 {
		return
	}
	*mask |= 1 << band
}

// NeedsAntiCollapse checks if a band collapsed (no pulses in transient frame).
// mask: collapse mask from current frame
// band: band index to check
// Returns: true if the band collapsed and needs anti-collapse noise injection.
//
// Reference: RFC 6716 Section 4.3.5
func NeedsAntiCollapse(mask uint32, band int) bool {
	if band < 0 || band >= 32 {
		return false
	}
	// Band collapsed if it's not in the coded mask
	return mask&(1<<band) == 0
}

// IsBandCoded checks if a band was coded (received pulses).
// mask: collapse mask
// band: band index
// Returns: true if the band received non-zero bit allocation.
func IsBandCoded(mask uint32, band int) bool {
	if band < 0 || band >= 32 {
		return false
	}
	return mask&(1<<band) != 0
}

// ClearCollapseMask resets the collapse mask to zero.
func ClearCollapseMask(mask *uint32) {
	*mask = 0
}

// GetCodedBandCount returns the number of bands with pulses.
// mask: collapse mask
// Returns: count of coded bands.
func GetCodedBandCount(mask uint32) int {
	count := 0
	for mask != 0 {
		count += int(mask & 1)
		mask >>= 1
	}
	return count
}
