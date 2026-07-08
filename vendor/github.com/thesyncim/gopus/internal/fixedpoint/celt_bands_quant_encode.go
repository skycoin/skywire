//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// This file ports the CELT FIXED_POINT band-shape encode (quant_all_bands
// encode path, encode=1, QEXT off) from celt/bands.c. It is the mirror of the
// decode driver in celt_bands_quant.go: it drives AlgQuant (vs AlgUnquant),
// runs the encode-side compute_theta (the angle search via stereo_itheta, the
// quantize decision and the avoid_split_noise / theta_round bias), the
// quant_partition / quant_band / quant_band_stereo encode paths (mid/side
// gains, the N=2 stereo sign, the intensity/dual-stereo handling, spreading
// and folding), the theta rate-distortion optimisation (theta_rdo) used for
// stereo at complexity>=8, and the collapse-mask accumulation. All shared
// helpers (haar1, bitexactCos, computeQn, stereoMerge, interleave/deinterleave
// Hadamard, the fold/noise injection) come from celt_bands_quant.go.
//
// Type model (celt/arch.h FIXED_POINT, QEXT off) matches the decode file:
// celt_norm and opus_val32 are int32 (NORM_SHIFT = 24), opus_val16 is int16,
// int64 accumulators. The bandE energies passed in are celt_ener (opus_val32).

// bandEncCtx mirrors the encode-relevant fields of libopus struct band_ctx.
// It shares the layout intent of bandDecCtx but holds the encoder, the band
// energies and the theta_round state used by the encode-side compute_theta.
type bandEncCtx struct {
	enc             *rangecoding.Encoder
	bandE           []int32 // celt_ener[2*nbEBands], channel-major (per-band, then per-band+nbEBands)
	nbEBands        int
	spread          int
	tfChange        int
	remainingBits   int
	intensity       int
	band            int
	seed            uint32
	disableInv      bool
	avoidSplitNoise bool
	resynth         bool
	thetaRound      int

	// Reusable per-band scratch (encoder-owned). When non-nil, AlgQuant and the
	// Hadamard (de)interleave borrow these buffers instead of allocating.
	scratch *celtEncodeScratch
}

const minStereoEnergy = int32(2) // MIN_STEREO_ENERGY (FIXED_POINT)

// div32_16 ports DIV32_16(a,b): integer division narrowed to int16.
func div32_16(a int32, b int16) int16 {
	return int16(a / int32(b))
}

// celtAtanNorm ports celt/mathops.h celt_atan_norm (FIXED_POINT). Input x is
// Q31 in [-1,1]; output is Q30. It is arctan(x)*2/pi.
func celtAtanNorm(x int32) int32 {
	const (
		atan2OverPi int32 = 1367130551  // Q31
		a03         int32 = -715791936  // Q31
		a05         int32 = 857391616   // Q32
		a07         int32 = -1200579328 // Q33
		a09         int32 = 1682636672  // Q34
		a11         int32 = -1985085440 // Q35
		a13         int32 = 1583306112  // Q36
		a15         int32 = -598602432  // Q37
	)
	if x == 1073741824 {
		return 536870912
	}
	if x == -1073741824 {
		return -536870912
	}
	xQ31 := shl32(x, 1)
	xSqQ30 := mult32x32q31(xQ31, x)
	tmp := mult32x32q31(xSqQ30, a15)
	tmp = mult32x32q31(xSqQ30, add32(a13, tmp))
	tmp = mult32x32q31(xSqQ30, add32(a11, tmp))
	tmp = mult32x32q31(xSqQ30, add32(a09, tmp))
	tmp = mult32x32q31(xSqQ30, add32(a07, tmp))
	tmp = mult32x32q31(xSqQ30, add32(a05, tmp))
	tmp = mult32x32q31(xSqQ30, add32(a03, tmp))
	tmp = add32(x, mult32x32q31(xQ31, tmp))
	return mult32x32q31(atan2OverPi, tmp)
}

// celtAtan2pNorm ports celt/mathops.h celt_atan2p_norm (FIXED_POINT). Both x,y
// are Q30 in [0,1], non-negative, at least one non-zero; the result is Q30.
func celtAtan2pNorm(y, x int32) int32 {
	if y == 0 && x == 0 {
		return 0
	} else if y < x {
		return celtAtanNorm(shr32(FracDiv32(y, x), 1))
	}
	return 1073741824 - celtAtanNorm(shr32(FracDiv32(x, y), 1))
}

