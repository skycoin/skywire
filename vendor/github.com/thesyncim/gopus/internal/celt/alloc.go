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

// cltComputeAllocation is libopus clt_compute_allocation() for the decoder, or
// for no coder when rd is nil (skip decisions then keep every band).
func cltComputeAllocation(start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int,
	rd *rangecoding.Decoder) int {
	start, end = allocBandRange(start, end)
	totalBitsQ3, skipRsv, intensityRsv, dualStereoRsv := allocReserve(start, end, totalBitsQ3, channels)
	var v allocVectors
	skipStart := v.init(start, end, offsets, cap, allocTrim, totalBitsQ3, channels, lm)
	return interpBits2Pulses(&v, start, end, skipStart, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, rd)
}

// allocBandRange clamps the clt_compute_allocation band range to the static
// band layout.
func allocBandRange(start, end int) (int, int) {
	return max(start, 0), min(end, MaxBands)
}

// allocReserve takes the skip, intensity and dual-stereo reservations of
// clt_compute_allocation out of totalBitsQ3.
func allocReserve(start, end, totalBitsQ3, channels int) (total, skipRsv, intensityRsv, dualStereoRsv int) {
	total = max(totalBitsQ3, 0)
	if total >= 1<<bitRes {
		skipRsv = 1 << bitRes
		total -= skipRsv
	}
	if channels == 2 {
		intensityRsv = int(log2FracTable[end-start])
		if intensityRsv > total {
			intensityRsv = 0
		} else {
			total -= intensityRsv
			if total >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
				total -= dualStereoRsv
			}
		}
	}
	return total, skipRsv, intensityRsv, dualStereoRsv
}

// allocVectors holds the clt_compute_allocation per-band vectors that
// interp_bits2pulses interpolates between.
type allocVectors struct {
	bits1, bits2, thresh [MaxBands]int32
}

