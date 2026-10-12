package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// decodeBandTableSet holds the per-band quantities of the standard mode's
// bit allocation that depend only on LM and the channel count C.
type decodeBandTableSet struct {
	// caps is init_caps().
	caps [MaxBands]int32
	// quanta is the dynalloc boost quantum
	// IMIN(width<<BITRES, IMAX(6<<BITRES, width)) for the coded width C*N<<LM.
	quanta [MaxBands]int32
	// thresh is clt_compute_allocation's
	// IMAX(C<<BITRES, (3*N<<LM<<BITRES)>>4).
	thresh [MaxBands]int32
	// alloc is C*N*allocVectors[q][j]<<LM>>2 for every allocation vector q.
	alloc [len(BandAlloc)][MaxBands]int32
}

// decodeBandTables is decodeBandTableSet for every LM and channel count.
var decodeBandTables = func() (t [4][2]decodeBandTableSet) {
	for lm := range t {
		for c := range t[lm] {
			s := &t[lm][c]
			channels := c + 1
			initCapsInto(s.caps[:], MaxBands, lm, channels)
			for j := range MaxBands {
				n := eBandWidths[j] << uint(lm)
				width := channels * n
				s.quanta[j] = int32(min(width<<bitRes, max(6<<bitRes, width)))
				s.thresh[j] = int32(max(channels<<bitRes, (3*n<<bitRes)>>4))
				for q := range BandAlloc {
					s.alloc[q][j] = int32(width * BandAlloc[q][j] >> 2)
				}
			}
		}
	}
	return t
}()

// stdAllocState is the per-packet band allocation of the standard mode, in
// fixed-size storage owned by the decoder.
type stdAllocState struct {
	tfRes, offsets, pulses, ebits, finePriority [MaxBands]int32
	// trimOffset, bits1 and bits2 are clt_compute_allocation()'s working
	// vectors.
	trimOffset, bits1, bits2 [MaxBands]int32
}

