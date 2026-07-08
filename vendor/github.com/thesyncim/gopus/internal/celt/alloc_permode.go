package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// Per-mode bit-allocation core for a non-standard Opus Custom mode whose band
// layout differs from the static 21-band 48 kHz tables. These functions mirror
// cltComputeAllocationWithScratch / interpBits2Pulses / initCapsInto exactly,
// substituting the per-mode tables (band count, band widths, allocation matrix,
// caps, logN, edges) for the package globals. They are reached only when a
// per-mode custom layout is installed; the standard, family, hybrid and QEXT
// callers keep the globals-based functions verbatim.
//
// Reference: libopus celt/rate.c clt_compute_allocation() / interp_bits2pulses()
// / init_caps() driven by a custom CELTMode.

func initCapsIntoMode(caps []int32, nbBands, lm, channels int, pm *perModeTables) {
	if nbBands > len(caps) {
		nbBands = len(caps)
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	row := 2*lm + (channels - 1)
	for i := 0; i < nbBands; i++ {
		N := pm.eBandWidths[i] << lm
		idx := pm.nbEBands*row + i
		cap := int32(pm.cacheCaps[idx])
		caps[i] = (cap + 64) * int32(channels) * int32(N) >> 2
	}
}

func cltComputeAllocationWithScratchMode(start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int,
	rd *rangecoding.Decoder, scratch []int32, pm *perModeTables) int {
	lenBands := pm.nbEBands
	if end > lenBands {
		end = lenBands
	}
	if start < 0 {
		start = 0
	}

	if totalBitsQ3 < 0 {
		totalBitsQ3 = 0
	}

	skipStart := start
	skipRsv := 0
	if totalBitsQ3 >= 1<<bitRes {
		skipRsv = 1 << bitRes
		totalBitsQ3 -= skipRsv
	}

	intensityRsv := 0
	dualStereoRsv := 0
	if channels == 2 {
		intensityRsv = int(log2FracTable[end-start])
		if intensityRsv > totalBitsQ3 {
			intensityRsv = 0
		} else {
			totalBitsQ3 -= intensityRsv
			if totalBitsQ3 >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
				totalBitsQ3 -= dualStereoRsv
			}
		}
	}
	if len(scratch) < lenBands*5 {
		scratch = make([]int32, lenBands*5)
	}
	bits1 := scratch[:lenBands]
	bits2 := scratch[lenBands : 2*lenBands]
	thresh := scratch[2*lenBands : 3*lenBands]
	trimOffset := scratch[3*lenBands : 4*lenBands]
	bandScale := scratch[4*lenBands : 5*lenBands]

	channels32 := int32(channels)
	for j := start; j < end; j++ {
		width := int32(pm.eBandWidths[j])
		widthLM := width << lm
		bandScale[j] = channels32 * widthLM
		thresh[j] = max32(channels32<<bitRes, (3*widthLM<<bitRes)>>4)
		trimOffset[j] = int32(int64(channels*int(width)*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if widthLM == 1 {
			trimOffset[j] -= channels32 << bitRes
		}
	}

	lo := 1
	hi := len(pm.bandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := int32(0)
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			bitsj := (bandScale[idx] * int32(pm.bandAlloc[mid][idx])) >> 2
			if bitsj > 0 {
				bitsj = max32(0, bitsj+trimOffset[idx])
			}
			bitsj += offsets[idx]
			if bitsj >= thresh[idx] || done != 0 {
				done = 1
				psum += min32(bitsj, cap[idx])
			} else if bitsj >= channels32<<bitRes {
				psum += channels32 << bitRes
			}
		}
		if int(psum) > totalBitsQ3 {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	hi = lo
	lo--
	if lo < 0 {
		lo = 0
	}
	if hi < 0 {
		hi = 0
	}

	for j := start; j < end; j++ {
		bits1j := (bandScale[j] * int32(pm.bandAlloc[lo][j])) >> 2
		bits2j := cap[j]
		if hi < len(pm.bandAlloc) {
			bits2j = (bandScale[j] * int32(pm.bandAlloc[hi][j])) >> 2
		}
		if bits1j > 0 {
			bits1j = max32(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = max32(0, bits2j+trimOffset[j])
		}
		if lo > 0 {
			bits1j += offsets[j]
		}
		bits2j += offsets[j]
		if offsets[j] > 0 {
			skipStart = j
		}
		bits2j = max32(0, bits2j-bits1j)
		bits1[j] = bits1j
		bits2[j] = bits2j
	}

	codedBands := interpBits2PulsesMode(start, end, skipStart, bits1, bits2, thresh, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, rd, pm)

	return codedBands
}

// cltComputeAllocationWithScratchModeEncode is the ENCODE analog of
// cltComputeAllocationWithScratchMode: it mirrors cltComputeAllocationEncode but
// substitutes the per-mode band tables (width, allocation matrix, edges, logN)
// for the package globals. The standard, family, hybrid and QEXT encode callers
// keep cltComputeAllocationEncode verbatim.
//
// Reference: libopus celt/rate.c clt_compute_allocation() (encode side).
func cltComputeAllocationWithScratchModeEncode(re *rangecoding.Encoder, start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int, prev int, signalBandwidth int,
	scratch []int32, pm *perModeTables) int {
	lenBands := pm.nbEBands
	if end > lenBands {
		end = lenBands
	}
	if start < 0 {
		start = 0
	}
	if totalBitsQ3 < 0 {
		totalBitsQ3 = 0
	}

	skipStart := start
	skipRsv := 0
	if totalBitsQ3 >= 1<<bitRes {
		skipRsv = 1 << bitRes
		totalBitsQ3 -= skipRsv
	}

	intensityRsv := 0
	dualStereoRsv := 0
	if channels == 2 {
		intensityRsv = int(log2FracTable[end-start])
		if intensityRsv > totalBitsQ3 {
			intensityRsv = 0
		} else {
			totalBitsQ3 -= intensityRsv
			if totalBitsQ3 >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
				totalBitsQ3 -= dualStereoRsv
			}
		}
	}

	if len(scratch) < lenBands*4 {
		scratch = make([]int32, lenBands*4)
	}
	bits1 := scratch[:lenBands]
	bits2 := scratch[lenBands : 2*lenBands]
	thresh := scratch[2*lenBands : 3*lenBands]
	trimOffset := scratch[3*lenBands : 4*lenBands]

	channels32 := int32(channels)
	for j := start; j < end; j++ {
		width := int32(pm.eBandWidths[j])
		thresh[j] = max32(channels32<<bitRes, (3*(width<<lm)<<bitRes)>>4)
		trimOffset[j] = int32(int64(channels*int(width)*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if (width << lm) == 1 {
			trimOffset[j] -= channels32 << bitRes
		}
	}

	lo := 1
	hi := len(pm.bandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := int32(0)
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			width := int32(pm.eBandWidths[idx])
			bitsj := (channels32 * width * int32(pm.bandAlloc[mid][idx]) << lm) >> 2
			if bitsj > 0 {
				bitsj = max32(0, bitsj+trimOffset[idx])
			}
			bitsj += offsets[idx]
			if bitsj >= thresh[idx] || done != 0 {
				done = 1
				psum += min32(bitsj, cap[idx])
			} else if bitsj >= channels32<<bitRes {
				psum += channels32 << bitRes
			}
		}
		if int(psum) > totalBitsQ3 {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	hi = lo
	lo--
	if lo < 0 {
		lo = 0
	}
	if hi < 0 {
		hi = 0
	}

	for j := start; j < end; j++ {
		width := int32(pm.eBandWidths[j])
		bits1j := (channels32 * width * int32(pm.bandAlloc[lo][j]) << lm) >> 2
		bits2j := cap[j]
		if hi < len(pm.bandAlloc) {
			bits2j = (channels32 * width * int32(pm.bandAlloc[hi][j]) << lm) >> 2
		}
		if bits1j > 0 {
			bits1j = max32(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = max32(0, bits2j+trimOffset[j])
		}
		if lo > 0 {
			bits1j += offsets[j]
		}
		bits2j += offsets[j]
		if offsets[j] > 0 {
			skipStart = j
		}
		bits2j = max32(0, bits2j-bits1j)
		bits1[j] = bits1j
		bits2[j] = bits2j
	}

	codedBands := interpBits2PulsesModeEncode(re, start, end, skipStart, bits1, bits2, thresh, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, prev, signalBandwidth, pm)

	return codedBands
}

func interpBits2PulsesModeEncode(re *rangecoding.Encoder, start, end, skipStart int, bits1, bits2, thresh, cap []int32,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int32,
	channels, lm int, prev int, signalBandwidth int, pm *perModeTables) int {
	edges := pm.eBands
	widths := pm.eBandWidths
	logN := pm.logN

	allocFloor := int32(channels << bitRes)
	stereo := 0
	if channels > 1 {
		stereo = 1
	}
	if prev < 0 {
		prev = 0
	}
	if signalBandwidth < start {
		signalBandwidth = start
	}
	if signalBandwidth > end-1 {
		signalBandwidth = end - 1
	}
	logM := int32(lm << bitRes)
	lo := 0
	hi := 1 << allocSteps
	for range allocSteps {
		mid := (lo + hi) >> 1
		psum := int32(0)
		done := 0
		for j := end; j > start; j-- {
			idx := j - 1
			tmp := bits1[idx] + int32((int64(mid)*int64(bits2[idx]))>>allocSteps)
			if tmp >= thresh[idx] || done != 0 {
				done = 1
				psum += min32(tmp, cap[idx])
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		if int(psum) > total {
			hi = mid
		} else {
			lo = mid
		}
	}
	psum := int32(0)
	done := 0
	for j := end; j > start; j-- {
		idx := j - 1
		tmp := bits1[idx] + int32((int64(lo)*int64(bits2[idx]))>>allocSteps)
		if tmp < thresh[idx] && done == 0 {
			if tmp >= allocFloor {
				tmp = allocFloor
			} else {
				tmp = 0
			}
		} else {
			done = 1
		}
		tmp = min32(tmp, cap[idx])
		bits[idx] = tmp
		psum += tmp
	}

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}

		left := int32(total) - psum
		percoeff := celtUdiv32(left, int32(edges[codedBands]-edges[start]))
		left -= int32(edges[codedBands]-edges[start]) * percoeff
		rem := max32(left-int32(edges[j]-edges[start]), 0)
		bandWidth := int32(edges[codedBands] - edges[j])
		bandBits := bits[j] + percoeff*bandWidth + rem

		if bandBits >= max32(thresh[j], allocFloor+(1<<bitRes)) {
			depthThreshold := 0
			if codedBands > 17 {
				if j < prev {
					depthThreshold = 7
				} else {
					depthThreshold = 9
				}
			}
			threshold := (int32(depthThreshold) * bandWidth << lm << bitRes) >> 4
			keepBand := codedBands <= start+2 || (bandBits > threshold && j <= signalBandwidth)

			if re != nil {
				if keepBand {
					re.EncodeBit(1, 1)
				} else {
					re.EncodeBit(0, 1)
				}
			}

			if keepBand {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}

		psum -= bits[j] + int32(intensityRsv)
		if intensityRsv > 0 {
			intensityRsv = int(log2FracTable[j-start])
		}
		psum += int32(intensityRsv)
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
		codedBands--
	}

	if intensityRsv > 0 {
		if *intensity > codedBands {
			*intensity = codedBands
		}
		if *intensity < start {
			*intensity = start
		}
		if re != nil {
			re.EncodeUniform(uint32(*intensity-start), uint32(codedBands+1-start))
		}
	} else {
		*intensity = 0
	}
	if *intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		if re != nil {
			re.EncodeBit(*dualStereo, 1)
		}
	} else {
		*dualStereo = 0
	}

	left := int32(total) - psum
	percoeff := celtUdiv32(left, int32(edges[codedBands]-edges[start]))
	left -= int32(edges[codedBands]-edges[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * int32(widths[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min32(left, int32(widths[j]))
		bits[j] += tmp
		left -= tmp
	}

	bal := int32(0)
	for j := start; j < codedBands; j++ {
		N0 := int32(widths[j])
		N := N0 << lm
		bit := bits[j] + bal
		excess := int32(0)
		if N > 1 {
			excess = max32(bit-cap[j], 0)
			bits[j] = bit - excess

			den := int32(channels) * N
			if channels == 2 && N > 2 && *dualStereo == 0 && j < *intensity {
				den++
			}
			NClogN := den * (int32(logN[j]) + logM)
			offset := (NClogN >> 1) - den*fineOffset
			if N == 2 {
				offset += (den << bitRes) >> 2
			}
			if bits[j]+offset < den*2<<bitRes {
				offset += NClogN >> 2
			} else if bits[j]+offset < den*3<<bitRes {
				offset += NClogN >> 3
			}

			ebits[j] = max32(0, bits[j]+offset+(den<<(bitRes-1)))
			ebits[j] = celtUdiv32(ebits[j], den) >> bitRes
			if int32(channels)*ebits[j] > (bits[j] >> bitRes) {
				ebits[j] = bits[j] >> stereo >> bitRes
			}
			ebits[j] = min32(ebits[j], maxFineBits)
			finePriority[j] = int32(boolToInt(ebits[j]*(den<<bitRes) >= bits[j]+offset))
			bits[j] -= int32(channels) * ebits[j] << bitRes
		} else {
			excess = max32(0, bit-(int32(channels)<<bitRes))
			bits[j] = bit - excess
			ebits[j] = 0
			finePriority[j] = 1
		}

		if excess > 0 {
			extraFine := min32(excess>>(stereo+bitRes), maxFineBits-ebits[j])
			ebits[j] += extraFine
			extraBits := extraFine * int32(channels) << bitRes
			finePriority[j] = int32(boolToInt(extraBits >= excess-bal))
			excess -= extraBits
			bal = excess
		} else {
			bal = 0
		}
	}
	*balance = int(bal)

	for j := codedBands; j < end; j++ {
		ebits[j] = bits[j] >> stereo >> bitRes
		bits[j] = 0
		finePriority[j] = int32(boolToInt(ebits[j] < 1))
	}

	return codedBands
}

func interpBits2PulsesMode(start, end, skipStart int, bits1, bits2, thresh, cap []int32,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int32,
	channels, lm int, rd *rangecoding.Decoder, pm *perModeTables) int {
	edges := pm.eBands
	widths := pm.eBandWidths
	logN := pm.logN

	allocFloor := int32(channels << bitRes)
	stereo := 0
	if channels > 1 {
		stereo = 1
	}
	logM := int32(lm << bitRes)
	bits1Band := bits1[start:end]
	bits2Band := bits2[start:end]
	threshBand := thresh[start:end]
	capBand := cap[start:end]
	bitsBand := bits[start:end]
	lo := 0
	hi := 1 << allocSteps
	for range allocSteps {
		mid := (lo + hi) >> 1
		psum := int32(0)
		done := 0
		for idx := len(bits1Band) - 1; idx >= 0; idx-- {
			tmp := bits1Band[idx] + ((int32(mid) * bits2Band[idx]) >> allocSteps)
			if tmp >= threshBand[idx] || done != 0 {
				done = 1
				psum += min32(tmp, capBand[idx])
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		if int(psum) > total {
			hi = mid
		} else {
			lo = mid
		}
	}
	psum := int32(0)
	done := 0
	for idx := len(bits1Band) - 1; idx >= 0; idx-- {
		tmp := bits1Band[idx] + ((int32(lo) * bits2Band[idx]) >> allocSteps)
		if tmp < threshBand[idx] && done == 0 {
			if tmp >= allocFloor {
				tmp = allocFloor
			} else {
				tmp = 0
			}
		} else {
			done = 1
		}
		tmp = min32(tmp, capBand[idx])
		bitsBand[idx] = tmp
		psum += tmp
	}

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}

		left := int32(total) - psum
		percoeff := celtUdiv32(left, int32(edges[codedBands]-edges[start]))
		left -= int32(edges[codedBands]-edges[start]) * percoeff
		rem := max32(left-int32(edges[j]-edges[start]), 0)
		bandWidth := int32(edges[codedBands] - edges[j])
		bandBits := bits[j] + percoeff*bandWidth + rem

		if bandBits >= max32(thresh[j], allocFloor+(1<<bitRes)) {
			if rd != nil {
				if rd.DecodeBit(1) == 1 {
					break
				}
			} else {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}

		psum -= bits[j] + int32(intensityRsv)
		if intensityRsv > 0 {
			intensityRsv = int(log2FracTable[j-start])
		}
		psum += int32(intensityRsv)
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
		codedBands--
	}

	if intensityRsv > 0 {
		if rd != nil {
			*intensity = start + int(rd.DecodeUniformSmall(uint32(codedBands+1-start)))
		} else {
			if *intensity > codedBands {
				*intensity = codedBands
			}
			if *intensity < start {
				*intensity = start
			}
		}
	} else {
		*intensity = 0
	}
	if *intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		if rd != nil {
			*dualStereo = rd.DecodeBit(1)
		}
	} else {
		*dualStereo = 0
	}

	left := int32(total) - psum
	percoeff := celtUdiv32(left, int32(edges[codedBands]-edges[start]))
	left -= int32(edges[codedBands]-edges[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * int32(widths[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min32(left, int32(widths[j]))
		bits[j] += tmp
		left -= tmp
	}

	bal := int32(0)
	for j := start; j < codedBands; j++ {
		N0 := int32(widths[j])
		N := N0 << lm
		bit := bits[j] + bal
		excess := int32(0)
		if N > 1 {
			excess = max32(bit-cap[j], 0)
			bits[j] = bit - excess

			den := int32(channels) * N
			if channels == 2 && N > 2 && *dualStereo == 0 && j < *intensity {
				den++
			}
			NClogN := den * (int32(logN[j]) + logM)
			offset := (NClogN >> 1) - den*fineOffset
			if N == 2 {
				offset += (den << bitRes) >> 2
			}
			if bits[j]+offset < den*2<<bitRes {
				offset += NClogN >> 2
			} else if bits[j]+offset < den*3<<bitRes {
				offset += NClogN >> 3
			}

			ebits[j] = max32(0, bits[j]+offset+(den<<(bitRes-1)))
			ebits[j] = celtUdiv32(ebits[j], den) >> bitRes
			if int32(channels)*ebits[j] > (bits[j] >> bitRes) {
				ebits[j] = bits[j] >> stereo >> bitRes
			}
			ebits[j] = min32(ebits[j], maxFineBits)
			finePriority[j] = int32(boolToInt(ebits[j]*(den<<bitRes) >= bits[j]+offset))
			bits[j] -= int32(channels) * ebits[j] << bitRes
		} else {
			excess = max32(0, bit-(int32(channels)<<bitRes))
			bits[j] = bit - excess
			ebits[j] = 0
			finePriority[j] = 1
		}

		if excess > 0 {
			extraFine := min32(excess>>(stereo+bitRes), maxFineBits-ebits[j])
			ebits[j] += extraFine
			extraBits := extraFine * int32(channels) << bitRes
			finePriority[j] = int32(boolToInt(extraBits >= excess-bal))
			excess -= extraBits
			bal = excess
		} else {
			bal = 0
		}
	}
	*balance = int(bal)

	for j := codedBands; j < end; j++ {
		ebits[j] = bits[j] >> stereo >> bitRes
		bits[j] = 0
		finePriority[j] = int32(boolToInt(ebits[j] < 1))
	}

	return codedBands
}
