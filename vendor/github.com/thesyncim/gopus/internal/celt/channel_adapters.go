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

// DecodeFrameWithPacketStereoToFloat32 decodes a CELT frame directly into a
// caller-provided float32 buffer for the common packetChannels==decoder
// channels path, falling back to the slice-returning path otherwise.
func (d *Decoder) DecodeFrameWithPacketStereoToFloat32(data []byte, frameSize int, packetStereo bool, out []float32) error {
	channels := int(d.channels)
	outLen := frameSize * channels
	if len(out) < outLen {
		return ErrOutputTooSmall
	}

	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	if len(data) > 1 && packetChannels == 1 && channels == 2 {
		d.directOutPCM = out[:outLen]
		defer func() {
			d.directOutPCM = nil
		}()
		_, err := d.decodeMonoPacketToStereo(data, frameSize)
		return err
	}

	if len(data) <= 1 {
		d.directOutPCM = out[:outLen]
		defer func() {
			d.directOutPCM = nil
		}()
		_, err := d.DecodeFrameWithPacketStereo(data, frameSize, packetStereo)
		return err
	}

	if packetChannels != channels {
		samples, err := d.DecodeFrameWithPacketStereo(data, frameSize, packetStereo)
		if err != nil {
			return err
		}
		copy(out[:outLen], samples)
		return nil
	}

	d.directOutPCM = out[:outLen]
	defer func() {
		d.directOutPCM = nil
	}()
	_, err := d.DecodeFrame(data, frameSize)
	return err
}

// DecodeFrameAtAPIRate decodes a frame and returns PCM at the decoder's API
// sample rate, choosing packet stereo from the decoder channel count.
func (d *Decoder) DecodeFrameAtAPIRate(data []byte, frameSize int) ([]float32, error) {
	return d.DecodeFrameWithPacketStereoAtAPIRate(data, frameSize, d.channels == 2)
}

// DecodeFrameWithPacketStereoAtAPIRate decodes a frame at the internal 48 kHz
// rate and downsamples to the decoder's API rate. frameSize is given at the API
// rate; the internal block is frameSize*downsampleFactor.
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

