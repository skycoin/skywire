package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func packetChannelsFromStereoFlag(packetStereo bool) int {
	if packetStereo {
		return 2
	}
	return 1
}

// DecodeFrameWithPacketStereo decodes a CELT frame with explicit packet stereo flag.
// This handles the case where the packet's stereo flag differs from the decoder's configured channels.
func (d *Decoder) DecodeFrameWithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	channels := int(d.channels)
	d.handleChannelTransition(packetChannels)
	if packetChannels == channels {
		return d.DecodeFrame(data, frameSize)
	}
	if packetChannels == 1 && channels == 2 {
		return d.decodeMonoPacketToStereo(data, frameSize)
	}
	return d.decodeStereoPacketToMono(data, frameSize)
}

// DecodeFrameWithPacketStereoToFloat32 decodes frameSize samples per channel at
// the decoder's internal rate directly into out. Standard and downsampled modes
// use a 48 kHz internal rate; native 96 kHz HD mode uses 96 kHz.
func (d *Decoder) DecodeFrameWithPacketStereoToFloat32(data []byte, frameSize int, packetStereo bool, out []float32) error {
	outLen := frameSize * int(d.channels)
	if len(out) < outLen {
		return ErrOutputTooSmall
	}
	return d.decodeFrameInto(data, frameSize, packetStereo, out[:outLen], false)
}

// decodeFrameInto decodes a frame of frameSize internal-rate samples into out,
// the libopus pcm buffer. out holds either frameSize or frameSize/downsample
// interleaved frames; in the latter case the de-emphasis downsamples. With
// accum the frame is added onto out (libopus celt_accum).
func (d *Decoder) decodeFrameInto(data []byte, frameSize int, packetStereo bool, out []float32, accum bool) error {
	d.directOutPCM = out
	d.directOutAccum = accum
	defer func() {
		d.directOutPCM = nil
		d.directOutAccum = false
	}()
	_, err := d.DecodeFrameWithPacketStereo(data, frameSize, packetStereo)
	return err
}

// DecodeFrameAtAPIRate decodes a frame and returns PCM at the decoder's API
// sample rate, choosing packet stereo from the decoder channel count.
func (d *Decoder) DecodeFrameAtAPIRate(data []byte, frameSize int) ([]float32, error) {
	return d.DecodeFrameWithPacketStereoAtAPIRate(data, frameSize, d.channels == 2)
}

// DecodeFrameWithPacketStereoAtAPIRate decodes frameSize samples per channel at
// the decoder's API rate. Standard modes below 48 kHz decode a frameSize times
// downsampleFactor block at 48 kHz and downsample it; native 96 kHz HD mode
// decodes at 96 kHz with no downsampling.
func (d *Decoder) DecodeFrameWithPacketStereoAtAPIRate(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	downsample := d.downsampleFactor()
	if downsample <= 1 {
		return d.DecodeFrameWithPacketStereo(data, frameSize, packetStereo)
	}
	if frameSize <= 0 || frameSize*downsample/downsample != frameSize {
		return nil, ErrInvalidFrameSize
	}
	internalFrameSize := frameSize * downsample
	if !ValidFrameSize(internalFrameSize) {
		return nil, ErrInvalidFrameSize
	}
	channels := int(d.channels)
	outLen := frameSize * channels
	samples, err := d.DecodeFrameWithPacketStereo(data, internalFrameSize, packetStereo)
	if err != nil {
		return nil, err
	}
	out := make([]float32, outLen)
	copyDownsampledFloat32(out, samples, frameSize, channels, downsample)
	return out, nil
}

// DecodeFrameWithPacketStereoToFloat32AtAPIRate decodes into out at the
// decoder's API sample rate, downsampling the 48 kHz internal block when needed.
// Native 96 kHz HD mode writes its 96 kHz samples without downsampling.
func (d *Decoder) DecodeFrameWithPacketStereoToFloat32AtAPIRate(data []byte, frameSize int, packetStereo bool, out []float32) error {
	return d.decodeFrameAtAPIRate(data, frameSize, packetStereo, out, false)
}

