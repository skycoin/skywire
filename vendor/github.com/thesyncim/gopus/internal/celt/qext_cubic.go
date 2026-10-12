package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func cubicQEXTThresholdQ3(ctx *bandCtx, n, lm int) int {
	if ctx == nil {
		return 0
	}
	return (3 * n << bitRes) + (ctx.modeLogN(ctx.band) + 8 + 8*lm)
}

func cubicCoderRemainingQ3(ctx *bandCtx) int {
	if ctx == nil {
		return 0
	}
	if ctx.encode {
		if ctx.re == nil {
			return 0
		}
		return (ctx.re.StorageBits() << bitRes) - ctx.re.TellFrac()
	}
	if ctx.rd == nil {
		return 0
	}
	return (ctx.rd.StorageBits() << bitRes) - ctx.rd.TellFrac()
}

func cubicQuantPartition(ctx *bandCtx, x []celtNorm, n, b, B, lm int, gain opusVal16) int {
	if ctx == nil || n <= 0 || len(x) < n {
		return 0
	}
	x = x[:n:n]

	ctx.remainingBits = cubicCoderRemainingQ3(ctx)
	if b > ctx.remainingBits {
		b = ctx.remainingBits
	}

	if lm == 0 || b <= 2*n<<bitRes || n < 2 || (n&1) != 0 {
		b = min(b+((n-1)<<bitRes)/2, ctx.remainingBits)
		res := 0
		if n > 1 {
			num := b - (1 << bitRes) - ctx.modeLogN(ctx.band) - (lm << bitRes) - 1
			if num > 0 {
				res = (num / (n - 1)) >> bitRes
			}
		}
		res = min(14, max(0, res))
		if ctx.encode {
			if ctx.re == nil {
				return 0
			}
			ret := cubicQuant(x, n, res, B, ctx.re, gain, ctx.resynth, ctx.encScratch)
			ctx.remainingBits = cubicCoderRemainingQ3(ctx)
			return ret
		}
		if ctx.rd == nil {
			return 0
		}
		ret := cubicUnquant(x, n, res, B, ctx.rd, gain, ctx.scratch)
		ctx.remainingBits = cubicCoderRemainingQ3(ctx)
		return ret
	}

	n0 := n
	half := n >> 1
	y := x[half:n]
	x = x[:half:half]
	lm--
	B = (B + 1) >> 1

	thetaRes := min(16, (b>>bitRes)/(n0-1)+1)
	ithetaQ30 := 0
	if ctx.encode {
		if ctx.re == nil {
			return 0
		}
		ithetaQ30 = stereoIthetaQ30(x, y, false)
		qtheta := (ithetaQ30 + (1 << (29 - thetaRes))) >> (30 - thetaRes)
		ctx.re.EncodeUniform(uint32(qtheta), uint32((1<<thetaRes)+1))
		ithetaQ30 = qtheta << (30 - thetaRes)
	} else {
		if ctx.rd == nil {
			return 0
		}
		qtheta := int(ctx.rd.DecodeUniform(uint32((1 << thetaRes) + 1)))
		ithetaQ30 = qtheta << (30 - thetaRes)
	}

	b -= thetaRes << bitRes
	delta := (n0 - 1) * 23 * ((ithetaQ30 >> 16) - 8192) >> (17 - bitRes)
	theta := float32(ithetaQ30) * (1.0 / float32(1<<30))
	mid := celtCosNorm2F32(theta)
	side := celtCosNorm2F32(1.0 - theta)

	b1 := 0
	b2 := 0
	switch ithetaQ30 {
	case 0:
		b1 = b
	case 1 << 30:
		b2 = b
	default:
		b1 = min(b, max(0, (b-delta)/2))
		b2 = b - b1
	}

	cm := cubicQuantPartition(ctx, x, half, b1, B, lm, opusVal16(float32(gain)*mid))
	cm |= cubicQuantPartition(ctx, y, half, b2, B, lm, opusVal16(float32(gain)*side))
	return cm
}

func computeQEXTCubicBits(ctx *bandCtx, extBudget, n, b, lm int) int {
	_ = b
	_ = lm
	if ctx == nil || n <= 1 || extBudget <= 2*n<<bitRes {
		return 0
	}
	if ctx.encode {
		if ctx.extEnc == nil {
			return 0
		}
	} else if ctx.extDec == nil {
		return 0
	}

	extraBits := (extBudget / (n - 1)) >> bitRes
	extTellFrac := 0
	if ctx.encode {
		extTellFrac = ctx.extEnc.TellFrac()
	} else {
		extTellFrac = ctx.extDec.TellFrac()
	}
	extRemainingBits := ctx.extTotalBits - extTellFrac
	if extRemainingBits <= n<<bitRes {
		return 0
	}
	if extRemainingBits < ((extraBits+1)*(n-1)+n)<<bitRes {
		extraBits = ((extRemainingBits - (n << bitRes)) / (n - 1)) >> bitRes
		extraBits = max(extraBits-1, 0)
	}
	return min(14, extraBits)
}

