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
	downsample := d.downsampleFactor()
	withQEXT := extsupport.QEXT && qext != nil && qext.end > 0
	specL := ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
	var specR []float32
	if d.channels == 2 {
		specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
	}
	for c := range int(d.channels) {
		spec := specL
		if c == 1 {
			spec = specR
		}
		coeffs, bandE := coeffsL, energies[:end]
		var qextCoeffs []celtNorm
		var qextE []celtGLog
		if withQEXT {
			qextCoeffs, qextE = qext.coeffsL, qext.energies[:qext.end]
		}
		if c == 1 {
			coeffs, bandE = coeffsR, energies[end:]
			if withQEXT {
				qextCoeffs, qextE = qext.coeffsR, qext.energies[qext.end:]
			}
		}
		denormalizeBandsPackedDownsampleIntoFloat32(spec, coeffs, bandE, HybridCELTStartBand, end, modeLM, EBands[:], downsample)
		if withQEXT {
			if qextCoeffs != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(spec, qextCoeffs, qextE, 0, qext.end, modeLM, qext.cfg.EBands, downsample)
			}
		} else {
			clear(spec[:min(hybridBinStart, len(spec))])
		}
	}
	return d.synthesizeFrame(specL, specR, frameSize, modeLM, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset)
}