// AccumulateFrameWithPacketStereoAtAPIRate decodes a frame at the decoder's
// API sample rate and adds it onto out (libopus celt_accum). opus_decode_frame
// uses it for the 2.5 ms CELT silence frame that fades out the CELT overlap on
// a Hybrid->SILK transition.
func (d *Decoder) AccumulateFrameWithPacketStereoAtAPIRate(data []byte, frameSize int, packetStereo bool, out []float32) error {
	// opus_decode_frame updates CELT's stream_channels control directly before
	// decoding the Hybrid->SILK fade frame. CELT_SET_CHANNELS only changes that
	// count in libopus; it does not run the mono-to-stereo history copy used by
	// the packet adapters. Mark the control value first so the shared adapter
	// does not synthesize an extra transition while accumulating this frame.
	d.prevStreamChannels = int32(packetChannelsFromStereoFlag(packetStereo))
	return d.decodeFrameAtAPIRate(data, frameSize, packetStereo, out, true)
}

func (d *Decoder) decodeFrameAtAPIRate(data []byte, frameSize int, packetStereo bool, out []float32, accum bool) error {
	downsample := d.downsampleFactor()
	if frameSize <= 0 || frameSize*downsample/downsample != frameSize {
		return ErrInvalidFrameSize
	}
	internalFrameSize := frameSize * downsample
	if !ValidFrameSize(internalFrameSize) {
		return ErrInvalidFrameSize
	}
	outLen := frameSize * int(d.channels)
	if len(out) < outLen {
		return ErrOutputTooSmall
	}
	return d.decodeFrameInto(data, internalFrameSize, packetStereo, out[:outLen], accum)
}

func copyDownsampledFloat32(dst []float32, src []float32, frameSize, channels, downsample int) {
	if frameSize <= 0 || channels <= 0 || downsample <= 0 {
		return
	}
	for i := range frameSize {
		srcBase := i * downsample * channels
		dstBase := i * channels
		for c := range channels {
			if srcBase+c >= len(src) || dstBase+c >= len(dst) {
				return
			}
			dst[dstBase+c] = src[srcBase+c]
		}
	}
}

