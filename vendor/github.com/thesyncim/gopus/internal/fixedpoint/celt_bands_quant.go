//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// This file ports the CELT FIXED_POINT band-shape decode (quant_all_bands
// decode path, QEXT off) from celt/bands.c. It fills the normalized celt_norm
// X[] (and stereo Y[]) buffers driven by AlgUnquant, performs recursive band
// splitting (compute_theta / quant_partition decode side), stereo split/merge,
// the spreading rotation, the Hadamard recombine/deinterleave, anti-collapse
// collapse-mask accumulation, and the lowband / lowband_out folding.
//
// Type model (celt/arch.h FIXED_POINT, QEXT off): celt_norm and opus_val32 are
// int32 (NORM_SHIFT = 24, NORM_SCALING = 1<<24), opus_val16 is int16, Q31ONE
// is 2^31-1, EPSILON is 1, BITRES is 3. The bit allocation (bits2pulses,
// get_pulses, balance) and CWRS/range pieces are plain integers shared with the
// float path and reused from the celt package via its exported helpers.
//
// The angle quantization step probabilities, the mid/side derivation, the
// rebalance logic and the fold/noise injection are reproduced exactly; only the
// celt_norm-typed arithmetic (the X buffers, renormalise_vector, the theta
// rotation, stereo_split/merge and lowband scaling) is integer.

const (
	normScaling = int32(1) << normShift // NORM_SCALING (1<<NORM_SHIFT)
	// q707Q31 is QCONST32(.70710678f, 31), used by stereo_split and haar1. The
	// libopus macro evaluates (opus_val32)(.5 + (.70710678f)*((opus_int64)1<<31))
	// with .70710678f as a float32 literal and the product taken in float, which
	// rounds to 1518500224 (not the double-precision 1518500247).
	q707Q31          = int32(1518500224)
	spreadAggressive = 3 // SPREAD_AGGRESSIVE
)

// bandDecCtx mirrors the decode-relevant fields of libopus struct band_ctx.
type bandDecCtx struct {
	dec             *rangecoding.Decoder
	spread          int
	tfChange        int
	remainingBits   int
	intensity       int
	band            int
	seed            uint32
	disableInv      bool
	avoidSplitNoise bool
}

// bandSplit mirrors the decode-relevant fields of libopus struct split_ctx.
type bandSplit struct {
	inv    int
	imid   int
	iside  int
	delta  int
	itheta int
	qalloc int
}

// fracMul16 ports FRAC_MUL16(a,b) = (16384 + a*b) >> 15, with both operands
// truncated to int16.
func fracMul16(a, b int) int {
	return int((16384 + int32(int16(a))*int32(int16(b))) >> 15)
}

// bitexactCos ports celt/bands.c bitexact_cos: a platform-independent cosine
// approximation whose exactness affects the bit allocation. x is opus_int16.
func bitexactCos(x int) int {
	tmp := (4096 + int32(int16(x))*int32(int16(x))) >> 13
	x2 := int(tmp)
	x2 = (32767 - x2) + fracMul16(x2, -7651+fracMul16(x2, 8277+fracMul16(-626, x2)))
	return int(int16(1 + x2))
}

// bitexactLog2tan ports celt/bands.c bitexact_log2tan.
func bitexactLog2tan(isin, icos int) int {
	lc := ilog32(uint32(icos))
	ls := ilog32(uint32(isin))
	icos <<= 15 - lc
	isin <<= 15 - ls
	return (ls-lc)*(1<<11) +
		fracMul16(isin, fracMul16(isin, -2597)+7932) -
		fracMul16(icos, fracMul16(icos, -2597)+7932)
}

func ilog32(x uint32) int {
	n := 0
	for x != 0 {
		n++
		x >>= 1
	}
	return n
}

