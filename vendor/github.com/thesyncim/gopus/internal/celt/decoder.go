package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeFrame decodes a CELT payload without Opus framing. frameSize is the
// per-channel sample count. Payloads of at most one byte request packet-loss
// concealment. The result is interleaved float32 PCM for stereo decoders.
func (d *Decoder) DecodeFrame(data []byte, frameSize int) ([]float32, error) {
	d.handleChannelTransition(int(d.channels))
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}

	// Match libopus celt_decode_with_ec(): raw CELT payloads of length <= 1 are lost frames.
	if len(data) <= 1 {
		return d.decodePLC(frameSize)
	}
	if !d.validFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	rd := &d.rangeDecoderScratch
	rd.Init(data)
	return d.decodeFrame(rd, frameSize, qextPayload)
}

// DecodeFrameWithDecoder decodes a CELT frame from an initialized range
// decoder, starting at band 0. Callers that need Hybrid band accumulation use
// [Decoder.AccumulateFrameHybridWithPacketStereo].
func (d *Decoder) DecodeFrameWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if !d.validFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	d.handleChannelTransition(int(d.channels))
	return d.decodeFrame(rd, frameSize, nil)
}

// decodeFrame is celt_decode_with_ec() for a received full-band frame whose
// stream channel count matches the decoder:
//  1. Decode the silence flag and frame header flags
//  2. Decode energy envelope (coarse + fine)
//  3. Compute bit allocation
//  4. Decode bands via PVQ
//  5. Synthesis: IMDCT + windowing + overlap-add, postfilter
//  6. De-emphasis
func (d *Decoder) decodeFrame(rd *rangecoding.Decoder, frameSize int, qextPayload []byte) ([]float32, error) {
	d.beginDecodedPacketPLCState()
	d.prepareMonoEnergyFromStereo()
	d.SetRangeDecoder(rd)

	mode := d.modeConfig(frameSize)
	lm := mode.LM
	end := d.effectiveEndBand(frameSize)
	start := 0
	channels := int(d.channels)
	prev1LogE, prev2LogE := d.prevLogE, d.prevLogE2

	totalBits := rd.StorageBits()
	silence := decodeSilenceFlag(rd, totalBits)
	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)

	energies := d.decodeCoarseEnergyGLogInto(ensureGLogSliceNoClear(&d.scratchEnergies, end*channels), end, header.intra, lm)
	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, header.transient)
	spectrum := d.decodeFrameSpectrum(qextPayload, rd, totalBits, frameSize, start, end, lm, header.shortBlocks, allocation.spread, allocation.antiCollapseRsv, energies,
		allocation.fineQuant, allocation.finePriority, allocation.pulses, allocation.tfRes, allocation.intensity, allocation.dualStereo, allocation.balance, allocation.codedBands)
	coeffsL := spectrum.coeffsL
	coeffsR := spectrum.coeffsR
	if d.synthTrace != nil {
		// decodeFrameSpectrum has finalized band energies; this snapshot is the
		// exact input to libopus's anti_collapse() boundary.
		d.synthTrace.captureAntiCollapsePre(coeffsL, coeffsR, channels, frameSize, spectrum.collapse, allocation.pulses, d.rng)
	}
	if spectrum.antiCollapseOn {
		if pm := d.perMode; pm != nil {
			antiCollapseGLogMode(coeffsL, coeffsR, spectrum.collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, allocation.pulses, d.rng, pm.eBands, pm.nbEBands)
		} else {
			antiCollapseGLog(coeffsL, coeffsR, spectrum.collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, allocation.pulses, d.rng)
		}
	}
	if d.synthTrace != nil {
		d.synthTrace.captureAntiCollapsePost(coeffsL, coeffsR, channels, frameSize)
	}
	if silence {
		applyDecodedSilence(energies, coeffsL, coeffsR, spectrum.qext)
	}
	d.applyPendingPLCPrefilterAndFold()
	samples := d.synthesizeDecodedFrame(frameSize, mode.LM, end, lm, header.shortBlocks, header.transient, header.postfilterPeriod, header.postfilterGain, header.postfilterTapset, energies, coeffsL, coeffsR, spectrum.qext)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, header.transient, energies, spectrum.qext, rd); err != nil {
		return nil, err
	}
	return samples, nil
}
