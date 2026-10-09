package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// Encoder band quantization for frames without a QEXT extension coder.
//
// These functions are quant_band_stereo(), quant_band(), quant_partition(),
// compute_theta(), alg_quant() and quant_band_n1() from libopus
// celt/bands.c and celt/vq.c with encode == 1 and no ext_ec. They run with the
// standard 48 kHz mode tables (cache_index50/cache_bits50, logN400) and a
// bandEncodeScratch, which quantAllBandsEncodeScratchWithMode checks before
// selecting them; every other configuration takes the general *WithExtBudget
// functions.

// quantBandN1Enc is quant_band_n1() on the encoder: one raw sign bit per
// channel while at least one whole bit remains.
func quantBandN1Enc(ctx *bandCtx, x, y []celtNorm, lowbandOut []celtNorm) int {
	quantBandN1EncChannel(ctx, x)
	if y != nil {
		quantBandN1EncChannel(ctx, y)
	}
	if len(lowbandOut) > 0 {
		// SHR32(X[0], 4) is a no-op in the float build.
		lowbandOut[0] = x[0]
	}
	return 1
}

func quantBandN1EncChannel(ctx *bandCtx, x []celtNorm) {
	sign := uint32(0)
	if ctx.remainingBits >= 1<<bitRes {
		if x[0] < 0 {
			sign = 1
		}
		ctx.re.EncodeRawBits(sign, 1)
		ctx.remainingBits -= 1 << bitRes
	}
	if ctx.resynth {
		v := celtNorm(1)
		if sign != 0 {
			v = -1
		}
		x[0] = v
	}
}

// computeThetaEnc is compute_theta() on the encoder without an extension
// coder: it measures the split angle, quantizes it with the theta_round bias
// of the stereo RDO trials, codes it, and applies the stereo mixing.
func computeThetaEnc(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	var traceState quantBandTraceState
	if celtQuantBandTraceEnabled {
		traceState = beginQuantThetaTrace(ctx, x, y, n, *b, B, B0, lm, stereo, *fill)
	}
	re := ctx.re
	pulseCap := LogN[ctx.band] + lm<<bitRes
	offset := pulseCap>>1 - qthetaOffset
	if stereo && n == 2 {
		offset = pulseCap>>1 - qthetaOffsetTwoPhase
	}
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && ctx.band >= ctx.intensity {
		qn = 1
	}

	tell := re.TellFrac()
	var ithetaQ30 int
	if stereo && ctx.rdoIthetaSet {
		// The rounded-up theta_rdo trial splits the same band input.
		ithetaQ30 = ctx.rdoItheta
	} else {
		ithetaQ30 = stereoIthetaQ30Norm(x, y, stereo)
		if stereo && ctx.thetaRound < 0 {
			ctx.rdoItheta, ctx.rdoIthetaSet = ithetaQ30, true
		}
	}
	rawIthetaQ30 := ithetaQ30
	itheta := ithetaQ30 >> 16
	inv := 0
	if qn != 1 {
		if !stereo || ctx.thetaRound == 0 {
			itheta = (itheta*qn + 8192) >> 14
			if !stereo && ctx.avoidSplitNoise && itheta > 0 && itheta < qn {
				unquantized := celtUdiv(itheta*16384, qn)
				delta := fracMul16((n-1)<<7, bitexactLog2tanTheta(unquantized))
				if delta > *b {
					itheta = qn
				} else if delta < -*b {
					itheta = 0
				}
			}
		} else {
			// theta_rdo trials bias the rounding away from the equal split.
			bias := -32767 / qn
			if itheta > 8192 {
				bias = 32767 / qn
			}
			down := min(max((itheta*qn+bias)>>14, 0), qn-1)
			if ctx.thetaRound < 0 {
				itheta = down
			} else {
				itheta = down + 1
			}
		}
		switch {
		case stereo && n > 2:
			const p0 = 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			if itheta <= x0 {
				fl := p0 * itheta
				re.Encode(uint32(fl), uint32(fl+p0), uint32(ft))
			} else {
				fl := (itheta - 1 - x0) + (x0+1)*p0
				re.Encode(uint32(fl), uint32(fl+1), uint32(ft))
			}
		case B0 > 1 || stereo:
			re.EncodeUniform(uint32(itheta), uint32(qn+1))
		default:
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			var fl, fs int
			if itheta <= qn>>1 {
				fs = itheta + 1
				fl = itheta * (itheta + 1) >> 1
			} else {
				fs = qn + 1 - itheta
				fl = ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
			}
			re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
		}
		itheta = celtUdiv(itheta*16384, qn)
		ithetaQ30 = itheta << 16
		if stereo {
			if itheta == 0 {
				intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
			} else {
				stereoSplit(x, y)
			}
		}
	} else if stereo {
		// Intensity stereo: the inversion flag comes from the raw angle.
		if itheta > 8192 && !ctx.disableInv {
			inv = 1
			for i := range y {
				y[i] = -y[i]
			}
		}
		intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			re.EncodeBit(inv, 2)
		} else {
			inv = 0
		}
		itheta = 0
		ithetaQ30 = 0
	}

	qalloc := re.TellFrac() - tell
	*b -= qalloc

	var imid, iside, delta int
	switch itheta {
	case 0:
		imid = 32767
		*fill &= (1 << B) - 1
		delta = -16384
	case 16384:
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	default:
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	*sctx = splitCtx{
		inv:       inv,
		imid:      imid,
		iside:     iside,
		delta:     delta,
		itheta:    itheta,
		ithetaQ30: ithetaQ30,
		qalloc:    qalloc,
	}
	if celtQuantBandTraceEnabled {
		finishQuantThetaTrace(&traceState, ctx, sctx, x, y, n, *b, *fill, qn, pulseCap, offset, rawIthetaQ30)
	}
}