// celtSudiv ports celt_sudiv for signed numerators (the denominator is always
// positive here).
func celtSudiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	if n < 0 {
		return -int(celtUdiv(uint32(-n), uint32(d)))
	}
	return int(celtUdiv(uint32(n), uint32(d)))
}

var computeQnExp2Table = [8]int{16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048}

// computeQn ports celt/bands.c compute_qn.
func computeQn(n, b, offset, pulseCap int, stereo bool) int {
	n2 := 2*n - 1
	if stereo && n == 2 {
		n2--
	}
	qb := celtSudiv(b+n2*offset, n2)
	qb = imin(b-pulseCap-(4<<bitRes), qb)
	qb = imin(8<<bitRes, qb)
	if qb < (1 << bitRes >> 1) {
		return 1
	}
	qn := computeQnExp2Table[qb&0x7] >> (14 - (qb >> bitRes))
	qn = (qn + 1) >> 1 << 1
	return qn
}

const (
	qthetaOffset         = 4
	qthetaOffsetTwophase = 16
)

// computeThetaDecode ports the decode side of celt/bands.c compute_theta (QEXT
// off). It decodes the split angle itheta from the range coder, updates *b and
// fill, and fills sctx with imid/iside/delta/itheta/qalloc. For the decode path
// the encode-only branches (stereo_itheta, intensity_stereo, stereo_split,
// theta_round bias) are not taken.
func computeThetaDecode(ctx *bandDecCtx, sctx *bandSplit, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	dec := ctx.dec
	i := ctx.band

	pulseCap := int(celt.LogN[i]) + lm*(1<<bitRes)
	off := qthetaOffset
	if stereo && n == 2 {
		off = qthetaOffsetTwophase
	}
	offset := (pulseCap >> 1) - off
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && i >= ctx.intensity {
		qn = 1
	}

	tell := dec.TellFrac()
	itheta := 0
	inv := 0
	if qn != 1 {
		// Entropy decoding of the angle: uniform for time split, a step for
		// stereo, triangular otherwise.
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			fs := int(dec.Decode(uint32(ft)))
			var x int
			if fs < (x0+1)*p0 {
				x = fs / p0
			} else {
				x = x0 + 1 + (fs - (x0+1)*p0)
			}
			var fl, flen int
			if x <= x0 {
				fl = p0 * x
				flen = p0 * (x + 1)
			} else {
				fl = (x - 1 - x0) + (x0+1)*p0
				flen = (x - x0) + (x0+1)*p0
			}
			dec.Update(uint32(fl), uint32(flen), uint32(ft))
			itheta = x
		} else if B0 > 1 || stereo {
			itheta = int(dec.DecodeUniform(uint32(qn + 1)))
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			fm := int(dec.Decode(uint32(ft)))
			var fl, fs int
			if fm < ((qn>>1)*((qn>>1)+1))>>1 {
				itheta = (int(opusmath.ISqrt32(uint32(8*fm+1))) - 1) >> 1
				fs = itheta + 1
				fl = itheta * (itheta + 1) >> 1
			} else {
				itheta = (2*(qn+1) - int(opusmath.ISqrt32(uint32(8*(ft-fm-1)+1)))) >> 1
				fs = qn + 1 - itheta
				fl = ft - ((qn+1-itheta)*(qn+2-itheta))>>1
			}
			dec.Update(uint32(fl), uint32(fl+fs), uint32(ft))
		}
		itheta = int(celtUdiv(uint32(itheta*16384), uint32(qn)))
	} else if stereo {
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			inv = dec.DecodeBit(2)
		} else {
			inv = 0
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
	}
	qalloc := dec.TellFrac() - tell
	*b -= qalloc

	var imid, iside, delta int
	switch itheta {
	case 0:
		imid = 32767
		iside = 0
		*fill &= (1 << B) - 1
		delta = -16384
	case 16384:
		imid = 0
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	default:
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tan(iside, imid))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.qalloc = qalloc
}

