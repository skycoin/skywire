package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func (d *Decoder) decodeHybridSpectrum(qextPayload []byte, rd *rangecoding.Decoder, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, channels int, disableInv bool, energies []celtGLog, prev1LogE, prev2LogE []celtGLog, pulses, fineQuant, finePriority, tfRes []int32, intensity, dualStereo, balance, codedBands int) (coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) {
	d.decodeFineEnergyGLogRange(energies, start, end, nil, fineQuant)
	if extsupport.QEXT {
		qext = d.prepareQEXTDecodeRange(qextPayload, rd, start, end, lm, frameSize)
	}
	if extsupport.QEXT && qext != nil {
		oldRD := d.rangeDecoder
		d.rangeDecoder = qext.dec
		d.decodeFineEnergyGLogRange(energies, start, end, fineQuant, qext.extraQuant)
		d.rangeDecoder = oldRD
	}

	var extDec *rangecoding.Decoder
	var extPulses []int32
	extTotalBitsQ3 := 0
	if extsupport.QEXT && qext != nil {
		extDec = qext.dec
		extPulses = qext.extraPulses[:end]
		extTotalBitsQ3 = qext.totalBitsQ3
	}
	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, disableInv, &d.rng, &d.scratchBands,
		extDec, extPulses, extTotalBitsQ3)
	if extsupport.QEXT && qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, disableInv, qext)
	}

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	// Hybrid finalisation only runs over the decoded CELT tail bands.
	if extsupport.QEXT && qext != nil {
		d.decodeEnergyFinaliseGLogRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.decodeEnergyFinaliseGLogRange(start, end, energies, fineQuant, finePriority, bitsLeft)
	}

	if antiCollapseOn {
		antiCollapseGLog(coeffsL, coeffsR, collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	return coeffsL, coeffsR, qext
}

func (d *Decoder) synthesizeHybridDecodedFrame(frameSize, modeLM, end, hybridBinStart, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float32, postfilterTapset int, energies []celtGLog, coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) []float32 {
	var samples []float32
	downsample := d.downsampleFactor()
	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		var specL []float32
		var specR []float32
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
			denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, modeLM, qext.cfg.EBands, downsample)
			}
			if qext.coeffsR != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, modeLM, qext.cfg.EBands, downsample)
			}
		} else {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
			denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
			for i := 0; i < hybridBinStart && i < len(specL); i++ {
				specL[i] = 0
			}
			for i := 0; i < hybridBinStart && i < len(specR); i++ {
				specR[i] = 0
			}
		}
		if !transient && len(d.directOutPCM) >= frameSize*2 {
			samplesL, samplesR := d.synthesizeStereoPlanarLongToFloat32(specL, specR)
			if d.postfilterGainOld == 0 && d.postfilterGain == 0 && postfilterGain == 0 {
				d.applyPostfilterNoGainStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			} else {
				d.applyPostfilterStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], samplesL[:frameSize], samplesR[:frameSize], 1.0/32768.0)
			return nil
		}
		samples = d.SynthesizeStereo(specL, specR, transient, shortBlocks)
	} else {
		var specL []float32
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, modeLM, qext.cfg.EBands, downsample)
			}
		} else {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
			for i := 0; i < hybridBinStart && i < len(specL); i++ {
				specL[i] = 0
			}
		}
		if !transient &&
			len(d.directOutPCM) >= frameSize &&
			d.postfilterGainOld == 0 &&
			d.postfilterGain == 0 &&
			postfilterGain == 0 {
			samplesF32 := d.synthesizeMonoLongToFloat32(specL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
			return nil
		}
		samples = d.Synthesize(specL, transient, shortBlocks)
	}

	d.applyPostfilterFloat32(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	return samples
}
