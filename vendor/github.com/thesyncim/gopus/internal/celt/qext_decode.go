package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

type preparedQEXTDecode struct {
	dec         *rangecoding.Decoder
	totalBitsQ3 int
	cfg         qextModeConfig
	end         int
	intensity   int
	dualStereo  int
	extraPulses []int32
	extraQuant  []int32
	energies    []celtGLog
	coeffsL     []celtNorm
	coeffsR     []celtNorm
}

func (d *Decoder) decodeCoarseEnergyIntoWithPrevState(dst []celtGLog, nbBands int, intra bool, lm int, prevState []celtGLog, prevStride int, rd *rangecoding.Decoder) []celtGLog {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := int(d.channels)
	needed := nbBands * channels
	if len(dst) < needed {
		dst = make([]celtGLog, needed)
	} else {
		dst = dst[:needed]
	}
	if rd == nil {
		return dst
	}
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() {
		d.rangeDecoder = oldRD
	}()

	var alpha, beta float32
	if intra {
		alpha = 0.0
		beta = float32(BetaIntra)
	} else {
		alpha = float32(AlphaCoef[lm])
		beta = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()
	prevBandEnergy := ensureFloat32Slice(&d.scratchPrevBandEnergy, channels)
	for i := range prevBandEnergy {
		prevBandEnergy[i] = 0
	}

	for band := 0; band < nbBands; band++ {
		for c := range channels {
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := min(2*band, 40)
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = d.decodeLaplace(fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			prevFrameEnergy := float32(0)
			idx := c*prevStride + band
			if idx >= 0 && idx < len(prevState) {
				prevFrameEnergy = float32(prevState[idx])
			}
			minEnergy := float32(-9.0 * DB6)
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}
			pred := alpha*prevFrameEnergy + prevBandEnergy[c]
			q := float32(qi) * float32(DB6)
			energy := pred + q

			dst[c*nbBands+band] = celtGLog(energy)
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	return dst
}

func (d *Decoder) storeQEXTEnergyState(energies []celtGLog, nbBands int) {
	channels := int(d.channels)
	if nbBands <= 0 || len(energies) < nbBands*channels {
		return
	}
	oldBandE := d.ensureQEXTOldBandE()
	for c := range channels {
		base := c * nbQEXTBands
		src := energies[c*nbBands : c*nbBands+nbBands]
		for band, energy := range src {
			oldBandE[base+band] = energy
		}
	}
}

func (d *Decoder) prepareQEXTDecode(payload []byte, mainRD *rangecoding.Decoder, end, lm, frameSize int) *preparedQEXTDecode {
	return d.prepareQEXTDecodeRange(payload, mainRD, 0, end, lm, frameSize)
}

func (d *Decoder) prepareQEXTDecodeRange(payload []byte, mainRD *rangecoding.Decoder, start, end, lm, frameSize int) *preparedQEXTDecode {
	if len(payload) == 0 || mainRD == nil || end <= 0 {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if start > end {
		start = end
	}

	qextState := d.ensureQEXTState()
	extDec := &qextState.rangeDecoderScratch
	extDec.Init(payload)
	channels := int(d.channels)
	hdr := decodeQEXTHeader(extDec, channels, len(payload))

	qext := &qextState.scratchDecode
	*qext = preparedQEXTDecode{
		dec:         extDec,
		totalBitsQ3: len(payload) * (8 << bitRes),
		extraPulses: ensureInt32Slice(&qextState.scratchPulses, MaxBands+nbQEXTBands),
		extraQuant:  ensureInt32Slice(&qextState.scratchFineQuant, MaxBands+nbQEXTBands),
	}

	var qextMode *qextModeConfig
	if end == MaxBands {
		if cfg, ok := computeQEXTModeConfig(int(d.sampleRate), qextShortMDCTSize(frameSize)); ok {
			qextEnd := min(hdr.EndBands, cfg.EffBands)
			if qextEnd > 0 {
				qext.cfg = cfg
				qext.end = qextEnd
				qext.intensity = min(hdr.Intensity, qext.end)
				if d.channels == 2 && hdr.DualStereo && qext.intensity != 0 {
					qext.dualStereo = 1
				}

				qext.energies = ensureGLogSlice(&qextState.scratchEnergies, qext.end*channels)
				qext.energies = qext.energies[:qext.end*channels]
				intra := extDec.Tell()+3 <= extDec.StorageBits() && extDec.DecodeBit(3) == 1
				qext.energies = d.decodeCoarseEnergyIntoWithPrevState(qext.energies, qext.end, intra, lm, d.ensureQEXTOldBandE(), nbQEXTBands, extDec)
				qextMode = &qext.cfg
			}
		}
	}

	budgetQ3 := max(qext.totalBitsQ3-mainRD.TellFrac()-1, 0)
	tellBeforeAlloc := extDec.TellFrac()
	computeQEXTExtraAllocationDecodeWithMode(start, end, qext.end, budgetQ3, channels, lm, extDec, qext.extraPulses, qext.extraQuant, qextMode)
	_ = tellBeforeAlloc
	return qext
}

func (d *Decoder) decodeQEXTBands(frameSize, lm, shortBlocks, spread int, disableInv bool, qext *preparedQEXTDecode) {
	if qext == nil || qext.dec == nil || qext.end <= 0 {
		return
	}
	extBalance := qext.totalBitsQ3 - qext.dec.TellFrac()
	fineQ3 := 0
	channels := int(d.channels)
	if qext.end > 1 {
		fineQ3 = channels * int(qext.extraQuant[MaxBands+1]<<bitRes)
	}
	for i := 0; i < qext.end; i++ {
		idx := MaxBands + i
		extBalance -= int(qext.extraPulses[idx])
		extBalance -= fineQ3
	}

	d.decodeFineEnergyGLogWithDecoder(qext.dec, qext.energies, qext.end, qext.extraQuant[MaxBands:MaxBands+qext.end])
	zeros := ensureInt32Slice(&d.scratchTFRes, qext.end)
	for i := 0; i < qext.end; i++ {
		zeros[i] = 0
	}
	qextState := d.ensureQEXTState()
	var dummyDec rangecoding.Decoder
	dummyDec.Init(nil)

	qext.coeffsL, qext.coeffsR, _ = quantAllBandsDecodeWithScratchWithMode(
		qext.dec,
		channels,
		frameSize,
		lm,
		0,
		qext.end,
		qext.extraPulses[MaxBands:MaxBands+qext.end],
		shortBlocks,
		spread,
		qext.dualStereo,
		qext.intensity,
		zeros[:qext.end],
		qext.totalBitsQ3,
		extBalance,
		qext.end,
		disableInv,
		&d.rng,
		&qextState.scratchBands,
		&dummyDec,
		zeros[:qext.end],
		0,
		qext.cfg.EBands,
		qext.cfg.LogN,
		qext.cfg.CacheIndex,
		qext.cfg.CacheBits,
	)
	d.storeQEXTEnergyState(qext.energies, qext.end)
}
