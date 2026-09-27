package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeFrameWithDecoder decodes a frame using a pre-initialized range decoder.
// This is useful when the range decoder is shared with other layers (e.g., SILK in hybrid mode).
func (d *Decoder) DecodeFrameWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Keep transition/state behavior aligned with DecodeFrame().
	channels := int(d.channels)
	d.handleChannelTransition(channels)
	d.beginDecodedPacketPLCState()
	d.prepareMonoEnergyFromStereo()
	d.SetRangeDecoder(rd)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := 0
	prev1Energy, prev1LogE, prev2LogE := d.snapshotDecodeHistory()

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		return d.handleDecodedSilenceFrame(frameSize, lm, prev1Energy, rd), nil
	}

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	postfilterGain := header.postfilterGain
	postfilterPeriod := header.postfilterPeriod
	postfilterTapset := header.postfilterTapset
	transient := header.transient
	intra := header.intra
	shortBlocks := header.shortBlocks

	// Step 1: Decode coarse energy
	energies := d.decodeCoarseEnergyGLogInto(ensureGLogSlice(&d.scratchEnergies, end*channels), end, intra, lm)

	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, transient)
	tfRes := allocation.tfRes
	spread := allocation.spread
	antiCollapseRsv := allocation.antiCollapseRsv
	pulses := allocation.pulses
	fineQuant := allocation.fineQuant
	finePriority := allocation.finePriority
	intensity := allocation.intensity
	dualStereo := allocation.dualStereo
	balance := allocation.balance
	codedBands := allocation.codedBands

	spectrum := d.decodeFrameSpectrum(nil, rd, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, energies, fineQuant, finePriority, pulses, tfRes, intensity, dualStereo, balance, codedBands)
	coeffsL := spectrum.coeffsL
	coeffsR := spectrum.coeffsR
	if spectrum.antiCollapseOn {
		antiCollapseGLog(coeffsL, coeffsR, spectrum.collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}
	d.applyPendingPLCPrefilterAndFold()
	samples := d.synthesizeDecodedFrame(frameSize, mode.LM, end, lm, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset, energies, coeffsL, coeffsR, nil)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, transient, energies, prev1Energy, nil, rd); err != nil {
		return nil, err
	}
	return samples, nil
}

// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
// Bands 0-16 are covered by SILK; CELT only decodes bands 17-21.
const HybridCELTStartBand = 17

// DecodeFrameHybrid decodes a CELT frame for hybrid mode.
// In hybrid mode, CELT only decodes bands 17-21 (frequencies above ~8kHz).
// The range decoder should already have been partially consumed by SILK.
//
// Parameters:
//   - rd: Range decoder (SILK has already consumed its portion)
//   - frameSize: Expected output samples (480 or 960 for hybrid 10ms/20ms)
//
// Returns: PCM samples as float32 slice at 48kHz
//
// Implementation approach:
// - Decode all bands as usual but zero out bands 0-16 before synthesis
// - This ensures correct operation with the existing synthesis pipeline
// - Only bands 17-21 contribute to the output (high frequencies for hybrid)
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode), libopus celt/celt_decoder.c
func (d *Decoder) DecodeFrameHybrid(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	// Hybrid only supports 10ms (480) and 20ms (960) frames
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	d.SetRangeDecoder(rd)
	d.prepareMonoEnergyFromStereo()
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := HybridCELTStartBand
	prev1Energy, prev1LogE, prev2LogE := d.snapshotDecodeHistory()

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		channels := int(d.channels)
		silenceE := ensureGLogSlice(&d.scratchSilenceE, MaxBands*channels)
		fillSilenceGLog(silenceE)
		d.updateLogEGLog(silenceE, MaxBands, false)
		d.setPrevEnergyGLogWithPrev(prev1Energy, silenceE)
		d.clearFrameHistoryOutsideRange(start, end, channels)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, channels)
		return samples, nil
	}

	postfilterGain := float32(0)
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniformSmall(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = float32(0.09375) * float32(qg+1)
		}
		tell = rd.Tell()
	}

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	d.applyLossEnergySafety(intra, start, end, lm)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Initialize energies with previous state so bands below start are preserved.
	channels := int(d.channels)
	energies := ensureGLogSlice(&d.scratchEnergies, end*channels)
	for c := range channels {
		for band := 0; band < end; band++ {
			energies[c*end+band] = d.prevEnergy[c*MaxBands+band]
		}
	}
	d.decodeCoarseEnergyRangeGLog(start, end, intra, lm, energies)

	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, transient)
	tfRes := allocation.tfRes
	spread := allocation.spread
	antiCollapseRsv := allocation.antiCollapseRsv
	pulses := allocation.pulses
	fineQuant := allocation.fineQuant
	finePriority := allocation.finePriority
	intensity := allocation.intensity
	dualStereo := allocation.dualStereo
	balance := allocation.balance
	codedBands := allocation.codedBands

	coeffsL, coeffsR, qext := d.decodeHybridSpectrum(qextPayload, rd, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, channels, d.phaseInversionDisabled, energies, prev1LogE, prev2LogE, pulses, fineQuant, finePriority, tfRes, intensity, dualStereo, balance, codedBands)

	hybridBinStart := ScaledBandStart(HybridCELTStartBand, frameSize)
	d.applyPendingPLCPrefilterAndFold()
	samples := d.synthesizeHybridDecodedFrame(frameSize, mode.LM, end, hybridBinStart, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset, energies, coeffsL, coeffsR, qext)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, transient, energies, prev1Energy, qext, rd); err != nil {
		return nil, err
	}
	return samples, nil
}