func cubicSynthesis(x []celtNorm, iy []int32, n, k, face, sign int, gain opusVal16) {
	if n <= 0 || len(x) < n || len(iy) < n {
		return
	}

	k32 := int32(k)
	for i := range n {
		x[i] = celtNorm(1 + 2*iy[i] - k32)
	}
	if sign != 0 {
		x[face] = celtNorm(-k)
	} else {
		x[face] = celtNorm(k)
	}
	sum := cubicEnergySum(x, n)
	if sum <= pvqEPSILON {
		clear(x[:n])
		return
	}
	// C ref: celt/vq.c cubic_synthesis() assigns 1.f/sqrt(sum) to
	// opus_val32. sqrt and the division use double before the float32 store.
	mag := float32(1 / math.Sqrt(float64(sum)))
	scale := mag * float32(gain)
	for i := range n {
		x[i] = celtNorm(float32(x[i]) * scale)
	}
}

// cubicEnergySum matches celt/vq.c:cubic_synthesis() in the selected libopus
// build. Scalar builds retain the ordinary Go expression so compiler
// contraction follows the paired scalar C build. The arm64 SIMD archive rounds
// each four-wide product before adding lanes in order, then uses fused
// multiply-adds for the scalar tail.
func cubicEnergySum(x []celtNorm, n int) float32 {
	if !celtFusedFloat {
		sum := float32(0)
		for i := range n {
			sample := float32(x[i])
			sum += sample * sample
		}
		return sum
	}

	sum := float32(0)
	vectorEnd := n &^ 3
	for i := 0; i < vectorEnd; i += 4 {
		p0 := noFMA32Mul(float32(x[i]), float32(x[i]))
		p1 := noFMA32Mul(float32(x[i+1]), float32(x[i+1]))
		p2 := noFMA32Mul(float32(x[i+2]), float32(x[i+2]))
		p3 := noFMA32Mul(float32(x[i+3]), float32(x[i+3]))
		sum = noFMA32Add(sum, p0)
		sum = noFMA32Add(sum, p1)
		sum = noFMA32Add(sum, p2)
		sum = noFMA32Add(sum, p3)
	}
	for i := vectorEnd; i < n; i++ {
		v := float32(x[i])
		sum = opusmath.FMA32(v, v, sum)
	}
	return sum
}

func cubicQuant(x []celtNorm, n, res, B int, enc *rangecoding.Encoder, gain opusVal16, resynth bool, scratch *bandEncodeScratch) int {
	if n <= 0 || len(x) < n || enc == nil {
		return 0
	}

	k := 1 << res
	if B != 1 {
		k = max(1, k-1)
	}
	if k == 1 {
		if resynth {
			clear(x[:n])
		}
		return 0
	}

	var iy []int32
	if scratch != nil {
		iy = scratch.ensureQEXTIy(n)
	} else {
		iy = make([]int32, n)
	}

	face := 0
	faceVal := float32(-1)
	for i := range n {
		ax := absCeltNorm(x[i])
		if ax > faceVal {
			faceVal = ax
			face = i
		}
	}
	sign := 0
	if x[face] < 0 {
		sign = 1
	}

	enc.EncodeUniform(uint32(face), uint32(n))
	enc.EncodeRawBits(uint32(sign), 1)

	norm := float32(0.5) * float32(k) / (faceVal + pvqEPSILON)
	for i := range n {
		iy[i] = int32(min(k-1, floor32ToInt((float32(x[i])+faceVal)*norm)))
		if i == face {
			continue
		}
		enc.EncodeRawBits(uint32(iy[i]), uint(res))
	}

	if resynth {
		cubicSynthesis(x, iy, n, k, face, sign, gain)
	}
	return (1 << B) - 1
}

func cubicUnquant(x []celtNorm, n, res, B int, dec *rangecoding.Decoder, gain opusVal16, scratch *bandDecodeScratch) int {
	if n <= 0 || len(x) < n || dec == nil {
		return 0
	}

	k := 1 << res
	if B != 1 {
		k = max(1, k-1)
	}
	if k == 1 {
		clear(x[:n])
		return 0
	}

	var iy []int32
	if scratch != nil {
		iy = scratch.ensurePVQPulses(n)
	} else {
		iy = make([]int32, n)
	}

	face := int(dec.DecodeUniform(uint32(n)))
	sign := int(dec.DecodeRawBit())
	for i := range n {
		if i == face {
			continue
		}
		iy[i] = int32(dec.DecodeRawBits(uint(res)))
	}
	iy[face] = 0
	cubicSynthesis(x, iy, n, k, face, sign, gain)
	return (1 << B) - 1
}
