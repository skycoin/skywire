package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

var qextLastZeroICDF = []uint8{64, 50, 0}
var qextLastCapICDF = []uint8{110, 60, 0}
var qextLastOtherICDF = []uint8{120, 112, 70, 0}

func encodeQEXTDepth(enc *rangecoding.Encoder, depth, cap int, last *int) {
	if enc == nil || last == nil {
		return
	}

	sym := 3
	if depth == *last {
		sym = 2
	}
	if depth == cap {
		sym = 1
	}
	if depth == 0 {
		sym = 0
	}

	switch {
	case *last == 0:
		enc.EncodeICDF(min(sym, 2), qextLastZeroICDF, 7)
	case *last == cap:
		enc.EncodeICDF(min(sym, 2), qextLastCapICDF, 7)
	default:
		enc.EncodeICDF(sym, qextLastOtherICDF, 7)
	}

	if sym == 3 {
		enc.EncodeUniform(uint32(depth-1), uint32(cap))
	}
	*last = depth
}

func decodeQEXTDepth(dec *rangecoding.Decoder, cap int, last *int) int {
	if dec == nil || last == nil {
		return 0
	}

	sym := 0
	switch {
	case *last == 0:
		sym = dec.DecodeICDF(qextLastZeroICDF, 7)
		if sym == 2 {
			sym = 3
		}
	case *last == cap:
		sym = dec.DecodeICDF(qextLastCapICDF, 7)
		if sym == 2 {
			sym = 3
		}
	default:
		sym = dec.DecodeICDF(qextLastOtherICDF, 7)
	}

	depth := 0
	switch sym {
	case 0:
		depth = 0
	case 1:
		depth = cap
	case 2:
		depth = *last
	default:
		depth = 1 + int(dec.DecodeUniform(uint32(cap)))
	}
	*last = depth
	return depth
}

func medianOf5Float32(x []float32) float32 {
	t0, t1 := x[0], x[1]
	if t0 > t1 {
		t0, t1 = t1, t0
	}
	t3, t4 := x[3], x[4]
	if t3 > t4 {
		t3, t4 = t4, t3
	}
	if t0 > t3 {
		t0, t3 = t3, t0
		t1, t4 = t4, t1
	}
	t2 := x[2]
	if t2 > t1 {
		if t1 < t3 {
			if t2 < t3 {
				return t2
			}
			return t3
		}
		if t4 < t1 {
			return t4
		}
		return t1
	}
	if t2 < t3 {
		if t1 < t3 {
			return t1
		}
		return t3
	}
	if t2 < t4 {
		return t2
	}
	return t4
}

func qextBandLogEGLogMax32(bandLogE []celtGLog, nbBands, channels, band int) float32 {
	if band < 0 || band >= nbBands || len(bandLogE) <= band {
		return 0
	}
	v := float32(bandLogE[band])
	if channels == 2 && nbBands+band < len(bandLogE) && float32(bandLogE[nbBands+band]) > v {
		v = float32(bandLogE[nbBands+band])
	}
	return v
}

func qextExtraBandWidth(edges []int, band, lm int) int {
	if band < 0 || band+1 >= len(edges) {
		return 0
	}
	return (edges[band+1] - edges[band]) << lm
}