// DecodeFrameWithPacketStereoToFloat32AtAPIRate decodes into the caller-provided
// out slice at the decoder's API sample rate, downsampling from the internal
// 48 kHz block when required.
func (d *Decoder) DecodeFrameWithPacketStereoToFloat32AtAPIRate(data []byte, frameSize int, packetStereo bool, out []float32) error {
	downsample := d.downsampleFactor()
	if downsample <= 1 {
		return d.DecodeFrameWithPacketStereoToFloat32(data, frameSize, packetStereo, out)
	}
	if frameSize <= 0 || frameSize*downsample/downsample != frameSize {
		return ErrInvalidFrameSize
	}
	internalFrameSize := frameSize * downsample
	if !ValidFrameSize(internalFrameSize) {
		return ErrInvalidFrameSize
	}
	channels := int(d.channels)
	outLen := frameSize * channels
	if len(out) < outLen {
		return ErrOutputTooSmall
	}

	d.directOutPCM = out[:outLen]
	defer func() {
		d.directOutPCM = nil
	}()

	samples, err := d.DecodeFrameWithPacketStereo(data, internalFrameSize, packetStereo)
	if err != nil {
		return err
	}
	if len(samples) != 0 {
		copyDownsampledFloat32(out[:outLen], samples, frameSize, channels, downsample)
	}
	return nil
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
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	origChannels := int(d.channels)
	d.channels = 1

	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := 0

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

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}

	defer func() {
		d.channels = int32(origChannels)
		d.prevEnergy = origPrevEnergy
	}()

	if silence {
		d.channels = int32(origChannels)
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		silenceE := ensureGLogSlice(&d.scratchSilenceE, MaxBands*origChannels)
		fillSilenceGLog(silenceE)
		d.prevEnergy = origPrevEnergy
		for i := 0; i < MaxBands*origChannels && i < len(d.prevEnergy); i++ {
			d.prevEnergy[i] = -28.0
		}
		d.updateLogEGLog(silenceE, MaxBands, false)
		d.updateBackgroundEnergy(lm)
		d.resetPLCCadence(frameSize, origChannels)
		d.rng = rd.Range()
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
	coeffsMono, _, collapse := quantAllBandsDecodeWithScratch(rd, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, false, &d.rng, &d.scratchBands,
		extDec, extPulses, extTotalBitsQ3)
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
		antiCollapseGLog(coeffsMono, nil, collapse, lm, 1, start, end, monoEnergies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	downsample := d.downsampleFactor()
	specMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(coeffsMono))
	if extsupport.QEXT && qext != nil && qext.end > 0 {
		specMono = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsMono))
		denormalizeBandsPackedDownsampleIntoFloat32(specMono, coeffsMono, monoEnergies, 0, end, lm, EBands[:], downsample)
		if qext.coeffsL != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specMono, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	} else {
		denormalizeBandsPackedDownsampleIntoFloat32(specMono, coeffsMono, monoEnergies, 0, end, lm, EBands[:], downsample)
	}

	d.channels = int32(origChannels)
	d.prevEnergy = origPrevEnergy
	d.applyPendingPLCPrefilterAndFold()

	var samples []float32
	directPlanar := false
	if !transient {
		outL, outR := d.synthesizeStereoPlanarFromMonoLong(specMono)
		if len(d.directOutPCM) >= frameSize*2 {
			left := outL[:frameSize]
			right := outR[:frameSize]
			d.applyPostfilterStereoPlanarFromFloat32(left, right, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], left, right, 1.0/32768.0)
			directPlanar = true
		} else {
			samples = ensureFloat32Slice(&d.scratchStereoF32, frameSize*2)
			InterleaveStereoIntoF32(outL[:frameSize], outR[:frameSize], samples[:frameSize*2])
			samples = samples[:frameSize*2]
		}
	} else {
		coeffsL := specMono
		coeffsR := ensureFloat32Slice(&d.scratchMonoToStereoRF32, len(coeffsMono))
		copy(coeffsR, specMono)
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	}
	if !directPlanar {
		d.applyPostfilterFloat32(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
		if len(d.directOutPCM) >= len(samples) {
			d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
		} else {
			d.applyDeemphasisAndScale(samples, 1.0/32768.0)
		}
	}

	var stereoEnergiesArr [MaxBands * 2]celtGLog
	stereoEnergies := stereoEnergiesArr[:]
	for i := 0; i < end; i++ {
		stereoEnergies[i] = monoEnergies[i]
		stereoEnergies[MaxBands+i] = monoEnergies[i]
	}
	for i := end; i < MaxBands; i++ {
		stereoEnergies[i] = -28.0
		stereoEnergies[MaxBands+i] = -28.0
	}

	d.updateLogEGLog(stereoEnergies, end, transient)
	for i := range MaxBands {
		d.prevEnergy[i] = stereoEnergies[i]
		d.prevEnergy[MaxBands+i] = stereoEnergies[MaxBands+i]
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
	if !ValidFrameSize(frameSize) {
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

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := 0
	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	prev1LogE := d.prevLogE
	prev2LogE := d.prevLogE2

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := make([]float32, frameSize)
		var silenceEArr [MaxBands * 2]celtGLog
		silenceE := silenceEArr[:]
		fillSilenceGLog(silenceE)
		d.updateLogEGLog(silenceE, MaxBands, false)
		d.setPrevEnergyGLogWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, origChannels)
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
	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.phaseInversionDisabled, &d.rng, &d.scratchBands,
		extDec, extPulses, extTotalBitsQ3)
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
		antiCollapseGLog(coeffsL, coeffsR, collapse, lm, channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	energiesL := energies[:end]
	energiesR := energies[end:]
	downsample := d.downsampleFactor()
	var specL []float32
	var specR []float32
	if extsupport.QEXT && qext != nil && qext.end > 0 {
		specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
		specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, EBands[:], downsample)
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, EBands[:], downsample)
		if qext.coeffsL != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
		if qext.coeffsR != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	} else {
		specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
		specR = ensureFloat32Slice(&d.scratchMonoToStereoRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, EBands[:], downsample)
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, EBands[:], downsample)
	}
	coeffsMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(specL))
	for i := range coeffsMono {
		coeffsMono[i] = 0.5 * (specL[i] + specR[i])
	}

	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLogWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)
	d.clearFrameHistoryOutsideRange(start, end, 2)
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	}
	d.rng = combineFinalRange(rd, extDec)

	d.channels = int32(origChannels)
	d.applyPendingPLCPrefilterAndFold()

	samples := d.Synthesize(coeffsMono, transient, shortBlocks)
	d.applyPostfilterFloat32(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}

// DecodeFrameHybridWithPacketStereo decodes a hybrid CELT frame while honoring the packet stereo flag.
func (d *Decoder) DecodeFrameHybridWithPacketStereo(rd *rangecoding.Decoder, frameSize int, packetStereo bool) ([]float32, error) {
	packetChannels := packetChannelsFromStereoFlag(packetStereo)
	channels := int(d.channels)
	d.handleChannelTransition(packetChannels)
	if packetChannels == channels {
		return d.DecodeFrameHybrid(rd, frameSize)
	}
	if packetChannels == 1 && channels == 2 {
		return d.decodeMonoPacketToStereoHybrid(rd, frameSize)
	}
	return d.decodeStereoPacketToMonoHybrid(rd, frameSize)
}