// algQuantEnc is alg_quant() without refinement bits: spread rotation, PVQ
// search, index coding and, when the band is resynthesized, the normalized
// quantized shape written back into x.
func algQuantEnc(ctx *bandCtx, x []celtNorm, n, k, B int, gain opusVal16) int {
	s := ctx.encScratch
	x = x[:n:n]
	expRotationNorm(x, n, 1, B, k, ctx.spread)
	pulses, yy := opPVQSearchScratchNormWithInputMutation(x, k, &s.pvqIy, &s.pvqSignx, &s.pvqY, &s.pvqAbsX, true)
	index := encodePulsesFast32(pulses, n, k, &s.cwrsU)
	vSize := PVQ_V(n, k)
	if vSize == 0 {
		return 0
	}
	ctx.re.EncodeUniform(index, vSize)
	if !ctx.resynth {
		return extractCollapseMask(pulses, n, B)
	}
	cm := normalizeResidualKnownEnergyIntoAndCollapse32(x, pulses, gain, yy, B)
	expRotationNorm(x, n, -1, B, k, ctx.spread)
	return cm
}

// quantPartitionEnc is quant_partition() on the encoder: it splits the band
// while the bit budget exceeds the pulse cache, then codes each piece with
// alg_quant() or, with no pulses, resynthesizes it from the folding source.
func quantPartitionEnc(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int) int {
	if n == 1 {
		return 1
	}
	x = x[:n:n]

	cacheStart := int(cacheIndex50[(lm+1)*MaxBands+ctx.band])
	cacheValid := cacheStart >= 0 && cacheStart < len(cacheBits50) && pulseCacheLookup50.valid[cacheStart]
	maxBits := 0
	if cacheValid && lm != -1 {
		maxBits = int(pulseCacheLookup50.maxBits[cacheStart])
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		var sctx splitCtx
		computeThetaEnc(ctx, &sctx, x[:nHalf], y, nHalf, &b, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)
		delta := sctx.delta
		if B0 > 1 && sctx.itheta&0x3fff != 0 {
			if sctx.itheta > 8192 {
				delta -= delta >> (4 - lm)
			} else {
				delta = min(0, delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1, lowband2 []celtNorm
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			var traceContext quantBandTraceRestorePoint
			if celtQuantBandTraceEnabled {
				traceContext = saveQuantBandTraceContext()
			}
			cm = quantPartitionEnc(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill)
			if celtQuantBandTraceEnabled {
				restoreQuantBandTraceContext(traceContext)
				traceContext = saveQuantBandTraceContext()
			}
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionEnc(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B)
			if celtQuantBandTraceEnabled {
				restoreQuantBandTraceContext(traceContext)
			}
			cm |= scm << (B0 >> 1)
		} else {
			var traceContext quantBandTraceRestorePoint
			if celtQuantBandTraceEnabled {
				traceContext = saveQuantBandTraceContext()
			}
			cm = quantPartitionEnc(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B)
			if celtQuantBandTraceEnabled {
				restoreQuantBandTraceContext(traceContext)
				traceContext = saveQuantBandTraceContext()
			}
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			cm |= quantPartitionEnc(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill)
			if celtQuantBandTraceEnabled {
				restoreQuantBandTraceContext(traceContext)
			}
		}
		return cm
	}

	q := 0
	if b > 0 {
		var currBits int
		if cacheValid {
			cache := cacheBits50[cacheStart:]
			q = int(pulseCacheLookup50.lut[cacheStart][min(b-1, pulseCacheLookupBits-1)])
			if q > 0 {
				currBits = int(cache[q]) + 1
			}
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = 0
				if q > 0 {
					currBits = int(cache[q]) + 1
				}
				ctx.remainingBits -= currBits
			}
		} else {
			q = ctx.bitsToPulses(lm, b)
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = ctx.pulsesToBits(lm, q)
				ctx.remainingBits -= currBits
			}
		}
	}
	if q != 0 {
		k := getPulses(q)
		var traceState quantBandTraceState
		if celtQuantBandTraceEnabled {
			traceState = beginQuantPVQTrace(ctx, x, n, k, ctx.spread, B, lm, gain, ctx.resynth)
		}
		cm := algQuantEnc(ctx, x, n, k, B, gain)
		if celtQuantBandTraceEnabled {
			finishQuantPVQTrace(&traceState, ctx, x, n, cm)
		}
		return cm
	}
	if !ctx.resynth {
		return fill
	}
	cmMask := (1 << B) - 1
	fill &= cmMask
	if fill == 0 {
		clear(x)
		return 0
	}
	var seedPtr *uint32
	if ctx.seedActive {
		seedPtr = &ctx.seed
	}
	if lowband == nil {
		if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					// The signed seed shifts arithmetically, as in libopus.
					x[i] = celtNorm(int32(ctx.seed) >> 20)
				}
			}
			renormalizeVector(x, gain)
		}
		return cmMask
	}
	if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
		if ctx.seedActive {
			for i := range x {
				ctx.seed = ctx.seed*1664525 + 1013904223
				tmp := float32(1.0 / 256.0)
				if ctx.seed&0x8000 == 0 {
					tmp = -tmp
				}
				if i < len(lowband) {
					x[i] = celtNorm(float32(lowband[i]) + tmp)
				} else {
					x[i] = celtNorm(tmp)
				}
			}
		}
		renormalizeVector(x, gain)
	}
	return fill
}

