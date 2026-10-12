//go:build gopus_fixed_point && gopus_qext

package fixedpoint

const qextPLCPitchBufferSize = celtDecodeBufferSize / 2

// DecodeLost runs the ENABLE_QEXT celt_decode_lost path and fixed deemphasis.
// coreFrameSize is measured at the native CELT rate; out receives interleaved
// raw int24 opus_res samples at the decoder API rate.
func (d *QEXTCELTDecoder) DecodeLost(coreFrameSize int, out []int32) int {
	return d.decodeLost(coreFrameSize, out, false)
}

// DecodeLostAccum runs the QEXT CELT PLC path and adds its deemphasized output
// to the caller's existing opus_res samples, as hybrid CELT accumulation does.
func (d *QEXTCELTDecoder) DecodeLostAccum(coreFrameSize int, accum []int32) int {
	return d.decodeLost(coreFrameSize, accum, true)
}

func (d *QEXTCELTDecoder) decodeLost(coreFrameSize int, out []int32, accum bool) int {
	lm := -1
	for candidate := 0; candidate <= d.maxLM; candidate++ {
		if d.shortMDCTSize<<candidate == coreFrameSize {
			lm = candidate
			break
		}
	}
	if lm < 0 || coreFrameSize%d.downsample != 0 {
		return -2
	}
	apiFrameSize := coreFrameSize / d.downsample
	if len(out) < d.channels*apiFrameSize {
		return -2
	}
	if d.start < 0 || d.start >= d.nbEBands || d.end <= d.start || d.end > d.nbEBands {
		return -1
	}

	N, M := coreFrameSize, 1<<lm
	cc := d.channels
	memSize := d.decodeBufSize + d.overlap
	decodeMem := d.decodeRows[:cc]
	outSyn := d.synthesisRows[:cc]
	for c := 0; c < cc; c++ {
		decodeMem[c] = d.decodeMem[c*memSize : (c+1)*memSize]
		outSyn[c] = decodeMem[c][d.decodeBufSize-N:]
	}

	lossDuration := d.lossDuration
	periodic := d.plcDuration < 40 && d.start == 0 && !d.skipPLC
	if periodic {
		periodic = d.decodeLostPeriodicQEXT(N, lm, decodeMem)
	}
	if !periodic {
		d.decodeLostNoiseQEXT(N, lm, lossDuration, decodeMem, outSyn)
		d.lastFrameType = framePLCNoise
	} else {
		d.prefilterAndFold = true
		d.lastFrameType = framePLCPeriodic
	}
	d.lossDuration = min32(10000, lossDuration+int32(M))
	d.plcDuration = min32(10000, d.plcDuration+int32(M))

	d.lastRes = out[:cc*apiFrameSize]
	deemphasisQEXT(outSyn, d.lastRes, N, cc, d.downsample, d.preemphMem, accum,
		d.deemph0, d.deemph1, d.deemph3)
	return apiFrameSize
}

func (d *QEXTCELTDecoder) decodeLostNoiseQEXT(N, lm int, lossDuration int32, decodeMem, outSyn [][]int32) {
	effEnd := imax(d.start, imin(d.end, d.nbEBands))
	X := d.baseBands.x[:d.channels*N]
	clear(X)
	moveLen := d.decodeBufSize - N + d.overlap
	for c := 0; c < d.channels; c++ {
		copy(decodeMem[c][:moveLen], decodeMem[c][N:N+moveLen])
	}
	if d.prefilterAndFold {
		d.prefilterAndFoldQEXT(N, decodeMem)
	}

	decay := gconst05
	if lossDuration == 0 {
		decay = gconst15
	}
	for c := 0; c < d.channels; c++ {
		for band := d.start; band < d.end; band++ {
			idx := c*d.nbEBands + band
			d.oldBandE[idx] = max32(d.backgroundLogE[idx], d.oldBandE[idx]-decay)
		}
	}
	seed := d.rng
	for c := 0; c < d.channels; c++ {
		for band := d.start; band < effEnd; band++ {
			boffs := N*c + (int(d.eBands[band]) << lm)
			blen := (int(d.eBands[band+1]) - int(d.eBands[band])) << lm
			for j := 0; j < blen; j++ {
				seed = celtLcgRand(seed)
				X[boffs+j] = shl32(int32(seed)>>20, normShiftPLC-14)
			}
			RenormaliseVector(X[boffs:boffs+blen], blen, q31One)
		}
	}
	d.rng = seed
	d.synthesisQEXT(X, N, d.channels, d.channels, lm, false, false, 0, outSyn)
	d.applyQEXTCombFilter(decodeMem, N, lm, int(d.postfilterPeriod), d.postfilterGain, int(d.postfilterTapset))
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.prefilterAndFold = false
	d.skipPLC = true
}