// decodeMonoPacketToStereo decodes a mono packet and converts output to stereo.
func (d *Decoder) decodeMonoPacketToStereo(data []byte, frameSize int) ([]float32, error) {
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}
	if len(data) <= 1 {
		return d.decodePLC(frameSize)
	}
	if !d.validFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	origChannels := int(d.channels)
	d.channels = 1

	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

	mode := d.modeConfig(frameSize)
	lm := mode.LM
	end := d.effectiveEndBand(frameSize)
	start := 0

	bandStride := d.predStride()
	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergyGLog, bandStride)
	prev1LogE := d.prevLogE
	prev2LogE := d.prevLogE2
	for i := range bandStride {
		left := d.prevEnergy[i]
		if origChannels > 1 && len(d.prevEnergy) >= bandStride*2 {
			right := d.prevEnergy[bandStride+i]
			if right > left {
				left = right
			}
		}
		prev1Energy[i] = left
	}
	origPrevEnergy := d.prevEnergy
	d.prevEnergy = prev1Energy

	totalBits := len(data) * 8
	silence := decodeSilenceFlag(rd, totalBits)

	defer func() {
		d.channels = int32(origChannels)
		d.prevEnergy = origPrevEnergy
	}()

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	postfilterGain := header.postfilterGain
	postfilterPeriod := header.postfilterPeriod
	postfilterTapset := header.postfilterTapset
	transient := header.transient
	intra := header.intra
	shortBlocks := header.shortBlocks

	monoEnergies := d.decodeCoarseEnergyGLogInto(ensureGLogSlice(&d.scratchEnergies, end*int(d.channels)), end, intra, lm)

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

	d.decodeFineEnergyGLog(monoEnergies, end, nil, fineQuant)
	var qext *preparedQEXTDecode
	if extsupport.QEXT {
		qext = d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	}
	if extsupport.QEXT && qext != nil {
		d.decodeFineEnergyGLogWithDecoderPrev(qext.dec, monoEnergies, end, fineQuant, qext.extraQuant[:end])
	}

	var extDec *rangecoding.Decoder
	var extPulses []int32
	extTotalBitsQ3 := 0
	if extsupport.QEXT && qext != nil {
		extDec = qext.dec
		extPulses = qext.extraPulses[:end]
		extTotalBitsQ3 = qext.totalBitsQ3
	}
	var coeffsMono []celtNorm
	var collapse []byte
	if pm := d.perMode; pm != nil {
		coeffsMono, _, collapse = quantAllBandsDecodeWithScratchWithMode(rd, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, false, &d.rng, &d.scratchBands,
			extDec, extPulses, extTotalBitsQ3, pm.eBands, pm.logN, pm.cacheIndex, pm.cacheBits)
	} else {
		coeffsMono, _, collapse = quantAllBandsDecodeWithScratch(rd, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, false, &d.rng, &d.scratchBands,
			extDec, extPulses, extTotalBitsQ3)
	}
	if extsupport.QEXT && qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, false, qext)
	}

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBit() == 1
	}

	bitsLeft := totalBits - rd.Tell()
	if extsupport.QEXT && qext != nil {
		d.decodeEnergyFinaliseGLogRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.decodeEnergyFinaliseGLog(monoEnergies, end, fineQuant, finePriority, bitsLeft)
	}

	if antiCollapseOn {
		if pm := d.perMode; pm != nil {
			antiCollapseGLogMode(coeffsMono, nil, collapse, lm, 1, start, end, monoEnergies, prev1LogE, prev2LogE, pulses, d.rng, pm.eBands, pm.nbEBands)
		} else {
			antiCollapseGLog(coeffsMono, nil, collapse, lm, 1, start, end, monoEnergies, prev1LogE, prev2LogE, pulses, d.rng)
		}
	}
	if silence {
		applyDecodedSilence(monoEnergies, coeffsMono, nil, qext)
	}

	downsample := d.downsampleFactor()
	specMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(coeffsMono))
	if extsupport.QEXT && qext != nil && qext.end > 0 {
		specMono = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsMono))
		denormalizeBandsPackedDownsampleIntoFloat32(specMono, coeffsMono, monoEnergies, 0, end, lm, d.modeEdges(), downsample)
		if qext.coeffsL != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specMono, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	} else {
		denormalizeBandsPackedDownsampleIntoFloat32(specMono, coeffsMono, monoEnergies, 0, end, lm, d.modeEdges(), downsample)
	}

	d.channels = int32(origChannels)
	d.prevEnergy = origPrevEnergy
	d.applyPendingPLCPrefilterAndFold()

	// celt_synthesis with C=1, CC=2 runs the inverse MDCT of the mono
	// spectrum into both output channels.
	samples := d.synthesizeFrame(specMono, specMono, frameSize, mode.LM, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset)

	stereoEnergies := ensureGLogSlice(&d.scratchStereoEnergies, bandStride*2)
	for i := range bandStride {
		// Update background energy from oldBandE before clearing the inactive bands.
		energy := prev1Energy[i]
		if i < end {
			energy = monoEnergies[i]
		}
		stereoEnergies[i] = energy
		stereoEnergies[bandStride+i] = energy
	}

	d.updateLogEGLog(stereoEnergies, bandStride, transient)
	for i := range bandStride {
		d.prevEnergy[i] = stereoEnergies[i]
		d.prevEnergy[bandStride+i] = stereoEnergies[bandStride+i]
	}
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, origChannels)

	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	}
	d.rng = combineFinalRange(rd, extDec)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// decodeStereoPacketToMono decodes a stereo packet and converts output to mono.