// init fills bits1, bits2 and thresh for bands [start, end) as
// clt_compute_allocation does: it bisects the static allocation vectors for the
// last one that fits totalBitsQ3 and returns skip_start.
func (v *allocVectors) init(start, end int, offsets, cap []int32, allocTrim, totalBitsQ3, channels, lm int) int {
	offsets = offsets[:end]
	cap = cap[:end]
	var trimOffset, bandScale [MaxBands]int32
	c := int32(channels)
	allocFloor := c << bitRes
	// C*N*(alloc_trim-5-LM)*(end-j-1)*(1<<(LM+BITRES))>>6 with the factors
	// that do not depend on the band taken out of the loop.
	trimScale := c * int32(allocTrim-5-lm) << uint(lm+bitRes)
	for j := start; j < end; j++ {
		width := int32(eBandWidths[j])
		widthLM := width << uint(lm)
		bandScale[j] = c * widthLM
		v.thresh[j] = max(allocFloor, (3*widthLM<<bitRes)>>4)
		trim := width * trimScale * int32(end-j-1) >> 6
		if widthLM == 1 {
			trim -= allocFloor
		}
		trimOffset[j] = trim
	}

	scale := bandScale[start:end]
	trim := trimOffset[start:end]
	off := offsets[start:end]
	thresh := v.thresh[start:end]
	capBand := cap[start:end]
	lo := 1
	hi := len(BandAlloc) - 1
	for lo <= hi {
		mid := (lo + hi) >> 1
		if int(allocSearchSum(scale, bandAlloc32[mid][start:end], trim, off, thresh, capBand, allocFloor)) > totalBitsQ3 {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	hi = lo
	lo--

	skipStart := start
	allocLo := &bandAlloc32[lo]
	for j := start; j < end; j++ {
		bits1 := allocSearchBits(bandScale[j], allocLo[j], trimOffset[j], 0)
		bits2 := cap[j]
		if hi < len(BandAlloc) {
			bits2 = bandScale[j] * bandAlloc32[hi][j] >> 2
		}
		if bits2 > 0 {
			bits2 = max(0, bits2+trimOffset[j])
		}
		if lo > 0 {
			bits1 += offsets[j]
		}
		bits2 += offsets[j]
		if offsets[j] > 0 {
			skipStart = j
		}
		v.bits1[j] = bits1
		v.bits2[j] = max(0, bits2-bits1)
	}
	return skipStart
}

// bandAlloc32 is BandAlloc as int32, the width the allocation search uses.
var bandAlloc32 = func() (t [len(BandAlloc)][MaxBands]int32) {
	for q, row := range BandAlloc {
		for j, v := range row {
			t[q][j] = int32(v)
		}
	}
	return t
}()

// allocSearchSum is the per-band total of one step of the clt_compute_allocation
// bisection over the static allocation vectors (libopus celt/rate.c): bands are
// visited from the top, and once one reaches its threshold every lower band
// contributes min(bits, cap); before that a band contributes allocFloor when
// it reaches allocFloor. The first loop covers the bands above the first one
// that reaches its threshold and the second loop the rest.
func allocSearchSum(bandScale, alloc, trimOffset, offsets, thresh, cap []int32, allocFloor int32) int32 {
	n := len(bandScale)
	alloc = alloc[:n]
	trimOffset = trimOffset[:n]
	offsets = offsets[:n]
	thresh = thresh[:n]
	cap = cap[:n]
	psum := int32(0)
	idx := n - 1
	for ; idx >= 0; idx-- {
		bitsj := allocSearchBits(bandScale[idx], alloc[idx], trimOffset[idx], offsets[idx])
		if bitsj >= thresh[idx] {
			break
		}
		psum += allocFloorContribution(bitsj, allocFloor)
	}
	for ; idx >= 0; idx-- {
		bitsj := allocSearchBits(bandScale[idx], alloc[idx], trimOffset[idx], offsets[idx])
		psum += min(bitsj, cap[idx])
	}
	return psum
}

// allocSearchBits is the clt_compute_allocation bits for one band and
// allocation vector entry.
func allocSearchBits(bandScale, alloc, trimOffset, offset int32) int32 {
	bitsj := (bandScale * alloc) >> 2
	if bitsj > 0 {
		bitsj = max(0, bitsj+trimOffset)
	}
	return bitsj + offset
}

// allocFloorContribution is allocFloor when bits reaches it and 0 below, the
// contribution of a band above the first one that reaches its threshold.
func allocFloorContribution(bits, allocFloor int32) int32 {
	v := int32(0)
	if bits >= allocFloor {
		v = allocFloor
	}
	return v
}

// interpSearchSum is the per-band total of one interp_bits2pulses bisection
// step, with the same threshold rule as allocSearchSum.
func interpSearchSum(bits1, bits2, thresh, cap []int32, mid, allocFloor int32) int32 {
	n := len(bits1)
	bits2 = bits2[:n]
	thresh = thresh[:n]
	cap = cap[:n]
	psum := int32(0)
	idx := n - 1
	for ; idx >= 0; idx-- {
		tmp := bits1[idx] + ((mid * bits2[idx]) >> allocSteps)
		if tmp >= thresh[idx] {
			break
		}
		psum += allocFloorContribution(tmp, allocFloor)
	}
	for ; idx >= 0; idx-- {
		tmp := bits1[idx] + ((mid * bits2[idx]) >> allocSteps)
		psum += min(tmp, cap[idx])
	}
	return psum
}

// interpolate runs the interp_bits2pulses bisection between bits1 and bits2,
// stores the interpolated per-band bits in bits[start:end] and returns their
// sum.
func (v *allocVectors) interpolate(start, end int, cap, bits []int32, total, allocFloor int32) int32 {
	bits1 := v.bits1[start:end]
	bits2 := v.bits2[start:end][:len(bits1)]
	thresh := v.thresh[start:end][:len(bits1)]
	capBand := cap[start:end][:len(bits1)]
	out := bits[start:end][:len(bits1)]
	lo := int32(0)
	hi := int32(1 << allocSteps)
	for range allocSteps {
		mid := (lo + hi) >> 1
		if interpSearchSum(bits1, bits2, thresh, capBand, mid, allocFloor) > total {
			hi = mid
		} else {
			lo = mid
		}
	}
	psum := int32(0)
	idx := len(bits1) - 1
	for ; idx >= 0; idx-- {
		tmp := bits1[idx] + ((lo * bits2[idx]) >> allocSteps)
		if tmp >= thresh[idx] {
			break
		}
		tmp = min(allocFloorContribution(tmp, allocFloor), capBand[idx])
		out[idx] = tmp
		psum += tmp
	}
	for ; idx >= 0; idx-- {
		tmp := min(bits1[idx]+((lo*bits2[idx])>>allocSteps), capBand[idx])
		out[idx] = tmp
		psum += tmp
	}
	return psum
}

// allocSkipBandBits is the band_bits of the interp_bits2pulses skip loop for
// band j = codedBands-1: its interpolated bits plus its share of the bits left
// over the coded bands.
func allocSkipBandBits(start, codedBands int, bits []int32, total, psum int32) int32 {
	j := codedBands - 1
	left := total - psum
	width := int32(EBands[codedBands] - EBands[start])
	percoeff := celtUdiv32(left, width)
	left -= width * percoeff
	rem := max(left-int32(EBands[j]-EBands[start]), 0)
	bandWidth := int32(EBands[codedBands] - EBands[j])
	return bits[j] + percoeff*bandWidth + rem
}

// allocDropBand is the interp_bits2pulses skip-loop update for a skipped band
// j: it returns the new psum and intensity reservation and gives the band the
// allocation floor when bandBits reaches it.
func allocDropBand(start, j int, bits []int32, bandBits, psum, allocFloor int32, intensityRsv int) (int32, int) {
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
	return psum, intensityRsv
}

// interpBits2Pulses is libopus interp_bits2pulses() for the decoder, or for no
// coder when rd is nil.
func interpBits2Pulses(v *allocVectors, start, end, skipStart int, cap []int32,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int32,
	channels, lm int, rd *rangecoding.Decoder) int {
	allocFloor := int32(channels << bitRes)
	psum := v.interpolate(start, end, cap, bits, int32(total), allocFloor)

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}
		bandBits := allocSkipBandBits(start, codedBands, bits, int32(total), psum)
		if bandBits >= max(v.thresh[j], allocFloor+(1<<bitRes)) {
			if rd == nil || rd.DecodeBit(1) == 1 {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}
		psum, intensityRsv = allocDropBand(start, j, bits, bandBits, psum, allocFloor, intensityRsv)
		codedBands--
	}

	if intensityRsv > 0 {
		if rd != nil {
			*intensity = start + int(rd.DecodeUniformSmall(uint32(codedBands+1-start)))
		} else {
			*intensity = max(min(*intensity, codedBands), start)
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

	*balance = int(allocFineSplit(start, end, codedBands, int32(total)-psum, cap, bits, ebits, finePriority,
		channels, lm, *intensity, *dualStereo))
	return codedBands
}

// allocFineSplit is the tail of interp_bits2pulses: it spreads the bits left
// after the skip decisions over the coded bands, splits each coded band between
// fine energy (ebits) and PVQ (bits), and moves the bits of the bands above
// codedBands to fine energy. It returns the final balance.
func allocFineSplit(start, end, codedBands int, left int32, cap, bits, ebits, finePriority []int32,
	channels, lm, intensity, dualStereo int) int32 {
	cap = cap[:end]
	bits = bits[:end]
	ebits = ebits[:end]
	finePriority = finePriority[:end]
	c := int32(channels)
	stereo := 0
	if channels > 1 {
		stereo = 1
	}
	logM := int32(lm << bitRes)

	width := int32(EBands[codedBands] - EBands[start])
	percoeff := celtUdiv32(left, width)
	left -= width * percoeff
	for j := start; j < codedBands; j++ {
		n0 := int32(eBandWidths[j])
		tmp := min(left, n0)
		bits[j] += percoeff*n0 + tmp
		left -= tmp
	}

	bal := int32(0)
	for j := start; j < codedBands; j++ {
		n := int32(eBandWidths[j]) << uint(lm)
		bit := bits[j] + bal
		var excess, b, e, prio int32
		if n > 1 {
			excess = max(bit-cap[j], 0)
			b = bit - excess

			den := c * n
			if channels == 2 && n > 2 && dualStereo == 0 && j < intensity {
				den++
			}
			nClogN := den * (int32(LogN[j]) + logM)
			offset := (nClogN >> 1) - den*fineOffset
			if n == 2 {
				offset += (den << bitRes) >> 2
			}
			if b+offset < den*2<<bitRes {
				offset += nClogN >> 2
			} else if b+offset < den*3<<bitRes {
				offset += nClogN >> 3
			}

			e = max(0, b+offset+(den<<(bitRes-1)))
			e = celtUdiv32(e, den) >> bitRes
			if c*e > b>>bitRes {
				e = b >> stereo >> bitRes
			}
			e = min(e, maxFineBits)
			prio = int32(boolToInt(e*(den<<bitRes) >= b+offset))
			b -= c * e << bitRes
		} else {
			excess = max(0, bit-(c<<bitRes))
			b = bit - excess
			prio = 1
		}

		if excess > 0 {
			extraFine := min(excess>>(stereo+bitRes), maxFineBits-e)
			e += extraFine
			extraBits := extraFine * c << bitRes
			prio = int32(boolToInt(extraBits >= excess-bal))
			bal = excess - extraBits
		} else {
			bal = 0
		}
		bits[j] = b
		ebits[j] = e
		finePriority[j] = prio
	}

	for j := codedBands; j < end; j++ {
		e := bits[j] >> stereo >> bitRes
		ebits[j] = e
		bits[j] = 0
		finePriority[j] = int32(boolToInt(e < 1))
	}
	return bal
}

// InitCaps initializes band caps for allocation.
// Exported for testing.
func InitCaps(nbBands, lm, channels int) []int32 {
	return initCaps(nbBands, lm, channels)
}

// InitCapsInto computes the same per-band caps into caller-owned storage.
// CELT's fixed encoder keeps this slice across frames to avoid a per-packet
// allocation in the custom mode path.
func InitCapsInto(caps []int32, nbBands, lm, channels int) {
	initCapsInto(caps, nbBands, lm, channels)
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

	if nbBands == 0 {
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
	modeWork     []int32
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

	if nbBands == 0 {
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

// cltComputeAllocationEncode is libopus clt_compute_allocation() for the
// encoder; re may be nil to run the decisions without coding them.
func cltComputeAllocationEncode(re *rangecoding.Encoder, start, end int, offsets, cap []int32, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int32, channels, lm int, prev int, signalBandwidth int) int {
	start, end = allocBandRange(start, end)
	totalBitsQ3, skipRsv, intensityRsv, dualStereoRsv := allocReserve(start, end, totalBitsQ3, channels)
	var v allocVectors
	skipStart := v.init(start, end, offsets, cap, allocTrim, totalBitsQ3, channels, lm)
	return interpBits2PulsesEncode(re, &v, start, end, skipStart, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, prev, signalBandwidth)
}

// interpBits2PulsesEncode is libopus interp_bits2pulses() for the encoder.
func interpBits2PulsesEncode(re *rangecoding.Encoder, v *allocVectors, start, end, skipStart int, cap []int32,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int32,
	channels, lm int, prev int, signalBandwidth int) int {
	allocFloor := int32(channels << bitRes)
	prev = max(prev, 0)
	signalBandwidth = min(max(signalBandwidth, start), end-1)
	psum := v.interpolate(start, end, cap, bits, int32(total), allocFloor)

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}
		bandBits := allocSkipBandBits(start, codedBands, bits, int32(total), psum)
		if bandBits >= max(v.thresh[j], allocFloor+(1<<bitRes)) {
			// Past band 17 the band is kept only when it gets at least
			// depth_threshold/16 bits per coefficient, with hysteresis on the
			// previous frame's coded band count.
			depthThreshold := int32(0)
			if codedBands > 17 {
				if j < prev {
					depthThreshold = 7
				} else {
					depthThreshold = 9
				}
			}
			bandWidth := int32(EBands[codedBands] - EBands[j])
			threshold := (depthThreshold * bandWidth << uint(lm) << bitRes) >> 4
			keepBand := codedBands <= start+2 || (bandBits > threshold && j <= signalBandwidth)
			if re != nil {
				re.EncodeBit(boolToInt(keepBand), 1)
			}
			if keepBand {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}
		psum, intensityRsv = allocDropBand(start, j, bits, bandBits, psum, allocFloor, intensityRsv)
		codedBands--
	}

	if intensityRsv > 0 {
		*intensity = max(min(*intensity, codedBands), start)
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

	*balance = int(allocFineSplit(start, end, codedBands, int32(total)-psum, cap, bits, ebits, finePriority,
		channels, lm, *intensity, *dualStereo))
	return codedBands
}