func (d *QEXTCELTDecoder) decodeLostPeriodicQEXT(N, lm int, decodeMem [][]int32) bool {
	scale := d.decodeBufSize / qextCELTDecodeBufferSize48
	maxPeriod := celtMaxPeriod * scale
	var maxPitchLag int
	fade := q15One
	if d.lastFrameType == framePLCPeriodic {
		maxPitchLag = int(d.lastPitchIndex)
		fade = 26214
	} else {
		maxPitchLag = qextPLCPitchSearch(d.decodeRows[:d.channels], d.channels, d.decodeBufSize, scale)
	}
	base := d.decodeBufSize - N
	if base < maxPitchLag || base < celtLPCOrder {
		return false
	}
	if d.lastFrameType != framePLCPeriodic {
		d.lastPitchIndex = int32(maxPitchLag)
	}
	excLength := imin(2*maxPitchLag, maxPeriod)
	var excStorage [2*celtMaxPeriod + celtLPCOrder]int16
	exc := excStorage[:]
	const excOff = celtLPCOrder
	var firTmpStorage [2 * celtMaxPeriod]int16
	firTmp := firTmpStorage[:excLength]

	for c := 0; c < d.channels; c++ {
		buf := decodeMem[c]
		for i := 0; i < maxPeriod+celtLPCOrder; i++ {
			exc[excOff+i-celtLPCOrder] = sround16(buf[d.decodeBufSize-maxPeriod-celtLPCOrder+i], sigShift)
		}
		lpc := d.plcLPC[c*celtLPCOrder : (c+1)*celtLPCOrder]
		if d.lastFrameType != framePLCPeriodic {
			var ac [celtLPCOrder + 1]int32
			plcCeltAutocorr(exc[excOff:], ac[:], d.plcWindow[:d.overlap], d.overlap,
				celtLPCOrder, maxPeriod, nil)
			ac[0] += ac[0] >> 13
			for i := 1; i <= celtLPCOrder; i++ {
				ac[i] -= mult16x32q15(int16(2*i*i), ac[i])
			}
			plcCeltLPC(lpc, ac[:], celtLPCOrder)
			for {
				tmp := q15One
				sum := int32(1 << sigShift)
				for i := 0; i < celtLPCOrder; i++ {
					sum += int32(abs16(lpc[i]))
				}
				if sum < 65535 {
					break
				}
				for i := 0; i < celtLPCOrder; i++ {
					tmp = mult16x16q15(32440, tmp)
					lpc[i] = mult16x16q15(lpc[i], tmp)
				}
			}
		}

		plcCeltFir(exc, excOff+maxPeriod-excLength, lpc, firTmp, excLength, celtLPCOrder)
		copy(exc[excOff+maxPeriod-excLength:excOff+maxPeriod], firTmp)

		var decay int16
		{
			E1, E2 := int32(1), int32(1)
			shift := imax(0, 2*int(CeltILog2(plcCeltMaxabs16(exc[excOff+maxPeriod-excLength:excOff+maxPeriod])))-20)
			if scale == 2 {
				shift++
			}
			decayLength := excLength >> 1
			for i := 0; i < decayLength; i++ {
				e := exc[excOff+maxPeriod-decayLength+i]
				E1 += mult16x16(int32(e), int32(e)) >> shift
				e = exc[excOff+maxPeriod-2*decayLength+i]
				E2 += mult16x16(int32(e), int32(e)) >> shift
			}
			E1 = min32(E1, E2)
			decay = int16(CeltSqrt(FracDiv32(E1>>1, E2)))
		}

		copy(buf[:d.decodeBufSize-N], buf[N:N+d.decodeBufSize-N])
		extrapolationOffset := maxPeriod - maxPitchLag
		extrapolationLen := N + d.overlap
		attenuation := mult16x16q15(fade, decay)
		var S1 int32
		for i, j := 0, 0; i < extrapolationLen; i, j = i+1, j+1 {
			if j >= maxPitchLag {
				j -= maxPitchLag
				attenuation = mult16x16q15(attenuation, decay)
			}
			buf[d.decodeBufSize-N+i] = shl32(int32(mult16x16q15(attenuation, exc[excOff+extrapolationOffset+j])), sigShift)
			previous := sround16(buf[d.decodeBufSize-maxPeriod-N+extrapolationOffset+j], sigShift)
			// S1 uses SHR32(MULT16_16(...), 11), retaining the signed
			// arithmetic shift used by the fixed-point C path.
			S1 += mult16x16(int32(previous), int32(previous)) >> 11
		}
		var lpcMem [celtLPCOrder]int16
		for i := 0; i < celtLPCOrder; i++ {
			lpcMem[i] = sround16(buf[d.decodeBufSize-N-1-i], sigShift)
		}
		plcCeltIir(buf, d.decodeBufSize-N, lpc, extrapolationLen, celtLPCOrder, lpcMem[:])
		for i := 0; i < extrapolationLen; i++ {
			buf[d.decodeBufSize-N+i] = saturateSig(buf[d.decodeBufSize-N+i])
		}
		var S2 int32
		for i := 0; i < extrapolationLen; i++ {
			tmp := sround16(buf[d.decodeBufSize-N+i], sigShift)
			S2 += mult16x16(int32(tmp), int32(tmp)) >> 11
		}
		if !(S1 > S2>>2) {
			clear(buf[d.decodeBufSize-N : d.decodeBufSize-N+extrapolationLen])
		} else if S1 < S2 {
			ratio := int16(CeltSqrt(FracDiv32((S1>>1)+1, S2+1)))
			for i := 0; i < d.overlap; i++ {
				tmpG := q15One - mult16x16q15(d.plcWindow[i], q15One-ratio)
				buf[d.decodeBufSize-N+i] = mult16x32q15(tmpG, buf[d.decodeBufSize-N+i])
			}
			for i := d.overlap; i < extrapolationLen; i++ {
				buf[d.decodeBufSize-N+i] = mult16x32q15(ratio, buf[d.decodeBufSize-N+i])
			}
		}
	}
	return true
}

