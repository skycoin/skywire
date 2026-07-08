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
	allocation := decodedBandAllocation{
		spread: spreadNormal,
	}

	allocation.tfRes = ensureInt32Slice(&d.scratchTFRes, end)
	tfDecode32(start, end, transient, allocation.tfRes, lm, rd)

	tell := rd.Tell()
	if tell+4 <= totalBits {
		allocation.spread = rd.DecodeICDF(spreadICDF, 5)
	}

	cap := ensureInt32Slice(&d.scratchCaps, end)
	channels := int(d.channels)
	pm := d.perMode
	if pm != nil {
		initCapsIntoMode(cap, end, lm, channels, pm)
	} else {
		initCapsInto(cap, end, lm, channels)
	}
	edges := d.modeEdges()
	offsets := ensureInt32Slice(&d.scratchOffsets, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := channels * (edges[i+1] - edges[i]) << lm
		quanta := min(width<<bitRes, max(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for ; tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < int(cap[i]); j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = int32(boost)
		if j > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
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
	allocScratch := d.allocationScratch()
	if pm != nil {
		allocation.codedBands = cltComputeAllocationWithScratchMode(start, end, offsets, cap, allocTrim, &allocation.intensity, &allocation.dualStereo,
			bitsQ3, &allocation.balance, allocation.pulses, allocation.fineQuant, allocation.finePriority, channels, lm, rd, allocScratch, pm)
	} else {
		allocation.codedBands = cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, &allocation.intensity, &allocation.dualStereo,
			bitsQ3, &allocation.balance, allocation.pulses, allocation.fineQuant, allocation.finePriority, channels, lm, rd, allocScratch)
	}

	return allocation
}
