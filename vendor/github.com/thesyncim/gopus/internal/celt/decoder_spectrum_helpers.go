package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type decodedFrameSpectrum struct {
	qext           *preparedQEXTDecode
	coeffsL        []celtNorm
	coeffsR        []celtNorm
	collapse       []byte
	antiCollapseOn bool
}

func (d *Decoder) decodeFrameSpectrum(qextPayload []byte, rd *rangecoding.Decoder, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv int, energies []celtGLog, fineQuant, finePriority, pulses, tfRes []int32, intensity, dualStereo, balance, codedBands int) decodedFrameSpectrum {
	spectrum := decodedFrameSpectrum{}

	d.decodeFineEnergyGLog(energies, end, nil, fineQuant)
	if extsupport.QEXT {
		spectrum.qext = d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	}
	if extsupport.QEXT && spectrum.qext != nil {
		d.decodeFineEnergyGLogWithDecoderPrev(spectrum.qext.dec, energies, end, fineQuant, spectrum.qext.extraQuant[:end])
	}

	var extDec *rangecoding.Decoder
	var extPulses []int32
	extTotalBitsQ3 := 0
	if extsupport.QEXT && spectrum.qext != nil {
		extDec = spectrum.qext.dec
		extPulses = spectrum.qext.extraPulses[:end]
		extTotalBitsQ3 = spectrum.qext.totalBitsQ3
	}
	if pm := d.perMode; pm != nil {
		spectrum.coeffsL, spectrum.coeffsR, spectrum.collapse = quantAllBandsDecodeWithScratchWithMode(rd, int(d.channels), frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.phaseInversionDisabled, &d.rng, &d.scratchBands,
			extDec, extPulses, extTotalBitsQ3, pm.eBands, pm.logN, pm.cacheIndex, pm.cacheBits)
	} else {
		spectrum.coeffsL, spectrum.coeffsR, spectrum.collapse = quantAllBandsDecodeWithScratch(rd, int(d.channels), frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.phaseInversionDisabled, &d.rng, &d.scratchBands,
			extDec, extPulses, extTotalBitsQ3)
	}
	if extsupport.QEXT && spectrum.qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, d.phaseInversionDisabled, spectrum.qext)
	}

	if antiCollapseRsv > 0 {
		spectrum.antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	if extsupport.QEXT && spectrum.qext != nil {
		d.decodeEnergyFinaliseGLogRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.decodeEnergyFinaliseGLog(energies, end, fineQuant, finePriority, bitsLeft)
	}

	return spectrum
}