func (d *Decoder) decodeStereoPacketToMono(data []byte, frameSize int) ([]float32, error) {
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}
	if len(data) <= 1 {
		return d.decodePLC(frameSize)
	}
	if !d.validFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	d.ensureEnergyState(2)

	origChannels := int(d.channels)
	d.channels = 2
	defer func() {
		d.channels = int32(origChannels)
	}()

	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

	mode := d.modeConfig(frameSize)
	lm := mode.LM
	end := d.effectiveEndBand(frameSize)
	start := 0
	prev1LogE := d.prevLogE
	prev2LogE := d.prevLogE2

	totalBits := len(data) * 8
	silence := decodeSilenceFlag(rd, totalBits)

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	postfilterGain := header.postfilterGain
	postfilterPeriod := header.postfilterPeriod
	postfilterTapset := header.postfilterTapset
	transient := header.transient
	intra := header.intra
	shortBlocks := header.shortBlocks

	energies := d.decodeCoarseEnergyGLogInto(ensureGLogSlice(&d.scratchEnergies, end*int(d.channels)), end, intra, lm)

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

	d.decodeFineEnergyGLog(energies, end, nil, fineQuant)
	var qext *preparedQEXTDecode
	if extsupport.QEXT {
		qext = d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	}
	if extsupport.QEXT && qext != nil {
		d.decodeFineEnergyGLogWithDecoderPrev(qext.dec, energies, end, fineQuant, qext.extraQuant[:end])
	}

	var extDec *rangecoding.Decoder
	var extPulses []int32
	extTotalBitsQ3 := 0
	if extsupport.QEXT && qext != nil {
		extDec = qext.dec
		extPulses = qext.extraPulses[:end]
		extTotalBitsQ3 = qext.totalBitsQ3
	}
	channels := int(d.channels)
	var coeffsL, coeffsR []celtNorm
	var collapse []byte
	if pm := d.perMode; pm != nil {
		coeffsL, coeffsR, collapse = quantAllBandsDecodeWithScratchWithMode(rd, channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.phaseInversionDisabled, &d.rng, &d.scratchBands,
			extDec, extPulses, extTotalBitsQ3, pm.eBands, pm.logN, pm.cacheIndex, pm.cacheBits)
	} else {
		coeffsL, coeffsR, collapse = quantAllBandsDecodeWithScratch(rd, channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.phaseInversionDisabled, &d.rng, &d.scratchBands,
			extDec, extPulses, extTotalBitsQ3)
	}
	if extsupport.QEXT && qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, d.phaseInversionDisabled, qext)
	}

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBit() == 1
	}

	bitsLeft := totalBits - rd.Tell()
	if extsupport.QEXT && qext != nil {
		d.decodeEnergyFinaliseGLogRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.decodeEnergyFinaliseGLog(energies, end, fineQuant, finePriority, bitsLeft)
	}

	if antiCollapseOn {
		if pm := d.perMode; pm != nil {
			antiCollapseGLogMode(coeffsL, coeffsR, collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng, pm.eBands, pm.nbEBands)
		} else {
			antiCollapseGLog(coeffsL, coeffsR, collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
		}
	}
	if silence {
		applyDecodedSilence(energies, coeffsL, coeffsR, qext)
	}

	energiesL := energies[:end]
	energiesR := energies[end:]
	downsample := d.downsampleFactor()
	var specL []float32
	var specR []float32
	if extsupport.QEXT && qext != nil && qext.end > 0 {
		specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
		specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, d.modeEdges(), downsample)
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, d.modeEdges(), downsample)
		if qext.coeffsL != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
		if qext.coeffsR != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	} else {
		specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
		specR = ensureFloat32Slice(&d.scratchMonoToStereoRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, d.modeEdges(), downsample)
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, d.modeEdges(), downsample)
	}
	coeffsMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(specL))
	for i := range coeffsMono {
		coeffsMono[i] = 0.5 * (specL[i] + specR[i])
	}

	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLog(energies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, 2)
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	}
	d.rng = combineFinalRange(rd, extDec)

	d.channels = int32(origChannels)
	d.applyPendingPLCPrefilterAndFold()

	samples := d.synthesizeFrame(coeffsMono, nil, frameSize, mode.LM, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// decodeFrameHybridWithPacketStereo decodes a hybrid CELT frame while honoring the packet stereo flag.
func (d *Decoder) decodeFrameHybridWithPacketStereo(rd *rangecoding.Decoder, frameSize int, packetStereo bool) ([]float32, error) {
	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	channels := int(d.channels)
	d.handleChannelTransition(packetChannels)
	if packetChannels == channels {
		return d.decodeFrameHybrid(rd, frameSize)
	}
	if packetChannels == 1 && channels == 2 {
		return d.decodeMonoPacketToStereoHybrid(rd, frameSize)
	}
	return d.decodeStereoPacketToMonoHybrid(rd, frameSize)
}

// AccumulateFrameHybridWithPacketStereo decodes CELT bands starting at band 17
// from the range decoder positioned at the CELT payload, then accumulates the
// decoded signal onto out, which contains the SILK low band. dataLen is the
// main-payload length in bytes after redundancy parsing; frameSize is the
// per-channel CELT sample count at the active mode rate. out uses the decoder's
// configured API rate and channel layout. A payload length of at most one byte
// conceals only the CELT high band. This follows libopus
// opus_decoder.c:opus_decode_frame with celt_accum=1.
func (d *Decoder) AccumulateFrameHybridWithPacketStereo(rd *rangecoding.Decoder, dataLen, frameSize int, packetStereo bool, out []float32) error {
	if dataLen <= 1 {
		// opus_decode_frame can discard malformed redundancy and set len=0
		// without changing entropy storage. Conceal only the CELT highband.
		if extsupport.QEXT {
			_ = d.takeQEXTPayload()
		}
		return d.DecodeHybridFECPLC(frameSize, out)
	}
	d.directOutPCM = out
	d.directOutAccum = true
	defer func() {
		d.directOutPCM = nil
		d.directOutAccum = false
	}()
	_, err := d.decodeFrameHybridWithPacketStereo(rd, frameSize, packetStereo)
	return err
}

// decodeMonoPacketToStereoHybrid decodes a mono hybrid frame and duplicates to stereo output.
func (d *Decoder) decodeMonoPacketToStereoHybrid(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if !d.validHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	origChannels := int(d.channels)
	d.channels = 1

	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergyGLog, MaxBands)
	prev1LogE := d.prevLogE
	prev2LogE := d.prevLogE2
	for i := range MaxBands {
		left := d.prevEnergy[i]
		if origChannels > 1 && len(d.prevEnergy) >= MaxBands*2 {
			right := d.prevEnergy[MaxBands+i]
			if right > left {
				left = right
			}
		}
		prev1Energy[i] = left
	}
	origPrevEnergy := d.prevEnergy
	d.prevEnergy = prev1Energy

	defer func() {
		d.channels = int32(origChannels)
		d.prevEnergy = origPrevEnergy
	}()

	d.SetRangeDecoder(rd)
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}

	mode := d.modeConfig(frameSize)
	lm := mode.LM
	end := d.effectiveEndBand(frameSize)
	start := HybridCELTStartBand

	totalBits := rd.StorageBits()
	silence := decodeSilenceFlag(rd, totalBits)

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	postfilterGain := header.postfilterGain
	postfilterPeriod := header.postfilterPeriod
	postfilterTapset := header.postfilterTapset
	transient := header.transient
	intra := header.intra
	shortBlocks := header.shortBlocks

	monoEnergies := ensureGLogSlice(&d.scratchEnergies, end*int(d.channels))
	for band := 0; band < end; band++ {
		monoEnergies[band] = d.prevEnergy[band]
	}
	d.decodeCoarseEnergyRangeGLog(start, end, intra, lm, monoEnergies)

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

	coeffsMono, _, qext := d.decodeHybridSpectrum(qextPayload, rd, totalBits, frameSize, start, end, lm, shortBlocks, spread, antiCollapseRsv, 1, false, monoEnergies, prev1LogE, prev2LogE, pulses, fineQuant, finePriority, tfRes, intensity, dualStereo, balance, codedBands)
	if silence {
		applyDecodedSilence(monoEnergies, coeffsMono, nil, qext)
	}

	downsample := d.downsampleFactor()
	specMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(coeffsMono))
	if extsupport.QEXT && qext != nil && qext.end > 0 {
		specMono = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsMono))
		denormalizeBandsPackedDownsampleIntoFloat32(specMono, coeffsMono, monoEnergies, HybridCELTStartBand, end, lm, EBands[:], downsample)
		if qext.coeffsL != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specMono, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	} else {
		denormalizeBandsPackedDownsampleIntoFloat32(specMono, coeffsMono, monoEnergies, HybridCELTStartBand, end, lm, EBands[:], downsample)
	}

	d.channels = int32(origChannels)
	d.prevEnergy = origPrevEnergy
	d.applyPendingPLCPrefilterAndFold()

	// celt_synthesis with C=1, CC=2 runs the inverse MDCT of the mono
	// spectrum into both output channels.
	samples := d.synthesizeFrame(specMono, specMono, frameSize, mode.LM, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset)

	var stereoEnergiesArr [MaxBands * 2]celtGLog
	stereoEnergies := stereoEnergiesArr[:]
	for i := range MaxBands {
		// Match celt_decode_with_ec: update background, then clear outside the range.
		energy := prev1Energy[i]
		if i >= start && i < end {
			energy = monoEnergies[i]
		}
		stereoEnergies[i] = energy
		stereoEnergies[MaxBands+i] = energy
	}

	d.updateLogEGLog(stereoEnergies, MaxBands, transient)
	d.setPrevEnergyGLog(stereoEnergies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, origChannels)
	var extDec *rangecoding.Decoder
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	} else if extsupport.QEXT && qext != nil {
		extDec = qext.dec
	}

	d.rng = combineFinalRange(rd, extDec)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// decodeStereoPacketToMonoHybrid decodes a stereo hybrid frame and downmixes to mono output.
