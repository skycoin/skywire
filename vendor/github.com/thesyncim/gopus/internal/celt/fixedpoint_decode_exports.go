//go:build gopus_fixed_point

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// CELTDecodeAllocation holds the per-band bit allocation and side parameters the
// CELT decoder prologue derives between the spread decision and quant_all_bands.
type CELTDecodeAllocation struct {
	TFRes           []int32
	Offsets         []int32
	Pulses          []int32
	FineQuant       []int32
	FinePriority    []int32
	Spread          int
	AllocTrim       int
	Intensity       int
	DualStereo      int
	Balance         int
	CodedBands      int
	AntiCollapseRsv int
}

// TFDecode exposes the CELT tf_decode (celt/celt_decoder.c) for the integer
// decoder driver: it reads the per-band time-frequency change flags into tfRes.
func TFDecode(start, end int, isTransient bool, tfRes []int32, lm int, rd *rangecoding.Decoder) {
	tfDecode(start, end, isTransient, tfRes, lm, rd)
}

// DecodeCELTAllocation reproduces the celt_decode_with_ec allocation prologue
// (tf_decode -> spread -> dynalloc offsets -> alloc_trim -> anti_collapse_rsv ->
// clt_compute_allocation), reading from the shared range decoder. totalBits is
// len*8 (ec storage bits). It returns the fully-decoded allocation; all slices
// are freshly allocated of length end.
func DecodeCELTAllocation(rd *rangecoding.Decoder, totalBits, start, end, lm, channels int, transient bool) CELTDecodeAllocation {
	a := CELTDecodeAllocation{Spread: spreadNormal}

	a.TFRes = make([]int32, end)
	tfDecode(start, end, transient, a.TFRes, lm, rd)

	tell := rd.Tell()
	if tell+4 <= totalBits {
		a.Spread = rd.DecodeICDF(spreadICDF, 5)
	}

	cap := make([]int32, end)
	initCapsInto(cap, end, lm, channels)
	offsets := make([]int32, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := channels * (EBands[i+1] - EBands[i]) << lm
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
	a.Offsets = offsets

	a.AllocTrim = 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		a.AllocTrim = rd.DecodeICDF(trimICDF, 7)
	}

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		a.AntiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= a.AntiCollapseRsv

	a.Pulses = make([]int32, end)
	a.FineQuant = make([]int32, end)
	a.FinePriority = make([]int32, end)
	a.CodedBands = cltComputeAllocation(start, end, offsets, cap, a.AllocTrim, &a.Intensity, &a.DualStereo,
		bitsQ3, &a.Balance, a.Pulses, a.FineQuant, a.FinePriority, channels, lm, rd)

	return a
}
