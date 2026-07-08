//go:build gopus_libopus_oracle

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// BandAllocationProbe captures CELT decode-time band allocation after coarse
// energy and before spectrum decode. Used by libopus parity tests.
type BandAllocationProbe struct {
	Silent       bool
	Transient    bool
	Spread       int32
	AllocTrim    int32
	CodedBands   int32
	Intensity    int32
	DualStereo   int32
	Balance      int32
	TFRes        []int32
	Offsets      []int32
	Pulses       []int32
	FineQuant    []int32
	FinePriority []int32
}

// ProbeBandAllocationWithDecoder decodes through band allocation only.
func (d *Decoder) ProbeBandAllocationWithDecoder(rd *rangecoding.Decoder, frameSize int) (BandAllocationProbe, error) {
	if rd == nil {
		return BandAllocationProbe{}, ErrNilDecoder
	}
	if !ValidFrameSize(frameSize) {
		return BandAllocationProbe{}, ErrInvalidFrameSize
	}

	d.handleChannelTransition(int(d.channels))
	d.beginDecodedPacketPLCState()
	d.prepareMonoEnergyFromStereo()
	d.SetRangeDecoder(rd)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	if tell >= totalBits {
		return BandAllocationProbe{Silent: true}, nil
	}
	if tell == 1 && rd.DecodeBit(15) == 1 {
		return BandAllocationProbe{Silent: true}, nil
	}

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	d.decodeCoarseEnergyGLogInto(ensureGLogSlice(&d.scratchEnergies, end*int(d.channels)), end, header.intra, lm)

	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, header.transient)
	return BandAllocationProbe{
		Transient:    header.transient,
		Spread:       int32(allocation.spread),
		AllocTrim:    int32(allocation.allocTrim),
		CodedBands:   int32(allocation.codedBands),
		Intensity:    int32(allocation.intensity),
		DualStereo:   int32(allocation.dualStereo),
		Balance:      int32(allocation.balance),
		TFRes:        cloneInt32Slice(allocation.tfRes[:end]),
		Offsets:      cloneInt32Slice(allocation.offsets[:end]),
		Pulses:       cloneInt32Slice(allocation.pulses[:end]),
		FineQuant:    cloneInt32Slice(allocation.fineQuant[:end]),
		FinePriority: cloneInt32Slice(allocation.finePriority[:end]),
	}, nil
}

// ProbeBandAllocationFromPacket probes allocation from a standalone CELT packet.
func (d *Decoder) ProbeBandAllocationFromPacket(packet []byte, frameSize int) (BandAllocationProbe, error) {
	if len(packet) < 2 {
		return BandAllocationProbe{}, ErrInvalidFrame
	}
	var rd rangecoding.Decoder
	rd.Init(packet[1:])
	return d.ProbeBandAllocationWithDecoder(&rd, frameSize)
}

func cloneInt32Slice(src []int32) []int32 {
	if len(src) == 0 {
		return nil
	}
	out := make([]int32, len(src))
	copy(out, src)
	return out
}