// computeQEXTExtraAllocationEncode mirrors libopus clt_compute_extra_allocation()
// for the encode side. It fills extraPulses/extraQuant for the main bands in
// [start,end) and, when qextMode != nil, for the QEXT bands in [end,end+qextEnd).
func computeQEXTExtraAllocationEncode(start, end, qextEnd, totalQ3 int, channels, lm int,
	bandLogE []celtGLog, qextBandLogE []celtGLog, qextMode *qextModeConfig, toneFreq, toneishness float32,
	enc *rangecoding.Encoder, extraPulses, extraQuant []int32,
) {
	mainBands := len(bandLogE)
	qextBands := len(qextBandLogE)
	if channels > 1 {
		mainBands /= channels
		qextBands /= channels
	}

	totBands := end
	totSamples := (EBands[end] - EBands[start]) * channels << lm
	if qextMode != nil {
		totBands = end + qextEnd
		totSamples = qextMode.EBands[qextEnd] * channels << lm
	}

	limit := min(len(extraPulses), len(extraQuant))
	for i := start; i < limit; i++ {
		extraPulses[i] = 0
		extraQuant[i] = 0
	}
	if totalQ3 <= 0 || totSamples <= 0 || end <= start || limit == 0 {
		return
	}

	capVals := make([]int32, totBands)
	depth := make([]int32, totBands)
	flatE := make([]float32, totBands)
	ncoef := make([]int32, totBands)
	minVals := make([]float32, totBands)
	follower := make([]float32, totBands)

	for i := start; i < end; i++ {
		capVals[i] = 12
		ncoef[i] = int32(qextExtraBandWidth(EBands[:], i, lm) * channels)
		flatE[i] = qextBandLogEGLogMax32(bandLogE, mainBands, channels, i) - 0.0625*float32(LogN[i]) + float32(eMeans[i]) - 0.0062*float32((i+5)*(i+5))
	}
	if end > start {
		flatE[end-1] += 2.0
	}

	if qextMode != nil {
		minDepth := float32(0.0)
		if totalQ3 >= 3*channels*(qextMode.EBands[qextEnd]-qextMode.EBands[start])<<lm<<bitRes && (toneishness < 0.98 || toneFreq > 1.33) {
			minDepth = 1.0
		}
		for i := range qextEnd {
			idx := end + i
			capVals[idx] = 14
			ncoef[idx] = int32(qextExtraBandWidth(qextMode.EBands, i, lm) * channels)
			minVals[idx] = minDepth
			flatE[idx] = qextBandLogEGLogMax32(qextBandLogE, qextBands, channels, i) - 0.0625*float32(qextMode.LogN[i]) + float32(eMeans[i]) - 0.0062*float32((idx+5)*(idx+5))
		}
	}

	if totBands-start >= 5 {
		for i := start + 2; i < totBands-2; i++ {
			follower[i] = medianOf5Float32(flatE[i-2:])
		}
		follower[start] = follower[start+2]
		follower[start+1] = follower[start+2]
		follower[totBands-2] = follower[totBands-3]
		follower[totBands-1] = follower[totBands-3]
	} else {
		for i := start; i < totBands; i++ {
			follower[i] = flatE[i]
		}
	}

	for i := start + 1; i < totBands; i++ {
		if follower[i-1]-1.0 > follower[i] {
			follower[i] = follower[i-1] - 1.0
		}
	}
	for i := totBands - 2; i >= start; i-- {
		if follower[i+1]-1.0 > follower[i] {
			follower[i] = follower[i+1] - 1.0
		}
	}

	toneScale := float32(1.0 - toneishness)
	if toneScale < 0 {
		toneScale = 0
	}
	if toneScale > 1 {
		toneScale = 1
	}
	for i := start; i < totBands; i++ {
		flatE[i] -= toneScale * follower[i]
	}
	if qextMode != nil {
		for i := range qextEnd {
			flatE[end+i] += 3.0 + 0.2*float32(i)
		}
	}

	totalBits := totalQ3 >> bitRes
	sum := float32(0)
	for i := start; i < totBands; i++ {
		sum += float32(ncoef[i]) * flatE[i]
	}
	fill := (float32(totalBits) + sum) / float32(totSamples)
	for range 10 {
		sum = 0
		for i := start; i < totBands; i++ {
			target := flatE[i] - fill
			if target < minVals[i] {
				target = minVals[i]
			}
			if target > float32(capVals[i]) {
				target = float32(capVals[i])
			}
			sum += float32(ncoef[i]) * target
		}
		fill -= (float32(totalBits) - sum) / float32(totSamples)
	}

	last := 0
	for i := start; i < totBands; i++ {
		target := flatE[i] - fill
		if target < minVals[i] {
			target = minVals[i]
		}
		if target > float32(capVals[i]) {
			target = float32(capVals[i])
		}
		depth[i] = int32(0.5 + 4.0*target)
		if enc != nil && enc.TellFrac()+80 < enc.StorageBits()<<bitRes {
			encodeQEXTDepth(enc, int(depth[i]), int(4*capVals[i]), &last)
		} else {
			depth[i] = 0
		}
	}

	for i := start; i < end && i < limit; i++ {
		extraQuant[i] = (depth[i] + 3) >> 2
		width := qextExtraBandWidth(EBands[:], i, lm)
		extraPulses[i] = int32((((width)-1)*channels*int(depth[i])*(1<<bitRes) + 2) >> 2)
	}
	if qextMode != nil {
		for i := 0; i < qextEnd && end+i < limit; i++ {
			idx := end + i
			extraQuant[idx] = (depth[idx] + 3) >> 2
			width := qextExtraBandWidth(qextMode.EBands, i, lm)
			extraPulses[idx] = int32((((width)-1)*channels*int(depth[idx])*(1<<bitRes) + 2) >> 2)
		}
	}
}

// computeQEXTExtraAllocationDecodeWithMode mirrors the decode-side
// clt_compute_extra_allocation() path for the main bands in [start,end) and,
// when qextMode != nil, the QEXT extra bands in [MaxBands, MaxBands+qextEnd).
func computeQEXTExtraAllocationDecodeWithMode(start, end, qextEnd, totalQ3 int, channels, lm int,
	dec *rangecoding.Decoder, extraPulses, extraQuant []int32, qextMode *qextModeConfig,
) {
	limit := min(len(extraPulses), len(extraQuant))
	if limit > 0 {
		clear(extraPulses[:limit])
		clear(extraQuant[:limit])
	}
	if totalQ3 <= 0 || end <= start || dec == nil || limit == 0 {
		return
	}

	var depth [MaxBands + nbQEXTBands]int32
	var capVals [MaxBands + nbQEXTBands]int32
	last := 0
	for i := start; i < end; i++ {
		capVals[i] = 12
		if dec.TellFrac()+80 < dec.StorageBits()<<bitRes {
			depth[i] = int32(decodeQEXTDepth(dec, int(4*capVals[i]), &last))
		} else {
			depth[i] = 0
		}
		if i >= limit {
			continue
		}
		extraQuant[i] = (depth[i] + 3) >> 2
		width := qextExtraBandWidth(EBands[:], i, lm)
		extraPulses[i] = int32((((width)-1)*channels*int(depth[i])*(1<<bitRes) + 2) >> 2)
	}
	if qextMode != nil {
		for i := range qextEnd {
			idx := MaxBands + i
			if idx >= limit {
				break
			}
			capVals[idx] = 14
			if dec.TellFrac()+80 < dec.StorageBits()<<bitRes {
				depth[idx] = int32(decodeQEXTDepth(dec, int(4*capVals[idx]), &last))
			} else {
				depth[idx] = 0
			}
			extraQuant[idx] = (depth[idx] + 3) >> 2
			width := qextExtraBandWidth(qextMode.EBands, i, lm)
			extraPulses[idx] = int32((((width)-1)*channels*int(depth[idx])*(1<<bitRes) + 2) >> 2)
		}
	}
}
