// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func celtUdiv32(a, b int32) int32 {
	return int32(celtUdiv(int(a), int(b)))
}

// AllocationResult holds the output of bit allocation computation.
type AllocationResult struct {
	BandBits     []int32 // PVQ bit budget per band in Q3 (a.k.a. pulses[] in libopus)
	FineBits     []int32 // Fine energy bits per band
	FinePriority []int32 // Fine energy priority flags per band
	Caps         []int32 // PVQ caps per band in Q3
	Balance      int     // Bit balance carried into quant_all_bands (Q3)
	CodedBands   int     // Number of coded bands
	Intensity    int     // Intensity stereo start band (0 when disabled)
	DualStereo   bool    // Dual stereo flag
}

// ComputeAllocation computes bit allocation without consuming a range coder.
// This mirrors libopus clt_compute_allocation() math but skips entropy reads.
func ComputeAllocation(totalBits, nbBands, channels int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int) AllocationResult {
	return computeAllocation(nil, totalBits, nbBands, channels, cap, offsets, trim, intensity, dualStereo, lm)
}

// ComputeAllocationWithDecoder computes bit allocation and consumes the range decoder
// for skip/intensity/dual-stereo decisions.
func ComputeAllocationWithDecoder(rd *rangecoding.Decoder, totalBits, nbBands, channels int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int) AllocationResult {
	return computeAllocation(rd, totalBits, nbBands, channels, cap, offsets, trim, intensity, dualStereo, lm)
}

func computeAllocation(rd *rangecoding.Decoder, totalBits, nbBands, channels int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int) AllocationResult {
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
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := AllocationResult{
		BandBits:     make([]int32, nbBands),
		FineBits:     make([]int32, nbBands),
		FinePriority: make([]int32, nbBands),
		Caps:         make([]int32, nbBands),
		Balance:      0,
		CodedBands:   nbBands,
		Intensity:    0,
		DualStereo:   false,
	}

	if nbBands == 0 || totalBits <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = make([]int32, nbBands)
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocation(0, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBits<<bitRes, &balance, pulses, fineBits, finePriority, channels, lm, rd)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

func cltComputeAllocation(start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int,
	rd *rangecoding.Decoder) int {
	return cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, intensity, dualStereo,
		totalBitsQ3, balance, pulses, ebits, finePriority, channels, lm, rd, nil)
}