// quantBandEnc is quant_band() on the encoder: the time-frequency changes and
// Hadamard deinterleave around quant_partition(), undone again when the band
// is resynthesized.
func quantBandEnc(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int) int {
	if n == 1 {
		return quantBandN1Enc(ctx, x, nil, lowbandOut)
	}
	x = x[:n:n]

	N0 := n
	N_B := celtUdivBlocks(n, B)
	longBlocks := B == 1
	tfChange := ctx.tfChange
	recombine := max(tfChange, 0)

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	for k := range recombine {
		haar1(x, n>>k, 1<<k)
		if lowband != nil {
			haar1Norm(lowband, n>>k, 1<<k)
		}
		fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		haar1(x, N_B, B)
		if lowband != nil {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		x = ctx.encScratch.ensureQuantWork(n)
		deinterleaveHadamardInto(x, xOrig, N_B>>recombine, B0<<recombine, longBlocks)
		if lowband != nil {
			deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, nil, ctx.encScratch)
		}
	}

	var traceContext quantBandTraceRestorePoint
	if celtQuantBandTraceEnabled {
		traceContext = saveQuantBandTraceContext()
	}
	cm := quantPartitionEnc(ctx, x, n, b, B, lowband, lm, gain, fill)
	if celtQuantBandTraceEnabled {
		restoreQuantBandTraceContext(traceContext)
	}

	if !ctx.resynth {
		return cm
	}
	if B0 > 1 {
		interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
		x = xOrig
	}
	N_B = N_B0
	B = B0
	for range timeDivide {
		B >>= 1
		N_B <<= 1
		cm |= cm >> B
		haar1(x, N_B, B)
	}
	for k := range recombine {
		cm = bitDeinterleaveTable[cm&0xF]
		haar1(x, N0>>k, 1<<k)
	}
	B <<= recombine

	if len(lowbandOut) >= N0 {
		scaleLowbandOutForFoldingNorm(lowbandOut, x, N0)
	}
	return cm & ((1 << B) - 1)
}