// stereoItheta ports celt/vq.c stereo_itheta (FIXED_POINT). It returns the
// split angle in Q30 (the caller takes >>16 to get the Q14 itheta).
func stereoItheta(x, y []int32, stereo bool, n int) int32 {
	var emid, eside int32
	if stereo {
		for i := 0; i < n; i++ {
			// m,s are celt_norm = PSHR32(X+/-Y, NORM_SHIFT-13); MAC16_16 keeps the
			// int16-truncated product.
			m := int16(pshr32(add32(x[i], y[i]), normShift-13))
			s := int16(pshr32(sub32(x[i], y[i]), normShift-13))
			emid = mac16x16(emid, int32(m), int32(m))
			eside = mac16x16(eside, int32(s), int32(s))
		}
	} else {
		emid += celtInnerProdNormShift(x, x, n)
		eside += celtInnerProdNormShift(y, y, n)
	}
	mid := CeltSqrt32(emid)
	side := CeltSqrt32(eside)
	return celtAtan2pNorm(side, mid)
}

// intensityStereo ports celt/bands.c intensity_stereo (FIXED_POINT). It folds
// Y into X using the per-band energies (X is overwritten; the side is not
// encoded). bandE indices i and i+nbEBands give the two channels.
func intensityStereo(ctx *bandEncCtx, x, y []int32, n int) {
	i := ctx.band
	left32 := ctx.bandE[i]
	right32 := ctx.bandE[i+ctx.nbEBands]
	shift := int(celtZlog2(maxI32(left32, right32))) - 13
	left := vshr32(left32, shift)
	right := vshr32(right32, shift)
	// norm = EPSILON + celt_sqrt(EPSILON + left^2 + right^2)
	norm := 1 + CeltSqrt(1+mult16x16(left, left)+mult16x16(right, right))
	left = minI32(left, norm-1)
	right = minI32(right, norm-1)
	a1 := div32_16(shl32(left, 15), int16(norm))
	a2 := div32_16(shl32(right, 15), int16(norm))
	for j := 0; j < n; j++ {
		x[j] = add32(mult16x32Q15(a1, x[j]), mult16x32Q15(a2, y[j]))
	}
}

// stereoSplit ports celt/bands.c stereo_split (FIXED_POINT).
func stereoSplit(x, y []int32, n int) {
	for j := 0; j < n; j++ {
		l := mult32x32q31(q707Q31, x[j])
		r := mult32x32q31(q707Q31, y[j])
		x[j] = add32(l, r)
		y[j] = sub32(r, l)
	}
}