func cltComputeAllocationWithScratch(start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int,
	rd *rangecoding.Decoder, scratch []int32) int {
	lenBands := MaxBands
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
		width := int32(eBandWidths[j])
		widthLM := width << lm
		bandScale[j] = channels32 * widthLM
		thresh[j] = max32(channels32<<bitRes, (3*widthLM<<bitRes)>>4)
		trimOffset[j] = int32(int64(channels*int(width)*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if widthLM == 1 {
			trimOffset[j] -= channels32 << bitRes
		}
	}

	lo := 1
	hi := len(BandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := int32(0)
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			bitsj := (bandScale[idx] * int32(BandAlloc[mid][idx])) >> 2
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
		bits1j := (bandScale[j] * int32(BandAlloc[lo][j])) >> 2
		bits2j := cap[j]
		if hi < len(BandAlloc) {
			bits2j = (bandScale[j] * int32(BandAlloc[hi][j])) >> 2
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

	codedBands := interpBits2Pulses(start, end, skipStart, bits1, bits2, thresh, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, rd)

	return codedBands
}

func interpBits2Pulses(start, end, skipStart int, bits1, bits2, thresh, cap []int32,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int32,
	channels, lm int, rd *rangecoding.Decoder) int {
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
		percoeff := celtUdiv32(left, int32(EBands[codedBands]-EBands[start]))
		left -= int32(EBands[codedBands]-EBands[start]) * percoeff
		rem := max32(left-int32(EBands[j]-EBands[start]), 0)
		bandWidth := int32(EBands[codedBands] - EBands[j])
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
	percoeff := celtUdiv32(left, int32(EBands[codedBands]-EBands[start]))
	left -= int32(EBands[codedBands]-EBands[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * int32(eBandWidths[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min32(left, int32(eBandWidths[j]))
		bits[j] += tmp
		left -= tmp
	}

	bal := int32(0)
	for j := start; j < codedBands; j++ {
		N0 := int32(eBandWidths[j])
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
			NClogN := den * (int32(LogN[j]) + logM)
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

// InitCaps initializes band caps for allocation.
// Exported for testing.
func InitCaps(nbBands, lm, channels int) []int32 {
	return initCaps(nbBands, lm, channels)
}

func initCaps(nbBands, lm, channels int) []int32 {
	caps := make([]int32, nbBands)
	initCapsInto(caps, nbBands, lm, channels)
	return caps
}

func initCapsInto(caps []int32, nbBands, lm, channels int) {
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
		N := eBandWidths[i] << lm
		idx := MaxBands*row + i
		cap := int32(cacheCaps[idx])
		caps[i] = (cap + 64) * int32(channels) * int32(N) >> 2
	}
}

// InitCapsInto initializes band caps into the provided slice.
// This is an exported wrapper around initCapsInto for callers outside celt.
func InitCapsInto(caps []int32, nbBands, lm, channels int) {
	initCapsInto(caps, nbBands, lm, channels)
}

// ComputeAllocationWithEncoder computes bit allocation in Q3 and encodes the stereo params
// to the range encoder. This is the encoding counterpart to ComputeAllocationWithDecoder.
// prev is the last coded band count used for skip hysteresis (0 = no history).
// signalBandwidth is the highest band index considered to carry signal (>= start).
func ComputeAllocationWithEncoder(re *rangecoding.Encoder, totalBitsQ3, nbBands, channels int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) AllocationResult {
	return ComputeAllocationWithEncoderStart(re, 0, totalBitsQ3, nbBands, channels, cap, offsets, trim, intensity, dualStereo, lm, prev, signalBandwidth)
}

// ComputeAllocationWithEncoderStart is ComputeAllocationWithEncoder for a band
// subset starting at start (start>0 is the hybrid-CELT case where SILK occupies
// the low band). It encodes the skip/intensity/dual-stereo decisions over
// [start,nbBands) to the range encoder.
func ComputeAllocationWithEncoderStart(re *rangecoding.Encoder, start, totalBitsQ3, nbBands, channels int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) AllocationResult {
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
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := AllocationResult{
		BandBits:     make([]int32, nbBands),
		FineBits:     make([]int32, nbBands),
		FinePriority: make([]int32, nbBands),
		Caps:         make([]int32, nbBands),
		Balance:      0,
		CodedBands:   nbBands,
		Intensity:    0,
		DualStereo:   false,
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = make([]int32, nbBands)
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, start, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

// AllocEncodeScratch holds the reusable output slices for
// ComputeAllocationWithEncoderStartInto so a caller can run the encode-side bit
// allocation without per-call allocation. The per-band working buffers inside
// cltComputeAllocationEncode are fixed-size and stack-allocated, so only these
// four result slices need to be owned by the caller.
type AllocEncodeScratch struct {
	bandBits     []int32
	fineBits     []int32
	finePriority []int32
	caps         []int32
	result       AllocationResult
}

// ComputeAllocationWithEncoderStartInto is the allocation-free counterpart to
// ComputeAllocationWithEncoderStart: it writes the result into caller-owned
// scratch slices (grown once) and returns a pointer into that scratch. It is
// byte-identical to ComputeAllocationWithEncoderStart; only the result storage
// differs.
func ComputeAllocationWithEncoderStartInto(sc *AllocEncodeScratch, re *rangecoding.Encoder, start, totalBitsQ3, nbBands, channels int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
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
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := &sc.result
	result.BandBits = ensureInt32Slice(&sc.bandBits, nbBands)
	result.FineBits = ensureInt32Slice(&sc.fineBits, nbBands)
	result.FinePriority = ensureInt32Slice(&sc.finePriority, nbBands)
	result.Caps = ensureInt32Slice(&sc.caps, nbBands)
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false
	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = make([]int32, nbBands)
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, start, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

func cltComputeAllocationEncode(re *rangecoding.Encoder, start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int, prev int, signalBandwidth int) int {
	lenBands := MaxBands
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

	bits1 := make([]int32, lenBands)
	bits2 := make([]int32, lenBands)
	thresh := make([]int32, lenBands)
	trimOffset := make([]int32, lenBands)

	channels32 := int32(channels)
	for j := start; j < end; j++ {
		width := int32(eBandWidths[j])
		thresh[j] = max32(channels32<<bitRes, (3*(width<<lm)<<bitRes)>>4)
		trimOffset[j] = int32(int64(channels*int(width)*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if (width << lm) == 1 {
			trimOffset[j] -= channels32 << bitRes
		}
	}

	lo := 1
	hi := len(BandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := int32(0)
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			width := int32(eBandWidths[idx])
			bitsj := (channels32 * width * int32(BandAlloc[mid][idx]) << lm) >> 2
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
		width := int32(eBandWidths[j])
		bits1j := (channels32 * width * int32(BandAlloc[lo][j]) << lm) >> 2
		bits2j := cap[j]
		if hi < len(BandAlloc) {
			bits2j = (channels32 * width * int32(BandAlloc[hi][j]) << lm) >> 2
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

	codedBands := interpBits2PulsesEncode(re, start, end, skipStart, bits1, bits2, thresh, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, prev, signalBandwidth)

	return codedBands
}

func interpBits2PulsesEncode(re *rangecoding.Encoder, start, end, skipStart int, bits1, bits2, thresh, cap []int32,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int32,
	channels, lm int, prev int, signalBandwidth int) int {
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
		percoeff := celtUdiv32(left, int32(EBands[codedBands]-EBands[start]))
		left -= int32(EBands[codedBands]-EBands[start]) * percoeff
		rem := max32(left-int32(EBands[j]-EBands[start]), 0)
		bandWidth := int32(EBands[codedBands] - EBands[j])
		bandBits := bits[j] + percoeff*bandWidth + rem

		if bandBits >= max32(thresh[j], allocFloor+(1<<bitRes)) {
			// Compute the skip/keep decision (same logic whether encoding or not)
			// Match libopus exactly:
			//   if (codedBands<=start+2 || (band_bits > (depth_threshold*band_width<<LM<<BITRES)>>4 && j<=signalBandwidth))
			//
			// When codedBands > 17, depth_threshold is 7 or 9 depending on hysteresis.
			// When codedBands <= 17, depth_threshold is 0, which makes threshold=0,
			// so the condition simplifies to: codedBands<=start+2 || j<=signalBandwidth
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

			// Encode the decision if we have an encoder
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

	// Encode intensity and dual stereo params
	if intensityRsv > 0 {
		// Clamp intensity to valid range
		if *intensity > codedBands {
			*intensity = codedBands
		}
		if *intensity < start {
			*intensity = start
		}
		// Encode intensity using uniform distribution
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
		// Encode dual stereo bit
		if re != nil {
			re.EncodeBit(*dualStereo, 1)
		}
	} else {
		*dualStereo = 0
	}

	left := int32(total) - psum
	percoeff := celtUdiv32(left, int32(EBands[codedBands]-EBands[start]))
	left -= int32(EBands[codedBands]-EBands[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * int32(eBandWidths[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min32(left, int32(eBandWidths[j]))
		bits[j] += tmp
		left -= tmp
	}

	bal := int32(0)
	for j := start; j < codedBands; j++ {
		N0 := int32(eBandWidths[j])
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
			NClogN := den * (int32(LogN[j]) + logM)
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
