package silk

// encodePulses encodes quantization indices of excitation for the entire frame.
// This matches libopus silk_encode_pulses() - encoding ALL pulses at once.
// Per RFC 6716 Section 4.2.7.8.
func (e *Encoder) encodePulses(pulses []int8, signalType, quantOffset int) {
	frameLength := len(pulses)

	// Calculate number of shell blocks (iter)
	iter := frameLength >> log2ShellCodecFrameLength
	if iter*shellCodecFrameLength < frameLength {
		iter++
	}

	// Pad pulses if needed - use scratch buffer
	shellLen := iter * shellCodecFrameLength
	paddedPulses := ensureInt8Slice(&e.scratchPaddedPulses, shellLen)
	for i := range shellLen {
		paddedPulses[i] = 0 // Clear
	}
	for i := 0; i < frameLength && i < len(paddedPulses); i++ {
		paddedPulses[i] = pulses[i]
	}

	// Take absolute value of pulses - use scratch buffer
	absPulses := ensureInt32Slice(&e.scratchAbsPulses, shellLen)
	for i := range paddedPulses {
		p := int32(paddedPulses[i])
		if p < 0 {
			p = -p
		}
		absPulses[i] = p
	}

	// Calculate sum pulses per shell block with overflow handling - use scratch buffers
	sumPulses := ensureInt32Slice(&e.scratchSumPulses, iter)
	nRshifts := ensureInt32Slice(&e.scratchNRshifts, iter)

	var pulsesComb [8]int32
	absOffset := 0
	for i := 0; i < iter; i++ {
		nRshifts[i] = 0
		for {
			scaleDown := combineAndCheck(pulsesComb[:], absPulses[absOffset:], int32(silk_max_pulses_table[0]), 8)
			scaleDown += combineAndCheck(pulsesComb[:], pulsesComb[:], int32(silk_max_pulses_table[1]), 4)
			scaleDown += combineAndCheck(pulsesComb[:], pulsesComb[:], int32(silk_max_pulses_table[2]), 2)
			scaleDown += combineAndCheck(pulsesComb[:], pulsesComb[:], int32(silk_max_pulses_table[3]), 1)

			if scaleDown != 0 {
				nRshifts[i]++
				for k := range shellCodecFrameLength {
					absPulses[absOffset+k] >>= 1
				}
				continue
			}

			sumPulses[i] = pulsesComb[0]
			break
		}
		absOffset += shellCodecFrameLength
	}

	// Select rate level that minimizes total bits (libopus table-based selection).
	rateLevelIndex := 0
	minSumBits := int32(1<<31 - 1)
	rateBits := silk_rate_levels_BITS_Q5[signalType>>1]
	for k := range nRateLevels - 1 {
		sumBits := int32(rateBits[k])
		nBitsPtr := silk_pulses_per_block_BITS_Q5[k]
		for i := 0; i < iter; i++ {
			if nRshifts[i] > 0 {
				sumBits += int32(nBitsPtr[silkMaxPulses+1])
			} else {
				sumBits += int32(nBitsPtr[int(sumPulses[i])])
			}
		}
		if sumBits < minSumBits {
			minSumBits = sumBits
			rateLevelIndex = k
		}
	}

	// Encode rate level using libopus tables
	// signalType>>1 maps: 0,1 -> 0 (unvoiced/inactive), 2,3 -> 1 (voiced)
	e.rangeEncoder.EncodeICDF(rateLevelIndex, silk_rate_levels_iCDF[signalType>>1], 8)

	// Encode sum-weighted pulses per shell block
	// Use rate-level-dependent ICDF table (matching decoder's silk_pulses_per_block_iCDF)
	pulseCountICDF := silk_pulses_per_block_iCDF[rateLevelIndex]
	for i := 0; i < iter; i++ {
		if nRshifts[i] == 0 {
			e.rangeEncoder.EncodeICDF(int(sumPulses[i]), pulseCountICDF, 8)
		} else {
			// Overflow: encode special marker, then nRshifts-1 markers, then final sum
			e.rangeEncoder.EncodeICDF(silkMaxPulses+1, pulseCountICDF, 8)
			for k := int32(0); k < nRshifts[i]-1; k++ {
				e.rangeEncoder.EncodeICDF(silkMaxPulses+1, silk_pulses_per_block_iCDF[nRateLevels-1], 8)
			}
			e.rangeEncoder.EncodeICDF(int(sumPulses[i]), silk_pulses_per_block_iCDF[nRateLevels-1], 8)
		}
	}

	// Shell encoding (binary splits)
	for i := 0; i < iter; i++ {
		if sumPulses[i] > 0 {
			offset := i * shellCodecFrameLength
			e.shellEncoder(absPulses[offset : offset+shellCodecFrameLength])
		}
	}

	// LSB encoding for overflow cases using libopus table
	for i := 0; i < iter; i++ {
		if nRshifts[i] > 0 {
			offset := i * shellCodecFrameLength
			nLS := nRshifts[i] - 1
			for k := range shellCodecFrameLength {
				absQ := int32(paddedPulses[offset+k])
				if absQ < 0 {
					absQ = -absQ
				}
				for j := nLS; j > 0; j-- {
					bit := int((absQ >> j) & 1)
					e.rangeEncoder.EncodeICDF(bit, silk_lsb_iCDF, 8)
				}
				bit := int(absQ & 1)
				e.rangeEncoder.EncodeICDF(bit, silk_lsb_iCDF, 8)
			}
		}
	}

	// Encode signs
	e.encodeSigns(paddedPulses, frameLength, signalType, quantOffset, sumPulses)
}