func maxI32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func minI32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// computeThetaEncode ports the encode side of celt/bands.c compute_theta (QEXT
// off). It searches itheta from X/Y via stereo_itheta, quantizes it (with the
// avoid_split_noise / theta_round bias), encodes the angle into the range
// encoder, applies intensity_stereo / stereo_split to X/Y, and fills sctx.
func computeThetaEncode(ctx *bandEncCtx, sctx *bandSplit, x, y []int32, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	enc := ctx.enc
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

	// stereo_itheta search.
	ithetaQ30 := stereoItheta(x, y, stereo, n)
	itheta := int(ithetaQ30 >> 16)

	tell := enc.TellFrac()
	inv := 0
	if qn != 1 {
		if !stereo || ctx.thetaRound == 0 {
			itheta = (itheta*qn + 8192) >> 14
			if !stereo && ctx.avoidSplitNoise && itheta > 0 && itheta < qn {
				unquantized := int(celtUdiv(uint32(itheta*16384), uint32(qn)))
				imid := bitexactCos(unquantized)
				iside := bitexactCos(16384 - unquantized)
				delta := fracMul16((n-1)<<7, bitexactLog2tan(iside, imid))
				if delta > *b {
					itheta = qn
				} else if delta < -*b {
					itheta = 0
				}
			}
		} else {
			bias := -32767 / qn
			if itheta > 8192 {
				bias = 32767 / qn
			}
			down := imin(qn-1, imax(0, (itheta*qn+bias)>>14))
			if ctx.thetaRound < 0 {
				itheta = down
			} else {
				itheta = down + 1
			}
		}

		// Entropy coding of the angle.
		if stereo && n > 2 {
			p0 := 3
			xv := itheta
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			var fl, fh int
			if xv <= x0 {
				fl = p0 * xv
				fh = p0 * (xv + 1)
			} else {
				fl = (xv - 1 - x0) + (x0+1)*p0
				fh = (xv - x0) + (x0+1)*p0
			}
			enc.Encode(uint32(fl), uint32(fh), uint32(ft))
		} else if B0 > 1 || stereo {
			enc.EncodeUniform(uint32(itheta), uint32(qn+1))
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			var fs, fl int
			if itheta <= (qn >> 1) {
				fs = itheta + 1
				fl = itheta * (itheta + 1) >> 1
			} else {
				fs = qn + 1 - itheta
				fl = ft - ((qn+1-itheta)*(qn+2-itheta))>>1
			}
			enc.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
		}
		itheta = int(celtUdiv(uint32(itheta*16384), uint32(qn)))

		if stereo {
			if itheta == 0 {
				intensityStereo(ctx, x, y, n)
			} else {
				stereoSplit(x, y, n)
			}
		}
	} else if stereo {
		inv = b2i(itheta > 8192 && !ctx.disableInv)
		if inv != 0 {
			for j := 0; j < n; j++ {
				y[j] = -y[j]
			}
		}
		intensityStereo(ctx, x, y, n)
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			enc.EncodeBit(inv, 2)
		} else {
			inv = 0
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
	}
	qalloc := enc.TellFrac() - tell
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

// quantBandN1Encode ports celt/bands.c quant_band_n1 (encode side).
func quantBandN1Encode(ctx *bandEncCtx, x, y []int32, lowbandOut []int32) uint {
	stereo := y != nil
	cur := x
	for c := 0; ; c++ {
		sign := 0
		if ctx.remainingBits >= 1<<bitRes {
			sign = b2i(cur[0] < 0)
			ctx.enc.EncodeRawBits(uint32(sign), 1)
			ctx.remainingBits -= 1 << bitRes
		}
		if ctx.resynth {
			if sign != 0 {
				cur[0] = -normScaling
			} else {
				cur[0] = normScaling
			}
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

// quantPartitionEncode ports celt/bands.c quant_partition (encode side, QEXT
// off).
func quantPartitionEncode(ctx *bandEncCtx, x []int32, n, b, B int, lowband []int32, lm int, gain int32, fill int) uint {
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
		computeThetaEncode(ctx, &sctx, x[:n], y, n, &b, B, B0, lm, false, &fill)
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
			cm = quantPartitionEncode(ctx, x[:n], n, mbits, B, lowband, lm, mult32x32q31(gain, mid), fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			cm |= quantPartitionEncode(ctx, y, n, sbits, B, nextLowband2, lm, mult32x32q31(gain, side), fill>>B) << (B0 >> 1)
		} else {
			cm = quantPartitionEncode(ctx, y, n, sbits, B, nextLowband2, lm, mult32x32q31(gain, side), fill>>B) << (B0 >> 1)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			cm |= quantPartitionEncode(ctx, x[:n], n, mbits, B, lowband, lm, mult32x32q31(gain, mid), fill)
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
		return uint(AlgQuant(x[:n], n, k, ctx.spread, B, ctx.enc, gain, ctx.resynth, ctx.scratch))
	}

	// No pulse: fill the band anyway (only matters when resynth is on).
	if !ctx.resynth {
		return 0
	}
	cmMask := uint(1<<B) - 1
	fill &= int(cmMask)
	if fill == 0 {
		clearInt32(x[:n])
		return 0
	}
	if lowband == nil {
		for j := 0; j < n; j++ {
			ctx.seed = celtLcgRand(ctx.seed)
			x[j] = shl32(int32(ctx.seed)>>20, normShift-14)
		}
		RenormaliseVector(x[:n], n, gain)
		return cmMask
	}
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

// quantBandEncode ports celt/bands.c quant_band (encode side, QEXT off).
func quantBandEncode(ctx *bandEncCtx, x []int32, n, b, B int, lowband []int32, lm int, lowbandOut []int32, gain int32, lowbandScratch []int32, fill int) uint {
	n0 := n
	nB := n
	B0 := B
	longBlocks := B0 == 1
	nB = int(celtUdiv(uint32(nB), uint32(B)))

	if n == 1 {
		return quantBandN1Encode(ctx, x, nil, lowbandOut)
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
		haar1(x, n>>k, 1<<k)
		if lowband != nil {
			haar1(lowband, n>>k, 1<<k)
		}
		fill = bitInterleaveTable[fill&0xF] | bitInterleaveTable[fill>>4]<<2
	}
	B >>= recombine
	nB <<= recombine

	timeDivide := 0
	for (nB&1) == 0 && tfChange < 0 {
		haar1(x, nB, B)
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
		deinterleaveHadamard(x, nB>>recombine, B0<<recombine, longBlocks, ctx.scratch)
		if lowband != nil {
			deinterleaveHadamard(lowband, nB>>recombine, B0<<recombine, longBlocks, ctx.scratch)
		}
	}

	cm := quantPartitionEncode(ctx, x, n, b, B, lowband, lm, gain, fill)

	if ctx.resynth {
		if B0 > 1 {
			interleaveHadamard(x, nB>>recombine, B0<<recombine, longBlocks, ctx.scratch)
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
			nrm := int16(CeltSqrt(shl32(int32(n0), 22)))
			for j := 0; j < n0; j++ {
				lowbandOut[j] = mult16x32Q15(nrm, x[j])
			}
		}
		cm &= uint(1<<B) - 1
	}
	return cm
}

// quantBandStereoEncode ports celt/bands.c quant_band_stereo (encode side,
// QEXT off).
func quantBandStereoEncode(ctx *bandEncCtx, x, y []int32, n, b, B int, lowband []int32, lm int, lowbandOut []int32, lowbandScratch []int32, fill int) uint {
	if n == 1 {
		return quantBandN1Encode(ctx, x, y, lowbandOut)
	}
	origFill := fill

	// If either channel is below the minimum stereo energy, copy the louder
	// channel over the other so the (silent) side does not inject noise.
	if ctx.bandE[ctx.band] < minStereoEnergy || ctx.bandE[ctx.nbEBands+ctx.band] < minStereoEnergy {
		if ctx.bandE[ctx.band] > ctx.bandE[ctx.nbEBands+ctx.band] {
			copy(y[:n], x[:n])
		} else {
			copy(x[:n], y[:n])
		}
	}

	var sctx bandSplit
	computeThetaEncode(ctx, &sctx, x, y, n, &b, B, B, lm, true, &fill)
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
		c := b2i(itheta > 8192)
		ctx.remainingBits -= qalloc + sbits

		x2 := x
		y2 := y
		if c != 0 {
			x2, y2 = y, x
		}
		sign := 0
		if sbits != 0 {
			sign = b2i(mult32x32q31(x2[0], y2[1])-mult32x32q31(x2[1], y2[0]) < 0)
			ctx.enc.EncodeRawBits(uint32(sign), 1)
		}
		sign = 1 - 2*sign
		cm = quantBandEncode(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, q31One, lowbandScratch, origFill)
		y2[0] = int32(-sign) * x2[1]
		y2[1] = int32(sign) * x2[0]
		if ctx.resynth {
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
		}
	} else {
		mbits := imax(0, imin(b, (b-delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= qalloc

		rebalance := ctx.remainingBits
		if mbits >= sbits {
			cm = quantBandEncode(ctx, x, n, mbits, B, lowband, lm, lowbandOut, q31One, lowbandScratch, fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			cm |= quantBandEncode(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
		} else {
			cm = quantBandEncode(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			cm |= quantBandEncode(ctx, x, n, mbits, B, lowband, lm, lowbandOut, q31One, lowbandScratch, fill)
		}
	}

	if ctx.resynth {
		if n != 2 {
			stereoMerge(x, y, mid, n)
		}
		if inv != 0 {
			for j := 0; j < n; j++ {
				y[j] = -y[j]
			}
		}
	}
	return cm
}

// computeChannelWeights ports celt/bands.c compute_channel_weights
// (FIXED_POINT). It returns the per-channel distortion weights used by the
// theta_rdo decision.
func computeChannelWeights(ex, ey int32) (int16, int16) {
	minE := minI32(ex, ey)
	ex = add32(ex, minE/3)
	ey = add32(ey, minE/3)
	shift := int(CeltILog2(1+maxI32(ex, ey))) - 14
	return int16(vshr32(ex, shift)), int16(vshr32(ey, shift))
}

// QuantAllBandsEncode ports celt/bands.c quant_all_bands (encode, QEXT off). It
// drives the encode-side band quantization: it reads the normalized celt_norm
// X (and stereo Y) buffers and band energies, runs AlgQuant per band, writes
// the range-coded angles/codewords into enc, performs resynthesis back into
// X/Y when resynth is required (always for stereo theta_rdo at complexity>=8),
// and returns the per-band collapse masks.
//
// X and Y are modified in place (X/Y length frameSize per channel, channel
// major in the caller buffers). bandE is celt_ener[2*nbEBands] channel-major.
// totalBitsQ3 is the value libopus passes as total_bits. seed threads
// celt_lcg_rand through the noise fill.
func QuantAllBandsEncode(enc *rangecoding.Encoder, channels, frameSize, lm, start, end int,
	x, y []int32, bandE []int32,
	pulses, tfRes []int, shortBlocks, spread, dualStereo, intensity, totalBitsQ3, balance, codedBands int,
	complexity int, disableInv bool, seed *uint32, scratch *celtEncodeScratch) (collapse []byte) {

	eBands := celt.EBands
	nbEBands := celt.MaxBands // 21
	// effEBands == nbEBands for the static 48000/960 mode (the last band edge
	// equals shortMdctSize), so the i>=effEBands scratch redirect never fires.
	effEBands := celt.MaxBands
	M := 1 << lm
	B := 1
	if shortBlocks != 0 {
		B = M
	}

	thetaRdo := y != nil && dualStereo == 0 && complexity >= 8
	resynth := thetaRdo

	normOffset := M * eBands[start]
	normLen := M*eBands[nbEBands-1] - normOffset
	if normLen < 0 {
		normLen = 0
	}
	resynthAlloc := M * (eBands[end] - eBands[end-1])

	var norm, lowbandScratch []int32
	var xSave, ySave, xSave2, ySave2, normSave2 []int32
	if scratch != nil {
		collapse = ensureByteScratch(&scratch.qCollapse, channels*nbEBands)
		norm = ensureInt32(&scratch.qNorm, channels*normLen)
		lowbandScratch = ensureInt32(&scratch.qLowbandScratch, resynthAlloc)
		if thetaRdo {
			xSave = ensureInt32(&scratch.qXSave, resynthAlloc)
			ySave = ensureInt32(&scratch.qYSave, resynthAlloc)
			xSave2 = ensureInt32(&scratch.qXSave2, resynthAlloc)
			ySave2 = ensureInt32(&scratch.qYSave2, resynthAlloc)
			normSave2 = ensureInt32(&scratch.qNormSave2, resynthAlloc)
		}
	} else {
		collapse = make([]byte, channels*nbEBands)
		norm = make([]int32, channels*normLen)
		// libopus aliases lowband_scratch into X when resynth is off (it is never
		// written in that case); a dedicated buffer is equivalent and simpler.
		lowbandScratch = make([]int32, resynthAlloc)
		if thetaRdo {
			xSave = make([]int32, resynthAlloc)
			ySave = make([]int32, resynthAlloc)
			xSave2 = make([]int32, resynthAlloc)
			ySave2 = make([]int32, resynthAlloc)
			normSave2 = make([]int32, resynthAlloc)
		}
	}
	var norm2 []int32
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	ctx := bandEncCtx{
		enc:             enc,
		bandE:           bandE,
		nbEBands:        nbEBands,
		spread:          spread,
		intensity:       intensity,
		disableInv:      disableInv,
		resynth:         resynth,
		avoidSplitNoise: B > 1,
		scratch:         scratch,
	}
	if seed != nil {
		ctx.seed = *seed
	}

	lowbandOffset := 0
	updateLowband := true

	for i := start; i < end; i++ {
		ctx.band = i
		ctx.thetaRound = 0
		last := i == end-1
		bandStart := eBands[i] * M
		bandEnd := eBands[i+1] * M
		nBand := bandEnd - bandStart

		bandX := x[bandStart:bandEnd]
		var bandY []int32
		if channels == 2 {
			bandY = y[bandStart:bandEnd]
		}

		tell := enc.TellFrac()
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

		if resynth && (M*eBands[i]-nBand >= M*eBands[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFolding(norm, norm2, eBands[:], start, M, dualStereo != 0)
		}

		ctx.tfChange = tfRes[i]

		// Bands at/above effEBands point X/Y at the norm scratch and disable
		// lowband_scratch (libopus uses these only as scratch since the band is
		// past the coded range; the angle/pulse coding still runs).
		curScratch := lowbandScratch
		if i >= effEBands {
			bandX = norm
			if channels == 2 {
				bandY = norm
			}
			curScratch = nil
		}
		if last && !thetaRdo {
			curScratch = nil
		}

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
			if resynth {
				limit := M*eBands[i] - normOffset
				for j := 0; j < limit; j++ {
					norm[j] = halfNorm(norm[j] + norm2[j])
				}
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
			xCM = quantBandEncode(&ctx, bandX, nBand, b/2, B, lowbandX, lm, lowbandOutX, q31One, curScratch, int(xCM))
			yCM = quantBandEncode(&ctx, bandY, nBand, b/2, B, lowbandY, lm, lowbandOutY, q31One, curScratch, int(yCM))
		} else if channels == 2 {
			if thetaRdo && i < intensity {
				xCM = quantBandStereoThetaRDO(&ctx, bandX, bandY, nBand, b, B, lowbandX, lm, lowbandOutX, curScratch,
					int(xCM|yCM), i, start, last, bandE, norm, norm2, normOffset, M, dualStereo, eBands[:], normSave2,
					xSave, ySave, xSave2, ySave2)
			} else {
				ctx.thetaRound = 0
				xCM = quantBandStereoEncode(&ctx, bandX, bandY, nBand, b, B, lowbandX, lm, lowbandOutX, curScratch, int(xCM|yCM))
			}
			yCM = xCM
		} else {
			xCM = quantBandEncode(&ctx, bandX, nBand, b, B, lowbandX, lm, lowbandOutX, q31One, curScratch, int(xCM|yCM))
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
	return collapse
}

// quantBandStereoThetaRDO ports the theta_rdo branch of quant_all_bands: it
// encodes the band twice (theta rounded down then up), measures the weighted
// reconstruction distortion against the original X/Y, and keeps whichever
// rounding gives the lower distortion, restoring the encoder/seed/X/Y/norm
// state of the winning attempt. resynth is always on in this path.
func quantBandStereoThetaRDO(ctx *bandEncCtx, x, y []int32, n, b, B int, lowband []int32, lm int,
	lowbandOut []int32, lowbandScratch []int32, cm int, i, start int, last bool,
	bandE []int32, norm, norm2 []int32, normOffset, M int, dualStereo int, eBands []int, normSave2 []int32,
	xSave, ySave, xSave2, ySave2 []int32) uint {

	enc := ctx.enc
	w0, w1 := computeChannelWeights(bandE[i], bandE[ctx.nbEBands+i])

	startOffs := enc.Offs()
	ctxSave := *ctx
	var snapPre, snap2 rangecoding.EncoderSnapshot
	if ctx.scratch != nil {
		enc.SnapshotInto(&ctx.scratch.rdoSnapPre, startOffs)
		snapPre = ctx.scratch.rdoSnapPre
	} else {
		snapPre = enc.Snapshot(startOffs)
	}
	copy(xSave[:n], x[:n])
	copy(ySave[:n], y[:n])

	// Attempt 1: round down.
	ctx.thetaRound = -1
	cmDown := quantBandStereoEncode(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, cm)
	dist0 := add32(mult16x32Q15(w0, celtInnerProdNormShift(xSave[:n], x[:n], n)),
		mult16x32Q15(w1, celtInnerProdNormShift(ySave[:n], y[:n], n)))

	// Save attempt-1 result so we can restore it if it wins.
	ctxSave2 := *ctx
	if ctx.scratch != nil {
		enc.SnapshotInto(&ctx.scratch.rdoSnap2, startOffs)
		snap2 = ctx.scratch.rdoSnap2
	} else {
		snap2 = enc.Snapshot(startOffs)
	}
	copy(xSave2[:n], x[:n])
	copy(ySave2[:n], y[:n])
	var normOutStart int
	if !last {
		normOutStart = M*eBands[i] - normOffset
		copy(normSave2[:n], norm[normOutStart:normOutStart+n])
	}

	// Restore to the pre-encode state for attempt 2.
	*ctx = ctxSave
	enc.Restore(snapPre)
	copy(x[:n], xSave[:n])
	copy(y[:n], ySave[:n])
	if i == start+1 {
		specialHybridFolding(norm, norm2, eBands, start, M, dualStereo != 0)
	}

	// Attempt 2: round up.
	ctx.thetaRound = 1
	cmUp := quantBandStereoEncode(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, cm)
	dist1 := add32(mult16x32Q15(w0, celtInnerProdNormShift(xSave[:n], x[:n], n)),
		mult16x32Q15(w1, celtInnerProdNormShift(ySave[:n], y[:n], n)))

	if dist0 < dist1 {
		// Round up gives lower distortion; attempt 2's state is already live.
		return cmUp
	}
	// Round down wins (dist0 >= dist1): restore attempt 1's state.
	*ctx = ctxSave2
	enc.Restore(snap2)
	copy(x[:n], xSave2[:n])
	copy(y[:n], ySave2[:n])
	if !last {
		copy(norm[normOutStart:normOutStart+n], normSave2[:n])
	}
	return cmDown
}
