//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

// cubicQuantQEXT ports the FIXED_POINT encoder in celt/vq.c for a zero-pulse
// main allocation with an extension-side budget.
func cubicQuantQEXT(x []int32, n, resolution, blocks int, enc *rangecoding.Encoder, gain int32, resynth bool) uint32 {
	if n <= 0 || len(x) < n || enc == nil {
		return 0
	}
	k := 1 << resolution
	if blocks != 1 {
		k = imax(1, k-1)
	}
	if k == 1 {
		if resynth {
			clearInt32(x[:n])
		}
		return 0
	}

	face := 0
	faceValue := int32(-1)
	for i := 0; i < n; i++ {
		if value := abs32(x[i]); value > faceValue {
			faceValue = value
			face = i
		}
	}
	sign := boolToInt(x[face] < 0)
	enc.EncodeUniform(uint32(face), uint32(n))
	enc.EncodeRawBits(uint32(sign), 1)

	var iyStorage [qextMaxPVQDimension]int32
	iy := iyStorage[:n]
	if faceValue != 0 {
		faceShift := 30 - int(CeltILog2(faceValue))
		norm := CeltRcpNorm32(shl32(faceValue, faceShift))
		norm = mult16x32Q15(int16(k), norm)
		for i := 0; i < n; i++ {
			value := mult32x32q31(shl32(add32(x[i], faceValue), faceShift-1), norm) >> 15
			if value > int32(k-1) {
				value = int32(k - 1)
			}
			iy[i] = value
		}
	}
	for i := 0; i < n; i++ {
		if i != face {
			enc.EncodeRawBits(uint32(iy[i]), uint(resolution))
		}
	}
	if resynth {
		qextCubicSynthesis(x[:n], iy, n, k, face, sign, gain)
	}
	return uint32((1 << blocks) - 1)
}

// cubicQuantPartitionEncodeQEXT ports celt/bands.c cubic_quant_partition. It
// recursively sends the Q30 split angle on the side coder, then quantizes each
// leaf with cubic_quant. The context's encoder is the QEXT stream during the
// extension-band pass.
func cubicQuantPartitionEncodeQEXT(ctx *bandEncCtx, x []int32, n int, budget int32, blocks, lm int, gain int32) uint {
	remaining := int32(ctx.enc.StorageBits()<<bitRes) - int32(ctx.enc.TellFrac())
	ctx.remainingBits = int(remaining)
	if budget > remaining {
		budget = remaining
	}
	if lm == 0 || budget <= int32(2*n<<bitRes) {
		budget += int32((n-1)<<bitRes) / 2
		if budget > remaining {
			budget = remaining
		}
		resolution := (budget - (1 << bitRes) - int32(ctx.logN[ctx.band]) - int32(lm<<bitRes) - 1) / int32(n-1) >> bitRes
		if resolution < 0 {
			resolution = 0
		} else if resolution > 14 {
			resolution = 14
		}
		collapse := cubicQuantQEXT(x[:n], n, int(resolution), blocks, ctx.enc, gain, ctx.resynth)
		ctx.remainingBits = int(int32(ctx.enc.StorageBits()<<bitRes) - int32(ctx.enc.TellFrac()))
		return uint(collapse)
	}

	// celt/bands.c halves N and the vector, then codes a rounded Q30 angle.
	n0 := int32(n)
	n >>= 1
	y := x[n : 2*n]
	lm--
	blocks = (blocks + 1) >> 1
	thetaRes := (budget>>bitRes)/(n0-1) + 1
	if thetaRes > 16 {
		thetaRes = 16
	}
	theta := stereoItheta(x[:n], y, false, n)
	qtheta := (theta + int32(1<<(29-int(thetaRes)))) >> (30 - int(thetaRes))
	ctx.enc.EncodeUniform(uint32(qtheta), uint32((1<<int(thetaRes))+1))
	theta = qtheta << (30 - int(thetaRes))
	budget -= thetaRes << bitRes
	delta := (n0 - 1) * 23 * ((theta >> 16) - 8192) >> (17 - bitRes)
	gain1 := CeltCosNorm32(theta)
	gain2 := CeltCosNorm32((1 << 30) - theta)

	var budget1, budget2 int32
	switch theta {
	case 0:
		budget1 = budget
	case 1 << 30:
		budget2 = budget
	default:
		budget1 = minI32(budget, maxI32(0, (budget-delta)/2))
		budget2 = budget - budget1
	}
	collapse := cubicQuantPartitionEncodeQEXT(ctx, x[:n], n, budget1, blocks, lm, mult32x32q31(gain, gain1))
	collapse |= cubicQuantPartitionEncodeQEXT(ctx, y, n, budget2, blocks, lm, mult32x32q31(gain, gain2))
	return collapse
}
