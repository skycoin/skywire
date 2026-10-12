//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	qextDepthScale           int32 = 1 << 10
	qextDepthShift                 = 8
	qextFlatLogShift               = dbShift - 10
	qextToneScaleQ15         int32 = 32767
	qextToneFreqThresholdQ14       = 21791   // QCONST16(1.33f, 14)
	qextFlatQuarterQ24       int32 = 1 << 20 // GCONST(.0625f)
	qextFlatCurveQ24         int32 = 104019  // GCONST(.0062f)
	qextExtraBandBiasQ10     int32 = 3072    // QCONST16(3.f, 10)
	qextExtraBandSlopeQ10    int32 = 205     // QCONST16(.2f, 10)
)

// computeQEXTExtraAllocationFixed ports the FIXED_POINT encode branch of
// celt/rate.c clt_compute_extra_allocation. CELT log energies remain Q24,
// flattened energies/followers are opus_val16 Q10, and pulse costs are Q3.
// The fixed arrays mirror libopus's bounded frame-stack scratch and do not
// allocate on the encode path.
func computeQEXTExtraAllocationFixed(start, end, qextEnd int, totalQ3 int32, channels, lm int,
	mainLogE, qextLogE []int32, mainLogN []int16, qextLogN []int16, qextEdges []int16,
	mode *celtBandGeometry, toneFreq int16, toneishness int32, enc *rangecoding.Encoder,
	extraPulses, extraQuant []int32,
) {
	const maxBands = celtNbEBands + 14
	totBands := end
	totSamples := int32(int16(0))
	if mode != nil {
		totBands += qextEnd
		totSamples = int32(qextEdges[qextEnd]) * int32(channels) << uint(lm)
	} else {
		totSamples = int32(int16(0))
	}
	limit := len(extraPulses)
	if len(extraQuant) < limit {
		limit = len(extraQuant)
	}
	for i := start; i < limit && i < end+qextEnd; i++ {
		extraPulses[i] = 0
		extraQuant[i] = 0
	}
	if mode == nil {
		mainEdges := staticMDCT48000EBands
		totSamples = int32(int(mainEdges[end])-int(mainEdges[start])) * int32(channels) << uint(lm)
	}
	if totalQ3 <= 0 || totSamples <= 0 || end <= start || limit == 0 {
		return
	}
	if totBands > maxBands {
		return
	}

	var caps, ncoef, depth [maxBands]int32
	var flat, minimum, follower [maxBands]int16
	for i := start; i < end; i++ {
		caps[i] = 12
		width := int32(staticMDCT48000EBands[i+1] - staticMDCT48000EBands[i])
		ncoef[i] = width * int32(channels) << uint(lm)
		flat[i] = qextFlatBandEnergy(mainLogE, celtNbEBands, channels, i, int32(mainLogN[i]))
	}
	if end > start {
		flat[end-1] = int16(int32(flat[end-1]) + 2*qextDepthScale)
	}
	if mode != nil {
		minDepth := int16(0)
		minBudget := int32(3 * channels * (int(qextEdges[qextEnd]) - int(qextEdges[0])) << uint(lm+bitRes))
		if totalQ3 >= minBudget && (toneishness < celtToneishnessQ29 || toneFreq > qextToneFreqThresholdQ14) {
			minDepth = int16(qextDepthScale)
		}
		for i := 0; i < qextEnd; i++ {
			idx := end + i
			caps[idx] = 14
			width := int32(qextEdges[i+1] - qextEdges[i])
			ncoef[idx] = width * int32(channels) << uint(lm)
			minimum[idx] = minDepth
			flat[idx] = qextFlatBandEnergy(qextLogE, 14, channels, i, int32(qextLogN[i]))
			bandIndex := idx + 5
			curve := int32(bandIndex*bandIndex) * qextFlatCurveQ24
			mean := int32(eMeans[i]) << (dbShift - 4)
			value := qextLogEnergyForChannel(qextLogE, 14, channels, i) - qextFlatQuarterQ24*int32(qextLogN[i]) + mean - curve
			flat[idx] = int16(pshr32(value, qextFlatLogShift))
		}
	}

	if totBands-start >= 5 {
		for i := start + 2; i < totBands-2; i++ {
			follower[i] = medianOf5FixedQEXT(flat[i-2 : i+3])
		}
		follower[start], follower[start+1] = follower[start+2], follower[start+2]
		follower[totBands-2], follower[totBands-1] = follower[totBands-3], follower[totBands-3]
	} else {
		copy(follower[start:totBands], flat[start:totBands])
	}
	for i := start + 1; i < totBands; i++ {
		if int32(follower[i]) < int32(follower[i-1])-qextDepthScale {
			follower[i] = int16(int32(follower[i-1]) - qextDepthScale)
		}
	}
	for i := totBands - 2; i >= start; i-- {
		if int32(follower[i]) < int32(follower[i+1])-qextDepthScale {
			follower[i] = int16(int32(follower[i+1]) - qextDepthScale)
		}
	}

	toneScale := int32(qextToneScaleQ15 - pshr32(toneishness, 14))
	for i := start; i < totBands; i++ {
		flat[i] = int16(int32(flat[i]) - mult16x16Q15(toneScale, int32(follower[i])))
	}
	if mode != nil {
		for i := 0; i < qextEnd; i++ {
			flat[end+i] = int16(int32(flat[end+i]) + qextExtraBandBiasQ10 + qextExtraBandSlopeQ10*int32(i))
		}
	}

	totalBits := shr32(totalQ3, bitRes)
	var sum int32
	for i := start; i < totBands; i++ {
		sum = add32(sum, ncoef[i]*int32(flat[i]))
	}
	fill := (shl32(totalBits, 10) + sum) / totSamples
	for iter := 0; iter < 10; iter++ {
		sum = 0
		for i := start; i < totBands; i++ {
			target := maxI32(int32(minimum[i]), int32(flat[i])-fill)
			target = minI32(shl32(caps[i], 10), target)
			sum = add32(sum, ncoef[i]*target)
		}
		fill -= (shl32(totalBits, 10) - sum) / totSamples
	}

	last := int32(0)
	for i := start; i < totBands; i++ {
		target := maxI32(int32(minimum[i]), int32(flat[i])-fill)
		target = minI32(shl32(caps[i], 10), target)
		depth[i] = pshr32(target, qextDepthShift)
		if enc == nil || int32(enc.TellFrac())+80 >= int32(enc.StorageBits()<<bitRes) {
			depth[i] = 0
			continue
		}
		celt.QEXTEncodeDepthExport(enc, depth[i], caps[i]<<2, &last)
	}

	for i := start; i < end && i < limit; i++ {
		extraQuant[i] = (depth[i] + 3) >> 2
		width := int32(staticMDCT48000EBands[i+1]-staticMDCT48000EBands[i]) << uint(lm)
		extraPulses[i] = (((width-1)*int32(channels)*depth[i]*(1<<bitRes) + 2) >> 2)
	}
	if mode != nil {
		for i := 0; i < qextEnd && end+i < limit; i++ {
			idx := end + i
			extraQuant[idx] = (depth[idx] + 3) >> 2
			width := int32(qextEdges[i+1]-qextEdges[i]) << uint(lm)
			extraPulses[idx] = (((width-1)*int32(channels)*depth[idx]*(1<<bitRes) + 2) >> 2)
		}
	}
}

func qextFlatBandEnergy(logE []int32, bands, channels, band int, logN int32) int16 {
	value := qextLogEnergyForChannel(logE, bands, channels, band) - qextFlatQuarterQ24*logN
	value += int32(eMeans[band]) << (dbShift - 4)
	bandIndex := int32(band + 5)
	value -= qextFlatCurveQ24 * bandIndex * bandIndex
	return int16(pshr32(value, qextFlatLogShift))
}

func qextLogEnergyForChannel(logE []int32, bands, channels, band int) int32 {
	value := logE[band]
	if channels == 2 && logE[bands+band] > value {
		value = logE[bands+band]
	}
	return value
}

func medianOf5FixedQEXT(x []int16) int16 {
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
