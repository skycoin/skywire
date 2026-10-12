package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

type decodedBandAllocation struct {
	tfRes           []int32
	offsets         []int32
	pulses          []int32
	fineQuant       []int32
	finePriority    []int32
	spread          int
	allocTrim       int
	intensity       int
	dualStereo      int
	balance         int
	codedBands      int
	antiCollapseRsv int
}

func (d *Decoder) decodeBandAllocation(rd *rangecoding.Decoder, totalBits, start, end, lm int, transient bool) decodedBandAllocation {
	channels := int(d.channels)
	pm := d.perMode
	if pm == nil && lm >= 0 && lm <= 3 && channels >= 1 && channels <= 2 && end <= MaxBands && start >= 0 && start < end {
		return d.decodeBandAllocationStd(rd, totalBits, start, end, lm, transient, channels)
	}
	allocation := decodedBandAllocation{
		spread: spreadNormal,
	}

	allocation.tfRes = ensureInt32Slice(&d.scratchTFRes, end)
	tfDecode(start, end, transient, allocation.tfRes, lm, rd)

	tell := rd.Tell()
	if tell+4 <= totalBits {
		allocation.spread = rd.DecodeICDF(spreadICDF, 5)
	}

	offsets := ensureInt32Slice(&d.scratchOffsets, end)
	cap := ensureInt32Slice(&d.scratchCaps, end)
	if pm != nil {
		initCapsIntoMode(cap, end, lm, channels, pm)
	} else {
		initCapsInto(cap, end, lm, channels)
	}
	totalBitsQ3, tellFrac := decodeDynallocOffsets(rd, offsets, cap, d.modeEdges(), start, end, lm, channels, totalBits<<bitRes)
	allocation.offsets = offsets[:end]

	allocTrim := 5
	encodedTrim := tellFrac+(6<<bitRes) <= totalBitsQ3
	if encodedTrim {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	allocation.allocTrim = allocTrim

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		allocation.antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= allocation.antiCollapseRsv

	allocation.pulses = ensureInt32Slice(&d.scratchPulses, end)
	allocation.fineQuant = ensureInt32Slice(&d.scratchFineQuant, end)
	allocation.finePriority = ensureInt32Slice(&d.scratchFinePriority, end)
	if pm != nil {
		allocation.codedBands = cltComputeAllocationWithScratchMode(start, end, offsets, cap, allocTrim, &allocation.intensity, &allocation.dualStereo,
			bitsQ3, &allocation.balance, allocation.pulses, allocation.fineQuant, allocation.finePriority, channels, lm, rd, d.allocationScratch(), pm)
	} else {
		allocation.codedBands = cltComputeAllocation(start, end, offsets, cap, allocTrim, &allocation.intensity, &allocation.dualStereo,
			bitsQ3, &allocation.balance, allocation.pulses, allocation.fineQuant, allocation.finePriority, channels, lm, rd)
	}

	return allocation
}

// decodeDynallocOffsets decodes the dynalloc band boosts of
// celt_decode_with_ec() into offsets[start:end]. It returns total_bits (in
// 1/8 bits) less the boosts and the ec_tell_frac after the last boost flag.
func decodeDynallocOffsets(rd *rangecoding.Decoder, offsets, cap []int32, edges []int, start, end, lm, channels, totalBitsQ3 int) (int, int) {
	offsets = offsets[:end]
	cap = cap[:end]
	edges = edges[:end+1]
	dynallocLogp := 6
	tellFrac := rd.TellFrac()
	// C<<LM scales a band's bin count to its coded width.
	scale := channels << uint(lm)
	for i := start; i < end; i++ {
		width := scale * (edges[i+1] - edges[i])
		quanta := min(width<<bitRes, max(6<<bitRes, width))
		loopLogp := dynallocLogp
		boost := 0
		bandCap := int(cap[i])
		for tellFrac+(loopLogp<<bitRes) < totalBitsQ3 && boost < bandCap {
			flag := rd.DecodeBit(uint(loopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			loopLogp = 1
		}
		offsets[i] = int32(boost)
		if boost > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
	return totalBitsQ3, tellFrac
}
