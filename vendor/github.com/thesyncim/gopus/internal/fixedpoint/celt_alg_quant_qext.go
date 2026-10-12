//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// AlgQuantQEXT ports celt/vq.c alg_quant with FIXED_POINT + ENABLE_QEXT. The
// main coder receives the base CWRS index while extEnc carries QEXT refinement
// symbols. The pulse search and normalized vectors use fixed-width celt_norm
// (int32), and all reusable pulse/search arrays come from scratch.
func AlgQuantQEXT(x []int32, n, k, spread, blocks int, enc, extEnc *rangecoding.Encoder,
	gain int32, resynth bool, extraBits int, scratch *celtEncodeScratch) uint32 {
	if extraBits < 2 || extEnc == nil {
		return AlgQuant(x, n, k, spread, blocks, enc, gain, resynth, scratch)
	}
	if enc == nil || n < 2 || k < 1 || len(x) < n || extraBits > 12 {
		return 0
	}
	x = x[:n]
	up := (1 << extraBits) - 1
	yyShift := imax(0, extraBits-7)
	var iy, upIy []int32
	var refine []int32
	var yy int32
	if scratch != nil {
		iy = ensureInt32(&scratch.qextIy, n)
		upIy = ensureInt32(&scratch.qextUpIy, n)
		refine = ensureInt32(&scratch.qextRefine, n)
	} else {
		iy = make([]int32, n)
		upIy = make([]int32, n)
		refine = make([]int32, n)
	}

	expRotation(x, n, 1, blocks, k, spread)
	if n == 2 {
		yy = opPvqSearchN2QEXT(x, iy, upIy, k, up, &refine[0], yyShift)
	} else {
		var xNorm, rounding []int32
		if scratch != nil {
			xNorm = ensureInt32(&scratch.qextXNorm, n)
			rounding = ensureInt32(&scratch.qextRounding, n)
		} else {
			xNorm = make([]int32, n)
			rounding = make([]int32, n)
		}
		yy = opPvqSearchExtraQEXT(x, xNorm, rounding, iy, upIy, refine, k, up, yyShift)
	}

	collapse := extractCollapseMask32(upIy, n, blocks)
	index := celt.EncodePulses32Scratch(iy, n, k, pulseCWRSBuffer(scratch))
	vSize := celt.PVQ_V(n, k)
	if vSize == 0 {
		return 0
	}
	enc.EncodeUniform(index, vSize)
	if n == 2 {
		extEnc.EncodeUniform(uint32(refine[0]+int32((up-1)/2)), uint32(up))
	} else {
		useEntropy := (extEnc.StorageBits() - extEnc.Tell()) > (n-1)*(extraBits+3)+1
		for i := 0; i < n-1; i++ {
			encodeQEXTPVQRefine(extEnc, refine[i], up, extraBits, useEntropy)
		}
		if iy[n-1] == 0 {
			extEnc.EncodeRawBits(uint32(boolToInt(upIy[n-1] < 0)), 1)
		}
	}
	if resynth {
		normaliseResidualQEXT(upIy, x, n, yy, gain, yyShift)
		expRotation(x, n, -1, blocks, k, spread)
	}
	return collapse
}

func computeQEXTPVQRefineBits(extEnc *rangecoding.Encoder, totalBits, extBudget int32, n int) int {
	if extEnc == nil || n <= 1 || extBudget <= 0 {
		return 0
	}
	extraBits := (extBudget / int32(n-1)) >> bitRes
	extRemaining := totalBits - int32(extEnc.TellFrac())
	if extRemaining < ((extraBits+1)*int32(n-1)+int32(n))<<bitRes {
		extraBits = ((extRemaining - (int32(n) << bitRes)) / int32(n-1)) >> bitRes
		extraBits = maxI32(extraBits-1, 0)
	}
	extraBits = minI32(extraBits, 12)
	return int(extraBits)
}

func opPvqSearchN2QEXT(x []int32, iy, upIy []int32, k, up int, refine *int32, shift int) int32 {
	sum := add32(abs32(x[0]), abs32(x[1]))
	if sum < epsilon {
		iy[0] = int32(k)
		upIy[0] = int32(up * k)
		iy[1], upIy[1] = 0, 0
		*refine = 0
		// vq.c returns this silence fallback without the rounding bias used by
		// the normal search result.
		// vq.c evaluates k*k*up*up in opus_val64. Convert each operand
		// before multiplication so 32-bit Go targets preserve that width too.
		return int32((int64(k) * int64(k) * int64(up) * int64(up)) >> uint(2*shift))
	}
	sumShift := 30 - int(CeltILog2(sum))
	rcpSum := CeltRcpNorm32(shl32(sum, sumShift))
	x0 := mult32x32q31(shl32(x[0], sumShift), rcpSum)
	iy[0] = pshr32(mult32x32q31(shl32(int32(k), 8), x0), 7)
	upIy[0] = pshr32(mult32x32q31(shl32(int32(up*k), 8), x0), 7)
	lo := int32(up)*iy[0] - int32((up-1)/2)
	hi := int32(up)*iy[0] + int32((up-1)/2)
	upIy[0] = qextMax32(lo, qextMin32(hi, upIy[0]))
	offset := upIy[0] - int32(up)*iy[0]
	iy[1] = int32(k) - abs32(iy[0])
	upIy[1] = int32(up*k) - abs32(upIy[0])
	if x[1] < 0 {
		iy[1] = -iy[1]
		upIy[1] = -upIy[1]
		offset = -offset
	}
	*refine = offset
	return qextRoundedSquareSum(upIy, 2, shift)
}