// stereoMerge ports celt/bands.c stereo_merge (FIXED_POINT path).
func stereoMerge(x, y []int32, mid int32, n int) {
	xp := celtInnerProdNormShift(y, x, n)
	side := celtInnerProdNormShift(y, y, n)
	xp = mult32x32q31(mid, xp)
	midmid := shr32(mult32x32q31(mid, mid), 3)
	el := midmid + side - 2*xp
	er := midmid + side + 2*xp
	// QCONST32(6e-4f, 28) == 161061
	if er < 161061 || el < 161061 {
		copy(y[:n], x[:n])
		return
	}
	kl := int(CeltILog2(el)) >> 1
	kr := int(CeltILog2(er)) >> 1
	t := vshr32(el, (kl<<1)-29)
	lgain := CeltRsqrtNorm32(t)
	t = vshr32(er, (kr<<1)-29)
	rgain := CeltRsqrtNorm32(t)
	if kl < 7 {
		kl = 7
	}
	if kr < 7 {
		kr = 7
	}
	for j := 0; j < n; j++ {
		l := mult32x32q31(mid, x[j])
		r := y[j]
		x[j] = vshr32(mult32x32q31(lgain, sub32(l, r)), kl-15)
		y[j] = vshr32(mult32x32q31(rgain, add32(l, r)), kr-15)
	}
}

// haar1 ports celt/bands.c haar1 (FIXED_POINT path) over celt_norm int32.
func haar1(x []int32, n0, stride int) {
	n0 >>= 1
	for i := 0; i < stride; i++ {
		for j := 0; j < n0; j++ {
			tmp1 := mult32x32q31(q707Q31, x[stride*2*j+i])
			tmp2 := mult32x32q31(q707Q31, x[stride*(2*j+1)+i])
			x[stride*2*j+i] = add32(tmp1, tmp2)
			x[stride*(2*j+1)+i] = sub32(tmp1, tmp2)
		}
	}
}

var orderyTable = []int{
	1, 0,
	3, 0, 2, 1,
	7, 0, 4, 3, 6, 1, 5, 2,
	15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
}

func orderyForStride(stride int) []int {
	switch stride {
	case 2:
		return orderyTable[0:2]
	case 4:
		return orderyTable[2:6]
	case 8:
		return orderyTable[6:14]
	case 16:
		return orderyTable[14:30]
	default:
		return nil
	}
}

// deinterleaveHadamard ports celt/bands.c deinterleave_hadamard over int32.
// When scratch is non-nil the encoder-owned transpose buffer is reused.
func deinterleaveHadamard(x []int32, n0, stride int, hadamard bool, scratch *celtEncodeScratch) {
	n := n0 * stride
	var tmp []int32
	if scratch != nil {
		tmp = ensureInt32(&scratch.hadamardTmp, n)
	} else {
		tmp = make([]int32, n)
	}
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[ordery[i]*n0+j] = x[j*stride+i]
			}
		}
	} else {
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[i*n0+j] = x[j*stride+i]
			}
		}
	}
	copy(x[:n], tmp)
}

// interleaveHadamard ports celt/bands.c interleave_hadamard over int32.
// When scratch is non-nil the encoder-owned transpose buffer is reused.
func interleaveHadamard(x []int32, n0, stride int, hadamard bool, scratch *celtEncodeScratch) {
	n := n0 * stride
	var tmp []int32
	if scratch != nil {
		tmp = ensureInt32(&scratch.hadamardTmp, n)
	} else {
		tmp = make([]int32, n)
	}
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = x[ordery[i]*n0+j]
			}
		}
	} else {
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = x[i*n0+j]
			}
		}
	}
	copy(x[:n], tmp)
}

var bitInterleaveTable = [16]int{0, 1, 1, 1, 2, 3, 3, 3, 2, 3, 3, 3, 2, 3, 3, 3}
var bitDeinterleaveTable = [16]int{
	0x00, 0x03, 0x0C, 0x0F, 0x30, 0x33, 0x3C, 0x3F,
	0xC0, 0xC3, 0xCC, 0xCF, 0xF0, 0xF3, 0xFC, 0xFF,
}