func (d *QEXTCELTDecoder) prefilterAndFoldQEXT(N int, decodeMem [][]int32) {
	var tmp [qextCELTOverlapMax]int32
	for c := 0; c < d.channels; c++ {
		buf := decodeMem[c]
		base := d.decodeBufSize - N
		if !qextCombFilterHistoryReady(base, int(d.postfilterPeriodOld), int(d.postfilterPeriod), d.overlap) {
			continue
		}
		CombFilterQEXTPF(tmp[:d.overlap], 0, buf, base,
			int(d.postfilterPeriodOld), int(d.postfilterPeriod), d.overlap,
			-d.postfilterGainOld, -d.postfilterGain,
			int(d.postfilterTapsetOld), int(d.postfilterTapset), nil, 0)
		for i := 0; i < d.overlap/2; i++ {
			buf[base+i] = mult16x32q15(d.plcWindow[i], tmp[d.overlap-1-i]) +
				mult16x32q15(d.plcWindow[d.overlap-i-1], tmp[i])
		}
	}
}

func qextPLCPitchSearch(decodeMem [][]int32, channels, decodeBufferSize, scale int) int {
	var pitchLP [qextPLCPitchBufferSize]int16
	length := decodeBufferSize / (2 * scale)
	qextPLCPitchDownsample(decodeMem, pitchLP[:length], length, channels, 2*scale)
	pitch := plcPitchSearch(pitchLP[plcPitchLagMax/2:length], pitchLP[:length],
		celtDecodeBufferSize-plcPitchLagMax, plcPitchLagMax-plcPitchLagMin)
	return (plcPitchLagMax - pitch) * scale
}

func qextPLCPitchDownsample(x [][]int32, xLP []int16, length, channels, factor int) {
	offset := factor / 2
	maxabs := CeltMaxabs32(x[0][:length*factor])
	if channels == 2 {
		maxabs = max32(maxabs, CeltMaxabs32(x[1][:length*factor]))
	}
	if maxabs < 1 {
		maxabs = 1
	}
	shift := int(CeltILog2(maxabs)) - 10
	if shift < 0 {
		shift = 0
	}
	if channels == 2 {
		shift++
	}
	for i := 1; i < length; i++ {
		xLP[i] = int16((x[0][factor*i-offset] >> (shift + 2)) +
			(x[0][factor*i+offset] >> (shift + 2)) + (x[0][factor*i] >> (shift + 1)))
	}
	xLP[0] = int16((x[0][offset] >> (shift + 2)) + (x[0][0] >> (shift + 1)))
	if channels == 2 {
		for i := 1; i < length; i++ {
			xLP[i] += int16((x[1][factor*i-offset] >> (shift + 2)) +
				(x[1][factor*i+offset] >> (shift + 2)) + (x[1][factor*i] >> (shift + 1)))
		}
		xLP[0] += int16((x[1][offset] >> (shift + 2)) + (x[1][0] >> (shift + 1)))
	}
	var ac [5]int32
	plcCeltAutocorr(xLP, ac[:], nil, 0, 4, length, nil)
	ac[0] += ac[0] >> 13
	for i := 1; i <= 4; i++ {
		ac[i] -= mult16x32q15(int16(2*i*i), ac[i])
	}
	var lpc [4]int16
	plcCeltLPC(lpc[:], ac[:], 4)
	tmp := q15One
	for i := range lpc {
		tmp = mult16x16q15(29491, tmp)
		lpc[i] = mult16x16q15(lpc[i], tmp)
	}
	var lpc2 [5]int16
	const c1 int16 = 26214
	lpc2[0] = lpc[0] + 3277
	lpc2[1] = lpc[1] + mult16x16q15(c1, lpc[0])
	lpc2[2] = lpc[2] + mult16x16q15(c1, lpc[1])
	lpc2[3] = lpc[3] + mult16x16q15(c1, lpc[2])
	lpc2[4] = mult16x16q15(c1, lpc[3])
	plcCeltFir5(xLP, lpc2[:], length)
}