func opPvqSearchExtraQEXT(x, xNorm, rounding, iy, upIy, refine []int32, k, up, shift int) int32 {
	var sum int32
	for _, v := range x {
		sum = add32(sum, abs32(v))
	}
	failed := sum < epsilon
	if !failed {
		sumShift := 30 - int(CeltILog2(sum))
		rcpSum := CeltRcpNorm32(shl32(sum, sumShift))
		for i, v := range x {
			xNorm[i] = mult32x32q31(shl32(abs32(v), sumShift), rcpSum)
		}
	}
	failed = failed || opPvqRefineQEXT(xNorm, rounding, iy, iy, k, 1, k+1)
	failed = failed || opPvqRefineQEXT(xNorm, rounding, upIy, iy, up*k, up, up)
	if failed {
		iy[0] = int32(k)
		upIy[0] = int32(up * k)
		for i := 1; i < len(iy); i++ {
			iy[i], upIy[i] = 0, 0
		}
	}
	var yy int64
	for i, v := range x {
		yy += int64(upIy[i]) * int64(upIy[i])
		if v < 0 {
			iy[i] = -iy[i]
			upIy[i] = -upIy[i]
		}
		refine[i] = upIy[i] - int32(up)*iy[i]
	}
	return qextRoundShift64(yy, shift)
}

func opPvqRefineQEXT(xNorm, rounding, iy, iy0 []int32, k, up, margin int) bool {
	var sum int32
	if len(rounding) < len(xNorm) {
		return true
	}
	rounding = rounding[:len(xNorm)]
	for i, v := range xNorm {
		tmp := mult32x32q31(shl32(int32(k), 8), v)
		iy[i] = (tmp + 64) >> 7
		rounding[i] = tmp - shl32(iy[i], 7)
	}
	if len(iy) != len(iy0) {
		return true
	}
	if &iy[0] != &iy0[0] {
		for i := range iy {
			lo := int32(up)*iy0[i] - int32(up) + 1
			hi := int32(up)*iy0[i] + int32(up) - 1
			iy[i] = qextMin32(hi, qextMax32(lo, iy[i]))
		}
	}
	for _, v := range iy {
		sum = add32(sum, v)
	}
	if abs32(sum-int32(k)) > 32 {
		return true
	}
	dir := int32(-1)
	if sum < int32(k) {
		dir = 1
	}
	for sum != int32(k) {
		roundVal := int32(-1000000) * dir
		pos := 0
		for i := range iy {
			if int64(rounding[i]-roundVal)*int64(dir) > 0 &&
				abs32(iy[i]-int32(up)*iy0[i]) < int32(margin-1) &&
				!(dir == -1 && iy[i] == 0) {
				roundVal = rounding[i]
				pos = i
			}
		}
		iy[pos] += dir
		rounding[pos] = sub32(rounding[pos], shl32(dir, 15))
		sum += dir
	}
	return false
}

func qextRoundedSquareSum(values []int32, n, shift int) int32 {
	var sum int64
	for i := 0; i < n; i++ {
		v := int64(values[i])
		sum += v * v
	}
	return qextRoundShift64(sum, shift)
}

func qextRoundShift64(value int64, shift int) int32 {
	if shift > 0 {
		value += int64(1) << uint(2*shift-1)
		value >>= uint(2 * shift)
	}
	return int32(value)
}

func normaliseResidualQEXT(iy, x []int32, n int, ryy, gain int32, shift int) {
	k := int(CeltILog2(ryy)) >> 1
	t := vshr32(ryy, 2*(k-7)-15)
	g := mult32x32q31(CeltRsqrtNorm32(t), gain)
	if shift > 0 {
		totalShift := normShift + 1 - k - shift
		for i := 0; i < n; i++ {
			v := iy[i]
			if totalShift >= 0 {
				v = shl32(v, totalShift)
			} else {
				v = pshr32(v, -totalShift)
			}
			x[i] = mult32x32q31(g, v)
		}
		return
	}
	for i := 0; i < n; i++ {
		x[i] = vshr32(mult16x32Q15(int16(iy[i]), g), k+15-normShift)
	}
}

func encodeQEXTPVQRefine(enc *rangecoding.Encoder, refine int32, up, extraBits int, useEntropy bool) {
	large := abs32(refine) > int32(up/2)
	logp := uint(1)
	if useEntropy {
		logp = 3
	}
	enc.EncodeBit(boolToInt(large), logp)
	if large {
		enc.EncodeRawBits(uint32(boolToInt(refine < 0)), 1)
		enc.EncodeRawBits(uint32(abs32(refine)-int32(up/2)-1), uint(extraBits-1))
	} else {
		enc.EncodeRawBits(uint32(refine+int32(up/2)), uint(extraBits))
	}
}

func extractCollapseMask32(iy []int32, n, blocks int) uint32 {
	if blocks <= 1 {
		return 1
	}
	n0 := int(celtUdiv(uint32(n), uint32(blocks)))
	var mask uint32
	for b := 0; b < blocks; b++ {
		var nonzero int32
		for i := 0; i < n0; i++ {
			nonzero |= iy[b*n0+i]
		}
		if nonzero != 0 {
			mask |= 1 << uint(b)
		}
	}
	return mask
}

func qextMin32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func qextMax32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