// decodeBandAllocationStd is the tf_decode(), spread, dynalloc, alloc_trim and
// clt_compute_allocation()/interp_bits2pulses() sequence of
// celt_decode_with_ec() (libopus celt/celt_decoder.c, celt/rate.c) for the
// standard mode. It requires 0 <= start < end <= MaxBands, 0 <= lm <= 3 and
// 1 <= channels <= 2.
func (d *Decoder) decodeBandAllocationStd(rd *rangecoding.Decoder, totalBits, start, end, lm int, transient bool, channels int) decodedBandAllocation {
	t := &decodeBandTables[lm][channels-1]
	s := &d.stdAlloc
	// The clamps state the required band range for the indexing below.
	start, end = max(start, 0), min(end, MaxBands)

	// tf_decode()
	budget := rd.StorageBits()
	tell := rd.Tell()
	isTransient := boolToInt(transient)
	logp := 4 - 2*isTransient
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
			tfChanged |= curr
		}
		s.tfRes[i] = int32(curr)
		logp = 5 - isTransient
	}
	row := &tfSelectTable[lm]
	tfSelect := 0
	if tfSelectRsv && row[4*isTransient+tfChanged] != row[4*isTransient+2+tfChanged] {
		tfSelect = rd.DecodeBit(1)
	}
	tfRow := row[4*isTransient+2*tfSelect:][:2]
	for i := start; i < end; i++ {
		s.tfRes[i] = int32(tfRow[s.tfRes[i]&1])
	}

	spread := spreadNormal
	if rd.Tell()+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}

	// Dynamic allocation boosts.
	totalBitsQ3 := int32(totalBits << bitRes)
	dynallocLogp := int32(6)
	for i := start; i < end; i++ {
		loopLogp := dynallocLogp
		boost := int32(0)
		quanta := t.quanta[i]
		bandCap := t.caps[i]
		// ec_tell_frac() never exceeds ec_tell()<<BITRES, so the whole-bit
		// tell decides the budget test unless it is within a bit of the
		// limit.
		for boost < bandCap {
			need := loopLogp << bitRes
			if int32(rd.Tell()<<bitRes)+need >= totalBitsQ3 && int32(rd.TellFrac())+need >= totalBitsQ3 {
				break
			}
			if rd.DecodeBit(uint(loopLogp)) == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			loopLogp = 1
		}
		s.offsets[i] = boost
		if boost > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
	tellFrac := int32(rd.TellFrac())

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}

	total := int32(totalBits<<bitRes) - int32(rd.TellFrac()) - 1
	antiCollapseRsv := int32(0)
	if transient && lm >= 2 && total >= int32(lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	total -= antiCollapseRsv

	// clt_compute_allocation()
	c := int32(channels)
	allocFloor := c << bitRes
	total = max(total, 0)
	skipStart := start
	skipRsv := int32(0)
	if total >= 1<<bitRes {
		skipRsv = 1 << bitRes
	}
	total -= skipRsv
	intensityRsv := int32(0)
	dualStereoRsv := int32(0)
	if channels == 2 {
		intensityRsv = int32(log2FracTable[end-start])
		if intensityRsv > total {
			intensityRsv = 0
		} else {
			total -= intensityRsv
			if total >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
			}
			total -= dualStereoRsv
		}
	}

	trimOffset, bits1, bits2 := &s.trimOffset, &s.bits1, &s.bits2
	// C*N*(alloc_trim-5-LM)*(end-j-1)*(1<<(LM+BITRES))>>6 with the factors
	// that do not depend on the band taken out of the loop.
	trimScale := c * int32(allocTrim-5-lm) << uint(lm+bitRes)
	for j := start; j < end; j++ {
		trim := int32(eBandWidths[j]) * trimScale * int32(end-j-1) >> 6
		if eBandWidths[j]<<uint(lm) == 1 {
			trim -= allocFloor
		}
		trimOffset[j] = trim
	}

	lo := allocSearchStd(t, s, start, end, total, allocFloor)
	hi := lo
	lo--

	allocLo := &t.alloc[lo]
	for j := start; j < end; j++ {
		bits1j := allocLo[j]
		bits2j := t.caps[j]
		if hi < len(BandAlloc) {
			bits2j = t.alloc[hi][j]
		}
		if bits1j > 0 {
			bits1j = max(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = max(0, bits2j+trimOffset[j])
		}
		off := s.offsets[j]
		if lo > 0 {
			bits1j += off
		}
		bits2j += off
		if off > 0 {
			skipStart = j
		}
		bits1[j] = bits1j
		bits2[j] = max(0, bits2j-bits1j)
	}

	// interp_bits2pulses()
	bits := &s.pulses
	loI := interpSearchStd(t, s, start, end, total, allocFloor)
	psum := int32(0)
	j := end
	for j > start {
		j--
		tmp := bits1[j] + (loI * bits2[j] >> allocSteps)
		if tmp >= t.thresh[j] {
			tmp = min(tmp, t.caps[j])
			bits[j] = tmp
			psum += tmp
			break
		}
		if tmp >= allocFloor {
			tmp = allocFloor
		} else {
			tmp = 0
		}
		tmp = min(tmp, t.caps[j])
		bits[j] = tmp
		psum += tmp
	}
	j = min(j, MaxBands)
	for j > start {
		j--
		tmp := min(bits1[j]+(loI*bits2[j]>>allocSteps), t.caps[j])
		bits[j] = tmp
		psum += tmp
	}

	// Decide which bands to skip, working backwards from the end.
	codedBands := end
	for ; ; codedBands-- {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}
		left := total - psum
		width := int32(EBands[codedBands] - EBands[start])
		percoeff := celtUdiv32(left, width)
		left -= width * percoeff
		rem := max(left-int32(EBands[j]-EBands[start]), 0)
		bandWidth := int32(EBands[codedBands] - EBands[j])
		bandBits := bits[j] + percoeff*bandWidth + rem
		if bandBits >= max(t.thresh[j], allocFloor+(1<<bitRes)) {
			if rd.DecodeBit(1) == 1 {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}
		psum -= bits[j] + intensityRsv
		if intensityRsv > 0 {
			intensityRsv = int32(log2FracTable[j-start])
		}
		psum += intensityRsv
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
	}

	intensity := 0
	if intensityRsv > 0 {
		intensity = start + int(rd.DecodeUniformSmall(uint32(codedBands+1-start)))
	}
	if intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	dualStereo := 0
	if dualStereoRsv > 0 {
		dualStereo = rd.DecodeBit(1)
	}

	// Allocate the remaining bits.
	left := total - psum
	width := int32(EBands[codedBands] - EBands[start])
	percoeff := celtUdiv32(left, width)
	left -= width * percoeff
	for j := start; j < codedBands; j++ {
		n0 := int32(eBandWidths[j])
		tmp := min(left, n0)
		bits[j] += percoeff*n0 + tmp
		left -= tmp
	}

	stereo := 0
	if channels > 1 {
		stereo = 1
	}
	logM := int32(lm << bitRes)
	balance := int32(0)
	for j := start; j < codedBands; j++ {
		n := int32(eBandWidths[j]) << uint(lm)
		bit := bits[j] + balance
		var excess, b, e, prio int32
		if n > 1 {
			excess = max(bit-t.caps[j], 0)
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
			prio = int32(boolToInt(extraBits >= excess-balance))
			excess -= extraBits
		}
		balance = excess
		bits[j] = b
		s.ebits[j] = e
		s.finePriority[j] = prio
	}
	// The skipped bands use all their bits for fine energy.
	for j := codedBands; j < end; j++ {
		e := bits[j] >> stereo >> bitRes
		s.ebits[j] = e
		bits[j] = 0
		s.finePriority[j] = int32(boolToInt(e < 1))
	}

	return decodedBandAllocation{
		tfRes:           s.tfRes[:end],
		offsets:         s.offsets[:end],
		pulses:          s.pulses[:end],
		fineQuant:       s.ebits[:end],
		finePriority:    s.finePriority[:end],
		spread:          spread,
		allocTrim:       allocTrim,
		intensity:       intensity,
		dualStereo:      dualStereo,
		balance:         int(balance),
		codedBands:      codedBands,
		antiCollapseRsv: int(antiCollapseRsv),
	}
}

