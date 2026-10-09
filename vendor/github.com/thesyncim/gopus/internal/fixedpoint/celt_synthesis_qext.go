//go:build gopus_fixed_point && gopus_qext

package fixedpoint

func (d *QEXTCELTDecoder) synthesisQEXT(x []int32, N, C, CC, lm int, transient, silence bool, qextEnd int, outSyn [][]int32) {
	// QEXT decoding consumes all signaled bands; celt_synthesis renders only
	// the bands present in this mode (two extension bands at 48 kHz).
	qextEnd = min(qextEnd, d.qextMaxBands)
	M := 1 << lm
	B, NB, shift := 1, N, d.maxLM-lm
	if transient {
		B, NB, shift = M, d.shortMDCTSize, d.maxLM
	}

	denormalise := func(src, dst, logE, qextLogE []int32, end int) {
		DenormaliseBands(src, dst, logE, d.eBands, d.shortMDCTSize, d.start, end, M, d.downsample, silence)
		if qextEnd == 0 {
			return
		}
		DenormaliseBands(src, dst, qextLogE, d.qextEdges, d.shortMDCTSize, 0, qextEnd, M, d.downsample, silence)
	}
	backward := func(freq []int32, dst []int32) {
		for b := 0; b < B; b++ {
			d.mdct.MDCTBackward(freq[b:], dst[NB*b:], d.window, d.overlap, shift, B, &d.mdctScratch)
		}
	}

	switch {
	case CC == 2 && C == 1:
		freq := d.freq[:N]
		denormalise(x[:N], freq, d.oldBandE, d.qextOldBandE, min(d.end, d.nbEBands))
		freq2 := outSyn[1][d.overlap/2:]
		copy(freq2[:N], freq)
		backward(freq2, outSyn[0])
		backward(freq, outSyn[1])
	case CC == 1 && C == 2:
		freq := d.freq[:N]
		freq2 := outSyn[0][d.overlap/2:]
		denormalise(x[:N], freq, d.oldBandE, d.qextOldBandE, min(d.end, d.nbEBands))
		denormalise(x[N:2*N], freq2[:N], d.oldBandE[d.nbEBands:], d.qextOldBandE[qextCELTMaxQEXTBands:], min(d.end, d.nbEBands))
		for i := 0; i < N; i++ {
			freq[i] = half32(freq[i]) + half32(freq2[i])
		}
		backward(freq, outSyn[0])
	default:
		for c := 0; c < CC; c++ {
			codedChannel := c
			if codedChannel >= C {
				codedChannel = C - 1
			}
			denormalise(x[codedChannel*N:(codedChannel+1)*N], d.freq[:N],
				d.oldBandE[codedChannel*d.nbEBands:], d.qextOldBandE[codedChannel*qextCELTMaxQEXTBands:], min(d.end, d.nbEBands))
			backward(d.freq[:N], outSyn[c])
		}
	}

	for c := 0; c < CC; c++ {
		for i := 0; i < N; i++ {
			outSyn[c][i] = saturateSig(outSyn[c][i])
		}
	}
}

func deemphasisQEXT(in [][]int32, out []int32, N, channels, downsample int, mem []int32,
	accum bool, coef0, coef1, coef3 int16,
) {
	apiSamples := N / downsample
	for c := 0; c < channels; c++ {
		m := mem[c]
		apiIndex := 0
		for i := 0; i < N; i++ {
			sig := in[c][i]
			tmp := saturateSig(sig + m)
			if coef1 != 0 {
				m = mult16x32q15(coef0, tmp) - mult16x32q15(coef1, sig)
				tmp = shl32(mult16x32q15(coef3, tmp), 2)
			} else {
				m = mult16x32q15(coef0, tmp)
			}
			if i%downsample == 0 && apiIndex < apiSamples {
				idx := apiIndex*channels + c
				if accum {
					out[idx] = add32(out[idx], sig2res(tmp))
				} else {
					out[idx] = sig2res(tmp)
				}
				apiIndex++
			}
		}
		mem[c] = m
	}
}

func (d *QEXTCELTDecoder) applyQEXTCombFilter(decodeMem [][]int32, N, lm, postfilterPitch int, postfilterGain int16, postfilterTapset int) {
	for c := 0; c < d.channels; c++ {
		period := d.postfilterPeriod
		if period < int32(celtCombFilterMinPeriod) {
			period = int32(celtCombFilterMinPeriod)
		}
		periodOld := d.postfilterPeriodOld
		if periodOld < int32(celtCombFilterMinPeriod) {
			periodOld = int32(celtCombFilterMinPeriod)
		}
		d.postfilterPeriod = period
		d.postfilterPeriodOld = periodOld
		base := d.decodeBufSize - N
		if qextCombFilterHistoryReady(base, int(periodOld), int(period), d.overlap) {
			CombFilterQEXT(decodeMem[c], decodeMem[c], base, int(periodOld), int(period), d.shortMDCTSize,
				d.postfilterGainOld, d.postfilterGain, int(d.postfilterTapsetOld), int(d.postfilterTapset),
				d.window, d.overlap)
		}
		if lm != 0 {
			secondBase := base + d.shortMDCTSize
			if qextCombFilterHistoryReady(secondBase, int(period), postfilterPitch, d.overlap) {
				CombFilterQEXT(decodeMem[c], decodeMem[c], secondBase, int(period), postfilterPitch,
					N-d.shortMDCTSize, d.postfilterGain, postfilterGain, int(d.postfilterTapset),
					postfilterTapset, d.window, d.overlap)
			}
		}
	}
}

func qextCombFilterHistoryReady(base, t0, t1, overlap int) bool {
	if overlap == 240 {
		// CombFilterQEXT uses a 2,048-sample interleaved history window for
		// 96 kHz decimation before entering its 48 kHz comb-filter kernel.
		return base >= 2*qextCombFilterMaxPeriod
	}
	if t0 < celtCombFilterMinPeriod {
		t0 = celtCombFilterMinPeriod
	}
	if t1 < celtCombFilterMinPeriod {
		t1 = celtCombFilterMinPeriod
	}
	return base >= max(t0, t1)+2
}