// shellEncoder encodes 16 pulses using hierarchical binary splits.
// Matches libopus silk_shell_encoder() exactly.
// Uses scratch arrays from encoder to avoid allocations.
func (e *Encoder) shellEncoder(pulses []int32) {
	// Use scratch arrays (fixed size, no allocation)
	pulses1 := &e.scratchShellPulses1
	pulses2 := &e.scratchShellPulses2
	pulses3 := &e.scratchShellPulses3
	pulses4 := &e.scratchShellPulses4

	// Combine: 16 -> 8
	for k := range 8 {
		pulses1[k] = pulses[2*k] + pulses[2*k+1]
	}
	// Combine: 8 -> 4
	for k := range 4 {
		pulses2[k] = pulses1[2*k] + pulses1[2*k+1]
	}
	// Combine: 4 -> 2
	for k := range 2 {
		pulses3[k] = pulses2[2*k] + pulses2[2*k+1]
	}
	// Combine: 2 -> 1
	pulses4[0] = pulses3[0] + pulses3[1]

	// Encode splits using libopus shell tables
	e.encodeShellSplitLibopus(pulses3[0], pulses4[0], silk_shell_code_table3)

	e.encodeShellSplitLibopus(pulses2[0], pulses3[0], silk_shell_code_table2)

	e.encodeShellSplitLibopus(pulses1[0], pulses2[0], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[0], pulses1[0], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[2], pulses1[1], silk_shell_code_table0)

	e.encodeShellSplitLibopus(pulses1[2], pulses2[1], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[4], pulses1[2], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[6], pulses1[3], silk_shell_code_table0)

	e.encodeShellSplitLibopus(pulses2[2], pulses3[1], silk_shell_code_table2)

	e.encodeShellSplitLibopus(pulses1[4], pulses2[2], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[8], pulses1[4], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[10], pulses1[5], silk_shell_code_table0)

	e.encodeShellSplitLibopus(pulses1[6], pulses2[3], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[12], pulses1[6], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[14], pulses1[7], silk_shell_code_table0)
}

// encodeShellSplitLibopus encodes a binary split using libopus shell tables.
// pChild1: pulse count in first child subframe
// p: total pulse count in current subframe
// shellTable: the shell code table to use (e.g., silk_shell_code_table0)
func (e *Encoder) encodeShellSplitLibopus(pChild1, p int32, shellTable []uint8) {
	if p > 0 {
		// Get offset into table based on total pulse count
		offset := int(silk_shell_code_table_offsets[p])
		e.rangeEncoder.EncodeICDF(int(pChild1), shellTable[offset:], 8)
	}
}

// encodeSigns encodes signs for non-zero pulses.
// Matches libopus silk_encode_signs() exactly.
func (e *Encoder) encodeSigns(pulses []int8, frameLength, signalType, quantOffset int, sumPulses []int32) {
	// Build 2-element ICDF for sign encoding
	icdf := [2]uint8{}

	// Compute index into silk_sign_iCDF table
	// Per libopus: i = 7 * (quantOffsetType + (signalType << 1))
	baseIdx := 7 * (quantOffset + (signalType << 1))
	icdfPtr := silk_sign_iCDF[baseIdx:]

	// Process each shell block
	iter := (frameLength + shellCodecFrameLength/2) >> log2ShellCodecFrameLength
	for i := range iter {
		p := sumPulses[i]
		if p > 0 {
			// Set icdf[0] based on sumPulses, clamped to [0, 6]
			pIdx := min(int(p&0x1F), 6)
			icdf[0] = icdfPtr[pIdx]

			// Encode sign for each non-zero pulse in this block
			offset := i * shellCodecFrameLength
			for j := range shellCodecFrameLength {
				idx := offset + j
				if idx >= frameLength {
					break
				}
				if pulses[idx] != 0 {
					// silk_enc_map: negative -> 0, positive -> 1
					// Actually per libopus: silk_enc_map(a) = ( silk_RSHIFT( (a), 15 ) + 1 )
					// For int8, if a < 0, RSHIFT by 15 gives -1, so result is 0
					// If a > 0, RSHIFT by 15 gives 0, so result is 1
					sign := 1
					if pulses[idx] < 0 {
						sign = 0
					}
					e.rangeEncoder.EncodeICDF(sign, icdf[:], 8)
				}
			}
		}
	}
}

func combineAndCheck(pulsesComb []int32, pulsesIn []int32, maxPulses int32, length int) int32 {
	for k := range length {
		sum := pulsesIn[2*k] + pulsesIn[2*k+1]
		if sum > maxPulses {
			return 1
		}
		pulsesComb[k] = sum
	}
	return 0
}

// computeExcitation computes the LPC residual (excitation signal).
// excitation[n] = input[n] - sum(lpc[k] * input[n-k-1])
//
// Note: The excitation is computed WITHOUT gain scaling. The gain is encoded
// separately and applied during decoding. Dividing by gain here would cause
// the decoder to apply gain twice (once during excitation reconstruction,
// once during synthesis), resulting in incorrect signal levels.
func (e *Encoder) computeExcitation(pcm []float32, lpcQ12 []int16, gain float32) []int32 {
	n := len(pcm)
	order := len(lpcQ12)
	excitation := make([]int32, n)

	for i := range n {
		// Compute LPC prediction
		var prediction float32
		for k := 0; k < order && i-k-1 >= 0; k++ {
			prediction += float32(lpcQ12[k]) * pcm[i-k-1] / 4096.0
		}

		// Residual = input - prediction
		residual := pcm[i] - prediction

		// Quantize residual to integer (do NOT divide by gain - decoder applies gain)
		excitation[i] = roundF32HalfAwayFromZeroToInt32(residual)
	}

	return excitation
}