// quantBandStereoEnc is quant_band_stereo() on the encoder.
func quantBandStereoEnc(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut, lowbandScratch []celtNorm, fill int) int {
	if n == 1 {
		return quantBandN1Enc(ctx, x, y, lowbandOut)
	}
	x = x[:n:n]
	y = y[:n:n]
	var bandTrace quantBandTraceState
	if celtQuantBandTraceEnabled {
		bandTrace = beginQuantBandOutputTrace(ctx, x, y, n, b, B, lm)
	}
	origFill := fill

	if ctx.bandE != nil {
		l := ctx.bandEnergy(0)
		r := ctx.bandEnergy(1)
		if l < 1e-10 || r < 1e-10 {
			if l > r {
				copy(y, x)
			} else {
				copy(x, y)
			}
		}
	}

	var sctx splitCtx
	computeThetaEnc(ctx, &sctx, x, y, n, &b, B, B, lm, true, &fill)
	var topThetaTraceContext quantBandTraceContext
	if celtQuantBandTraceEnabled {
		topThetaTraceContext = quantBandTraceCurrentContext()
		setQuantBandOutputTraceContext(&bandTrace, topThetaTraceContext)
	}
	mid, side := thetaSplitGains(&sctx, celtQEXTFloatMath)

	if n == 2 {
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits := b - sbits
		ctx.remainingBits -= sctx.qalloc + sbits

		x2, y2 := x, y
		if sctx.itheta > 8192 {
			x2, y2 = y, x
		}
		sign := float32(1)
		if sbits > 0 {
			bit := uint32(0)
			if float32(x2[0])*float32(y2[1])-float32(x2[1])*float32(y2[0]) < 0 {
				bit = 1
				sign = -1
			}
			ctx.re.EncodeRawBits(bit, 1)
		}
		cm := quantBandEnc(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill)
		y2[0] = celtNorm(-sign * float32(x2[1]))
		y2[1] = celtNorm(sign * float32(x2[0]))
		if ctx.resynth {
			x[0] = celtNorm(float32(mid) * float32(x[0]))
			x[1] = celtNorm(float32(mid) * float32(x[1]))
			y[0] = celtNorm(float32(side) * float32(y[0]))
			y[1] = celtNorm(float32(side) * float32(y[1]))
			tmp := float32(x[0])
			y0 := float32(y[0])
			x[0] = celtNorm(noFMA32Sub(tmp, y0))
			y[0] = celtNorm(noFMA32Add(tmp, y0))
			tmp = float32(x[1])
			y1 := float32(y[1])
			x[1] = celtNorm(noFMA32Sub(tmp, y1))
			y[1] = celtNorm(noFMA32Add(tmp, y1))
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		if celtQuantBandTraceEnabled {
			finishQuantBandOutputTrace(&bandTrace, ctx, x, y, n, cm)
		}
		return cm
	}

	mbits := max(0, min(b, (b-sctx.delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	var cm int
	if mbits >= sbits {
		var traceContext quantBandTraceRestorePoint
		if celtQuantBandTraceEnabled {
			traceContext = saveQuantBandTraceContext()
		}
		cm = quantBandEnc(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
		if celtQuantBandTraceEnabled {
			restoreQuantBandTraceContext(traceContext)
			traceContext = saveQuantBandTraceContext()
		}
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		cm |= quantBandEnc(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B)
		if celtQuantBandTraceEnabled {
			restoreQuantBandTraceContext(traceContext)
		}
	} else {
		var traceContext quantBandTraceRestorePoint
		if celtQuantBandTraceEnabled {
			traceContext = saveQuantBandTraceContext()
		}
		cm = quantBandEnc(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B)
		if celtQuantBandTraceEnabled {
			restoreQuantBandTraceContext(traceContext)
			traceContext = saveQuantBandTraceContext()
		}
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		cm |= quantBandEnc(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
		if celtQuantBandTraceEnabled {
			restoreQuantBandTraceContext(traceContext)
		}
	}

	if ctx.resynth {
		var mergeTrace quantBandTraceState
		if celtQuantBandTraceEnabled {
			mergeTrace = beginQuantStereoMergeTrace(ctx, x, y, n, B, lm, opusVal16(mid), topThetaTraceContext)
		}
		stereoMerge(x, y, opusVal16(mid))
		if celtQuantBandTraceEnabled {
			finishQuantStereoMergeTrace(&mergeTrace, ctx, x, y, n)
		}
		if sctx.inv != 0 {
			for i := range y {
				y[i] = -y[i]
			}
		}
	}
	if celtQuantBandTraceEnabled {
		finishQuantBandOutputTrace(&bandTrace, ctx, x, y, n, cm)
	}
	return cm
}

// quantBandStereoThetaRDOEnc is the theta_rdo branch of quant_all_bands() for
// the standard-mode encoder. It codes the band with theta rounded down in
// place, then codes it again from the saved coder state with theta rounded up
// into the trial buffers x1, y1 and out1, and keeps the trial whose output has
// the larger weighted inner product with the input, as libopus does. The
// rounded-up trial reads the folding source with the first trial's output in
// it, as it does in libopus, where the first trial writes norm before the
// second runs; only the winning outputs are copied.
func quantBandStereoThetaRDOEnc(ctx *bandCtx, re *rangecoding.Encoder, scratch *bandEncodeScratch,
	x, y []celtNorm, b, B int, lowband []celtNorm, lm int, lowbandOut, lowbandScratch []celtNorm,
	fill int, leftE, rightE celtEner) int {
	var rdoTrace quantBandTraceState
	if celtQuantBandTraceEnabled {
		rdoTrace = beginQuantRDOTrace(ctx, x, y, len(x), b, B, lm)
	}
	n := len(x)
	w0, w1 := computeChannelWeights(leftE, rightE)
	xSave := scratch.ensureXSave(n)
	ySave := scratch.ensureYSave(n)
	copy(xSave, x)
	copy(ySave, y)
	ecSave := &scratch.ecSave
	re.SaveStateShallowInto(ecSave)
	remainingSave, seedSave := ctx.remainingBits, ctx.seed

	ctx.thetaRound = -1
	cm0 := quantBandStereoEnc(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill)
	dist0 := thetaRDODistortion(w0, w1, xSave, x, ySave, y)
	var thetaContext0 quantBandTraceContext
	if celtQuantBandTraceEnabled {
		thetaContext0 = quantBandTraceLastBandOutputContext()
	}

	re.SaveStateSinceInto(&scratch.ecSave0, ecSave)
	remainingSave0, seedSave0 := ctx.remainingBits, ctx.seed
	re.RestoreStateShallow(ecSave)
	ctx.remainingBits, ctx.seed = remainingSave, seedSave

	x1 := scratch.ensureXResult0(n)
	y1 := scratch.ensureYResult0(n)
	copy(x1, xSave)
	copy(y1, ySave)
	var out1 []celtNorm
	if lowbandOut != nil {
		out1 = scratch.ensureNormResult0(n)
	}
	ctx.thetaRound = 1
	cm1 := quantBandStereoEnc(ctx, x1, y1, n, b, B, lowband, lm, out1, lowbandScratch, fill)
	dist1 := thetaRDODistortion(w0, w1, xSave, x1, ySave, y1)
	var thetaContext1 quantBandTraceContext
	if celtQuantBandTraceEnabled {
		thetaContext1 = quantBandTraceLastBandOutputContext()
	}
	ctx.thetaRound = 0
	ctx.rdoIthetaSet = false
	if dist0 >= dist1 {
		re.RestoreState(&scratch.ecSave0)
		ctx.remainingBits, ctx.seed = remainingSave0, seedSave0
		if celtQuantBandTraceEnabled {
			finishQuantRDOTrace(&rdoTrace, ctx, x, y, n, -1, dist0, dist1, thetaContext0)
		}
		return cm0
	}
	copy(x, x1)
	copy(y, y1)
	if out1 != nil {
		copy(lowbandOut, out1)
	}
	if celtQuantBandTraceEnabled {
		finishQuantRDOTrace(&rdoTrace, ctx, x, y, n, 1, dist0, dist1, thetaContext1)
	}
	return cm1
}