func (d *Decoder) decodeStereoPacketToMonoHybrid(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if !d.validHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	d.ensureEnergyState(2)

	origChannels := int(d.channels)
	d.channels = 2
	defer func() {
		d.channels = int32(origChannels)
	}()

	d.SetRangeDecoder(rd)
	var qextPayload []byte
	if extsupport.QEXT {
		qextPayload = d.takeQEXTPayload()
	}

	mode := d.modeConfig(frameSize)
	lm := mode.LM
	end := d.effectiveEndBand(frameSize)
	start := HybridCELTStartBand
	prev1LogE := d.prevLogE
	prev2LogE := d.prevLogE2

	totalBits := rd.StorageBits()
	silence := decodeSilenceFlag(rd, totalBits)

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	postfilterGain := header.postfilterGain
	postfilterPeriod := header.postfilterPeriod
	postfilterTapset := header.postfilterTapset
	transient := header.transient
	intra := header.intra
	shortBlocks := header.shortBlocks

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
	if silence {
		applyDecodedSilence(energies, coeffsL, coeffsR, qext)
	}

	hybridBinStart := d.hybridBandStart(frameSize)
	energiesL := energies[:end]
	energiesR := energies[end:]
	downsample := d.downsampleFactor()
	var specL []float32
	var specR []float32
	if extsupport.QEXT && qext != nil && qext.end > 0 {
		specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
		specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, HybridCELTStartBand, end, lm, EBands[:], downsample)
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, HybridCELTStartBand, end, lm, EBands[:], downsample)
		if qext.coeffsL != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
		if qext.coeffsR != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	} else {
		specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
		specR = ensureFloat32Slice(&d.scratchMonoToStereoRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, HybridCELTStartBand, end, lm, EBands[:], downsample)
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, HybridCELTStartBand, end, lm, EBands[:], downsample)
		clear(specL[:min(hybridBinStart, len(specL))])
		clear(specR[:min(hybridBinStart, len(specR))])
	}

	coeffsMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(specL))
	for i := range coeffsMono {
		coeffsMono[i] = 0.5 * (specL[i] + specR[i])
	}

	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLog(energies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, 2)
	var extDec *rangecoding.Decoder
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	} else if extsupport.QEXT && qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)

	d.channels = int32(origChannels)
	d.applyPendingPLCPrefilterAndFold()

	samples := d.synthesizeFrame(coeffsMono, nil, frameSize, mode.LM, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}