// DecodeFrameHybridWithPacketStereoToFloat32 decodes the CELT half of a hybrid
// frame from an in-progress range decoder into out, handling packet/decoder
// channel-count mismatches.
func (d *Decoder) DecodeFrameHybridWithPacketStereoToFloat32(rd *rangecoding.Decoder, frameSize int, packetStereo bool, out []float32) error {
	outLen := frameSize * int(d.channels)
	if len(out) < outLen {
		return ErrOutputTooSmall
	}

	d.directOutPCM = out[:outLen]
	defer func() {
		d.directOutPCM = nil
	}()

	samples, err := d.DecodeFrameHybridWithPacketStereo(rd, frameSize, packetStereo)
	if err != nil {
		return err
	}
	if len(samples) != 0 {
		copy(out[:outLen], samples)
	}
	return nil
}

// decodeMonoPacketToStereoHybrid decodes a mono hybrid frame and duplicates to stereo output.
func (d *Decoder) decodeMonoPacketToStereoHybrid(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.beginDecodedPacketPLCState()
	origChannels := int(d.channels)
	d.channels = 1

	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergyGLog, MaxBands)
	prev1EnergyHistory := ensureGLogSlice(&d.scratchPrevEnergy, MaxBands)
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
		prev1EnergyHistory[i] = left
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

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := HybridCELTStartBand

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		d.channels = int32(origChannels)
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		var silenceEArr [MaxBands * 2]celtGLog
		silenceE := silenceEArr[:MaxBands*origChannels]
		fillSilenceGLog(silenceE)
		d.prevEnergy = origPrevEnergy
		d.updateLogEGLog(silenceE, MaxBands, false)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, origChannels)
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

	var samples []float32
	directPlanar := false
	if !transient {
		outL, outR := d.synthesizeStereoPlanarFromMonoLong(specMono)
		if len(d.directOutPCM) >= frameSize*2 {
			left := outL[:frameSize]
			right := outR[:frameSize]
			d.applyPostfilterStereoPlanarFromFloat32(left, right, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
			d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], left, right, 1.0/32768.0)
			directPlanar = true
		} else {
			samples = ensureFloat32Slice(&d.scratchStereoF32, frameSize*2)
			InterleaveStereoIntoF32(outL[:frameSize], outR[:frameSize], samples[:frameSize*2])
			samples = samples[:frameSize*2]
		}
	} else {
		coeffsL := specMono
		coeffsR := ensureFloat32Slice(&d.scratchMonoToStereoRF32, len(coeffsMono))
		copy(coeffsR, specMono)
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	}
	if !directPlanar {
		d.applyPostfilterFloat32(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
		d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	}

	var stereoEnergiesArr [MaxBands * 2]celtGLog
	stereoEnergies := stereoEnergiesArr[:]
	for i := 0; i < end; i++ {
		stereoEnergies[i] = monoEnergies[i]
		stereoEnergies[MaxBands+i] = monoEnergies[i]
	}
	for i := end; i < MaxBands; i++ {
		stereoEnergies[i] = -28.0
		stereoEnergies[MaxBands+i] = -28.0
	}

	d.updateLogEGLog(stereoEnergies, end, transient)
	d.setPrevEnergyGLogWithPrev(prev1EnergyHistory, stereoEnergies)
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
	if frameSize != 480 && frameSize != 960 {
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

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	start := HybridCELTStartBand
	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	prev1LogE := d.prevLogE
	prev2LogE := d.prevLogE2

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := ensureFloat32Slice(&d.scratchMonoMixF32, frameSize)
		clear(samples[:frameSize])
		var silenceEArr [MaxBands * 2]celtGLog
		silenceE := silenceEArr[:]
		fillSilenceGLog(silenceE)
		d.updateLogEGLog(silenceE, MaxBands, false)
		d.setPrevEnergyGLogWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		d.rng = rd.Range()
		d.resetPLCCadence(frameSize, origChannels)
		return samples[:frameSize], nil
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
		for i := 0; i < hybridBinStart && i < len(specL); i++ {
			specL[i] = 0
		}
		for i := 0; i < hybridBinStart && i < len(specR); i++ {
			specR[i] = 0
		}
	}

	coeffsMono := ensureFloat32Slice(&d.scratchMonoMixF32, len(specL))
	for i := range coeffsMono {
		coeffsMono[i] = 0.5 * (specL[i] + specR[i])
	}

	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLogWithPrev(prev1Energy, energies)
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

	samples := d.Synthesize(coeffsMono, transient, shortBlocks)
	d.applyPostfilterFloat32(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	d.resetPLCCadence(frameSize, origChannels)

	return samples, nil
}