// quantBandN1Decode ports celt/bands.c quant_band_n1 (decode side).
func quantBandN1Decode(ctx *bandDecCtx, x, y []int32, lowbandOut []int32) uint {
	stereo := y != nil
	cur := x
	for c := 0; ; c++ {
		sign := uint32(0)
		if ctx.remainingBits >= 1<<bitRes {
			sign = ctx.dec.DecodeRawBits(1)
			ctx.remainingBits -= 1 << bitRes
		}
		if sign != 0 {
			cur[0] = -normScaling
		} else {
			cur[0] = normScaling
		}
		cur = y
		if c+1 >= 1+b2i(stereo) {
			break
		}
	}
	if lowbandOut != nil {
		lowbandOut[0] = shr32(x[0], 4)
	}
	return 1
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// quantPartitionDecode ports celt/bands.c quant_partition (decode side, QEXT
// off). It recursively splits a mono partition, decoding the split angle and
// the PVQ codeword via AlgUnquant, and folds zero-pulse bands.
func quantPartitionDecode(ctx *bandDecCtx, x []int32, n, b, B int, lowband []int32, lm int, gain int32, fill int) uint {
	maxBits := 0
	if lm != -1 {
		maxBits = celt.MaxPulsesBitsExport(ctx.band, lm)
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		n >>= 1
		y := x[n:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		var sctx bandSplit
		computeThetaDecode(ctx, &sctx, n, &b, B, B0, lm, false, &fill)
		imid := sctx.imid
		iside := sctx.iside
		delta := sctx.delta
		itheta := sctx.itheta
		qalloc := sctx.qalloc
		mid := shl32(int32(imid), 16)
		side := shl32(int32(iside), 16)

		if B0 > 1 && (itheta&0x3fff) != 0 {
			if itheta > 8192 {
				delta -= delta >> (4 - lm)
			} else {
				delta = imin(0, delta+(n<<bitRes>>(5-lm)))
			}
		}
		mbits := imax(0, imin(b, (b-delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= qalloc

		var nextLowband2 []int32
		if lowband != nil {
			nextLowband2 = lowband[n:]
		}

		rebalance := ctx.remainingBits
		var cm uint
		if mbits >= sbits {
			cm = quantPartitionDecode(ctx, x[:n], n, mbits, B, lowband, lm, mult32x32q31(gain, mid), fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			cm |= quantPartitionDecode(ctx, y, n, sbits, B, nextLowband2, lm, mult32x32q31(gain, side), fill>>B) << (B0 >> 1)
		} else {
			cm = quantPartitionDecode(ctx, y, n, sbits, B, nextLowband2, lm, mult32x32q31(gain, side), fill>>B) << (B0 >> 1)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			cm |= quantPartitionDecode(ctx, x[:n], n, mbits, B, lowband, lm, mult32x32q31(gain, mid), fill)
		}
		return cm
	}

	// Basic no-split case.
	q := celt.BitsToPulsesExport(ctx.band, lm, b)
	currBits := celt.PulsesToBitsExport(ctx.band, lm, q)
	ctx.remainingBits -= currBits
	for ctx.remainingBits < 0 && q > 0 {
		ctx.remainingBits += currBits
		q--
		currBits = celt.PulsesToBitsExport(ctx.band, lm, q)
		ctx.remainingBits -= currBits
	}

	if q != 0 {
		k := celt.GetPulsesExport(q)
		return uint(AlgUnquant(x[:n], n, k, ctx.spread, B, ctx.dec, gain))
	}

	// No pulse: fill the band anyway.
	cmMask := uint(1<<B) - 1
	fill &= int(cmMask)
	if fill == 0 {
		clearInt32(x[:n])
		return 0
	}
	if lowband == nil {
		// Noise.
		for j := 0; j < n; j++ {
			ctx.seed = celtLcgRand(ctx.seed)
			x[j] = shl32(int32(ctx.seed)>>20, normShift-14)
		}
		RenormaliseVector(x[:n], n, gain)
		return cmMask
	}
	// Folded spectrum. QCONST16(1.0/256, NORM_SHIFT-4) == 1<<(NORM_SHIFT-4)/256.
	tmp := int32(1) << (normShift - 4) / 256
	for j := 0; j < n; j++ {
		ctx.seed = celtLcgRand(ctx.seed)
		t := tmp
		if ctx.seed&0x8000 == 0 {
			t = -tmp
		}
		x[j] = lowband[j] + t
	}
	RenormaliseVector(x[:n], n, gain)
	return uint(fill)
}

// quantBandDecode ports celt/bands.c quant_band (decode side, QEXT off).
func quantBandDecode(ctx *bandDecCtx, x []int32, n, b, B int, lowband []int32, lm int, lowbandOut []int32, gain int32, lowbandScratch []int32, fill int) uint {
	n0 := n
	nB := n
	B0 := B
	longBlocks := B0 == 1
	nB = int(celtUdiv(uint32(nB), uint32(B)))

	if n == 1 {
		return quantBandN1Decode(ctx, x, nil, lowbandOut)
	}

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((nB&1) == 0 && tfChange < 0) || B0 > 1) {
		copy(lowbandScratch[:n], lowband[:n])
		lowband = lowbandScratch
	}

	for k := 0; k < recombine; k++ {
		if lowband != nil {
			haar1(lowband, n>>k, 1<<k)
		}
		fill = bitInterleaveTable[fill&0xF] | bitInterleaveTable[fill>>4]<<2
	}
	B >>= recombine
	nB <<= recombine

	timeDivide := 0
	for (nB&1) == 0 && tfChange < 0 {
		if lowband != nil {
			haar1(lowband, nB, B)
		}
		fill |= fill << B
		B <<= 1
		nB >>= 1
		timeDivide++
		tfChange++
	}
	B0 = B
	nB0 := nB

	if B0 > 1 {
		if lowband != nil {
			deinterleaveHadamard(lowband, nB>>recombine, B0<<recombine, longBlocks, nil)
		}
	}

	cm := quantPartitionDecode(ctx, x, n, b, B, lowband, lm, gain, fill)

	// Resynthesis (decode is always resynth=1).
	if B0 > 1 {
		interleaveHadamard(x, nB>>recombine, B0<<recombine, longBlocks, nil)
	}
	nB = nB0
	B = B0
	for k := 0; k < timeDivide; k++ {
		B >>= 1
		nB <<= 1
		cm |= cm >> uint(B)
		haar1(x, nB, B)
	}
	for k := 0; k < recombine; k++ {
		cm = uint(bitDeinterleaveTable[cm&0xF])
		haar1(x, n0>>k, 1<<k)
	}
	B <<= recombine

	if lowbandOut != nil {
		// n = celt_sqrt(SHL32(EXTEND32(N0), 22)); lowband_out[j] = MULT16_32_Q15(n, X[j])
		nrm := int16(CeltSqrt(shl32(int32(n0), 22)))
		for j := 0; j < n0; j++ {
			lowbandOut[j] = mult16x32Q15(nrm, x[j])
		}
	}
	cm &= uint(1<<B) - 1
	return cm
}

// quantBandStereoDecode ports celt/bands.c quant_band_stereo (decode side,
// QEXT off).
func quantBandStereoDecode(ctx *bandDecCtx, x, y []int32, n, b, B int, lowband []int32, lm int, lowbandOut []int32, lowbandScratch []int32, fill int) uint {
	if n == 1 {
		return quantBandN1Decode(ctx, x, y, lowbandOut)
	}
	origFill := fill

	var sctx bandSplit
	computeThetaDecode(ctx, &sctx, n, &b, B, B, lm, true, &fill)
	inv := sctx.inv
	imid := sctx.imid
	iside := sctx.iside
	delta := sctx.delta
	itheta := sctx.itheta
	qalloc := sctx.qalloc
	mid := shl32(int32(imid), 16)
	side := shl32(int32(iside), 16)

	var cm uint
	if n == 2 {
		mbits := b
		sbits := 0
		if itheta != 0 && itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := 0
		if itheta > 8192 {
			c = 1
		}
		ctx.remainingBits -= qalloc + sbits

		x2 := x
		y2 := y
		if c != 0 {
			x2, y2 = y, x
		}
		sign := 0
		if sbits != 0 {
			sign = int(ctx.dec.DecodeRawBits(1))
		}
		sign = 1 - 2*sign
		cm = quantBandDecode(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, q31One, lowbandScratch, origFill)
		y2[0] = int32(-sign) * x2[1]
		y2[1] = int32(sign) * x2[0]
		// Resynthesis N=2.
		x[0] = mult32x32q31(mid, x[0])
		x[1] = mult32x32q31(mid, x[1])
		y[0] = mult32x32q31(side, y[0])
		y[1] = mult32x32q31(side, y[1])
		tmp := x[0]
		x[0] = sub32(tmp, y[0])
		y[0] = add32(tmp, y[0])
		tmp = x[1]
		x[1] = sub32(tmp, y[1])
		y[1] = add32(tmp, y[1])
	} else {
		mbits := imax(0, imin(b, (b-delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= qalloc

		rebalance := ctx.remainingBits
		if mbits >= sbits {
			cm = quantBandDecode(ctx, x, n, mbits, B, lowband, lm, lowbandOut, q31One, lowbandScratch, fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			cm |= quantBandDecode(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
		} else {
			cm = quantBandDecode(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			cm |= quantBandDecode(ctx, x, n, mbits, B, lowband, lm, lowbandOut, q31One, lowbandScratch, fill)
		}
	}

	// Resynthesis.
	if n != 2 {
		stereoMerge(x, y, mid, n)
	}
	if inv != 0 {
		for j := 0; j < n; j++ {
			y[j] = -y[j]
		}
	}
	return cm
}

func clearInt32(x []int32) {
	for i := range x {
		x[i] = 0
	}
}

// QuantAllBandsDecode ports celt/bands.c quant_all_bands (decode, QEXT off). It
// fills the normalized celt_norm X (and stereo Y) buffers and per-band collapse
// masks from the range decoder. The bit allocation (pulses), the time-frequency
// resolution (tfRes), the per-band balance and the coded-band count come from
// the decoder prologue; the mode tables are the static 48000/960 mode shared
// with the float path. X and Y must be length M*shortMdctSize per channel;
// collapse has length channels*nbEBands.
//
// totalBitsQ3 is len*(8<<BITRES)-anti_collapse_rsv (the value libopus passes as
// total_bits). seed threads celt_lcg_rand through the noise fill.
func QuantAllBandsDecode(dec *rangecoding.Decoder, channels, frameSize, lm, start, end int,
	pulses, tfRes []int, shortBlocks, spread, dualStereo, intensity, totalBitsQ3, balance, codedBands int,
	disableInv bool, seed *uint32) (left, right []int32, collapse []byte) {

	eBands := celt.EBands
	nbEBands := celt.MaxBands // 21
	M := 1 << lm
	B := 1
	if shortBlocks != 0 {
		B = M
	}

	left = make([]int32, frameSize)
	if channels == 2 {
		right = make([]int32, frameSize)
	}
	collapse = make([]byte, channels*nbEBands)

	normOffset := M * eBands[start]
	normLen := M*eBands[nbEBands-1] - normOffset
	if normLen < 0 {
		normLen = 0
	}
	norm := make([]int32, channels*normLen)
	var norm2 []int32
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	maxBand := M * (eBands[end] - eBands[end-1])
	lowbandScratch := make([]int32, maxBand)

	ctx := bandDecCtx{
		dec:             dec,
		spread:          spread,
		intensity:       intensity,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
	}
	if seed != nil {
		ctx.seed = *seed
	}

	lowbandOffset := 0
	updateLowband := true

	for i := start; i < end; i++ {
		ctx.band = i
		last := i == end-1
		bandStart := eBands[i] * M
		bandEnd := eBands[i+1] * M
		nBand := bandEnd - bandStart

		x := left[bandStart:bandEnd]
		var yCh []int32
		if channels == 2 {
			yCh = right[bandStart:bandEnd]
		}

		tell := dec.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		if i <= codedBands-1 {
			currBalance := celtSudiv(balance, imin(3, codedBands-i))
			b = imax(0, imin(16383, imin(remaining+1, pulses[i]+currBalance)))
		}

		if (M*eBands[i]-nBand >= M*eBands[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFolding(norm, norm2, eBands[:], start, M, dualStereo != 0)
		}

		ctx.tfChange = tfRes[i]

		effectiveLowband := -1
		var xCM, yCM uint
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = imax(0, M*eBands[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if M*eBands[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i || M*eBands[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= uint(collapse[fold*channels])
				yCM |= uint(collapse[fold*channels+channels-1])
			}
		} else {
			xCM = uint(1<<B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			limit := M*eBands[i] - normOffset
			for j := 0; j < limit; j++ {
				norm[j] = halfNorm(norm[j] + norm2[j])
			}
		}

		var lowbandX, lowbandY []int32
		if effectiveLowband != -1 {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}
		var lowbandOutX, lowbandOutY []int32
		if !last {
			outStart := M*eBands[i] - normOffset
			lowbandOutX = norm[outStart : outStart+nBand]
			if channels == 2 {
				lowbandOutY = norm2[outStart : outStart+nBand]
			}
		}

		if dualStereo != 0 {
			xCM = quantBandDecode(&ctx, x, nBand, b/2, B, lowbandX, lm, lowbandOutX, q31One, lowbandScratch, int(xCM))
			yCM = quantBandDecode(&ctx, yCh, nBand, b/2, B, lowbandY, lm, lowbandOutY, q31One, lowbandScratch, int(yCM))
		} else if channels == 2 {
			xCM = quantBandStereoDecode(&ctx, x, yCh, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, int(xCM|yCM))
			yCM = xCM
		} else {
			xCM = quantBandDecode(&ctx, x, nBand, b, B, lowbandX, lm, lowbandOutX, q31One, lowbandScratch, int(xCM|yCM))
			yCM = xCM
		}

		collapse[i*channels] = byte(xCM)
		collapse[i*channels+channels-1] = byte(yCM)
		balance += pulses[i] + tell

		updateLowband = b > (nBand << bitRes)
		ctx.avoidSplitNoise = false
	}
	if seed != nil {
		*seed = ctx.seed
	}
	return left, right, collapse
}

// halfNorm ports HALF32 over celt_norm: SHR32(x, 1) in the FIXED_POINT build.
func halfNorm(x int32) int32 {
	return x >> 1
}

// specialHybridFolding ports celt/bands.c special_hybrid_folding (non-draft).
func specialHybridFolding(norm, norm2 []int32, eBands []int, start, M int, dualStereo bool) {
	n1 := M * (eBands[start+1] - eBands[start])
	n2 := M * (eBands[start+2] - eBands[start+1])
	if n2 <= n1 {
		return
	}
	copy(norm[n1:n1+(n2-n1)], norm[2*n1-n2:2*n1-n2+(n2-n1)])
	if dualStereo {
		copy(norm2[n1:n1+(n2-n1)], norm2[2*n1-n2:2*n1-n2+(n2-n1)])
	}
}
