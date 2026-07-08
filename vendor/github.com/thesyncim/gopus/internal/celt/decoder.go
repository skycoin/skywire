package celt

import "github.com/thesyncim/gopus/internal/extsupport"

// DecodeFrame decodes a complete CELT frame from raw bytes.
// If data is nil, empty, or a single byte, performs Packet Loss Concealment (PLC) instead of decoding.
// data: raw CELT frame bytes (without Opus framing), or len <= 1 for PLC
// frameSize: expected output samples (120, 240, 480, or 960)
// Returns: PCM samples as float32 slice, interleaved if stereo
//
// The decoding pipeline:
// 1. Initialize range decoder
// 2. Decode frame header flags (silence, transient, intra)
// 3. Decode energy envelope (coarse + fine)
// 4. Compute bit allocation
// 5. Decode bands via PVQ
// 6. Synthesis: IMDCT + windowing + overlap-add
// 7. Apply de-emphasis filter
//
// Reference: RFC 6716 Section 4.3, libopus celt/celt_decoder.c celt_decode_with_ec()
func (d *Decoder) DecodeFrame(data []byte, frameSize int) ([]float32, error) {
	// Track channel count for transition detection (normal decode uses decoder's channels)
	channels := int(d.channels)
	d.handleChannelTransition(channels)
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}

	// Match libopus celt_decode_with_ec(): raw CELT payloads of length <= 1 are lost frames.
	if len(data) <= 1 {
		return d.decodePLC(frameSize)
	}

	setup, err := d.prepareDecodeFrame(data, frameSize)
	if err != nil {
		return nil, err
	}
	start := 0
	rd := setup.rd
	mode := setup.mode
	lm := setup.lm
	end := setup.end
	prev1Energy := setup.prev1Energy
	prev1LogE := setup.prev1LogE
	prev2LogE := setup.prev2LogE

	totalBits := len(data) * 8
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

	spectrum := d.decodeFrameSpectrum(qextPayload, rd, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, energies, fineQuant, finePriority, pulses, tfRes, intensity, dualStereo, balance, codedBands)
	qext := spectrum.qext
	coeffsL := spectrum.coeffsL
	coeffsR := spectrum.coeffsR
	if spectrum.antiCollapseOn {
		if pm := d.perMode; pm != nil {
			antiCollapseGLogMode(coeffsL, coeffsR, spectrum.collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng, pm.eBands, pm.nbEBands)
		} else {
			antiCollapseGLog(coeffsL, coeffsR, spectrum.collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
		}
	}
	d.applyPendingPLCPrefilterAndFold()
	samples := d.synthesizeDecodedFrame(frameSize, mode.LM, end, lm, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset, energies, coeffsL, coeffsR, qext)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, transient, energies, prev1Energy, qext, rd); err != nil {
		return nil, err
	}
	return samples, nil
}