// allocSearchStd is the clt_compute_allocation() bisection over the standard
// mode's allocation vectors, with s holding trim_offset and the dynalloc
// offsets: it returns the first vector whose total exceeds total, or
// len(BandAlloc) when none does. Bands above the first one that reaches its
// threshold contribute the allocation floor when they reach it; from that band
// down, every band contributes its capped bits.
func allocSearchStd(t *decodeBandTableSet, s *stdAllocState, start, end int, total, allocFloor int32) int {
	// The caller passes 0 <= start < end <= MaxBands; the clamps state those
	// bounds for the indexing below, and the loads check t and s for nil once
	// ahead of the loops.
	start, end = max(start, 0), min(end, MaxBands)
	_, _ = t.caps[0], s.bits1[0]
	lo := 1
	hi := len(BandAlloc) - 1
	for lo <= hi {
		mid := (lo + hi) >> 1
		alloc := &t.alloc[mid]
		psum := int32(0)
		j := end
		for j > start {
			j--
			bitsj := alloc[j]
			if bitsj > 0 {
				bitsj = max(0, bitsj+s.trimOffset[j])
			}
			bitsj += s.offsets[j]
			if bitsj >= t.thresh[j] {
				psum += min(bitsj, t.caps[j])
				break
			}
			if bitsj >= allocFloor {
				psum += allocFloor
			}
		}
		j = min(j, MaxBands)
		for j > start {
			j--
			bitsj := alloc[j]
			if bitsj > 0 {
				bitsj = max(0, bitsj+s.trimOffset[j])
			}
			psum += min(bitsj+s.offsets[j], t.caps[j])
		}
		if psum > total {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// interpSearchStd is the interp_bits2pulses() bisection between s.bits1 and
// s.bits1+s.bits2 in 1/64 steps; it returns the last step whose total fits
// total.
func interpSearchStd(t *decodeBandTableSet, s *stdAllocState, start, end int, total, allocFloor int32) int32 {
	// The caller passes 0 <= start < end <= MaxBands; the clamps state those
	// bounds for the indexing below, and the loads check t and s for nil once
	// ahead of the loops.
	start, end = max(start, 0), min(end, MaxBands)
	_, _ = t.caps[0], s.bits1[0]
	lo := int32(0)
	hi := int32(1 << allocSteps)
	// ALLOC_STEPS halvings of the power-of-two interval.
	for hi-lo > 1 {
		mid := (lo + hi) >> 1
		psum := int32(0)
		j := end
		for j > start {
			j--
			tmp := s.bits1[j] + (mid * s.bits2[j] >> allocSteps)
			if tmp >= t.thresh[j] {
				psum += min(tmp, t.caps[j])
				break
			}
			if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		j = min(j, MaxBands)
		for j > start {
			j--
			psum += min(s.bits1[j]+(mid*s.bits2[j]>>allocSteps), t.caps[j])
		}
		if psum > total {
			hi = mid
		} else {
			lo = mid
		}
	}
	return lo
}
