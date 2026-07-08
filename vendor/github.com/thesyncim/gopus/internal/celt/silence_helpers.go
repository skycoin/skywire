package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// DecodeStereoParams decodes stereo parameters (intensity and dual stereo).
// Reference: RFC 6716 Section 4.3.4, libopus celt/celt_decoder.c
func (d *Decoder) DecodeStereoParams(nbBands int) (intensity, dualStereo int) {
	if d.rangeDecoder == nil {
		return -1, 0
	}

	// IntensityDecay = 16384 (Q15)
	const decay = 16384

	// Compute fs0 exactly as encoder does
	// fs0 = laplaceNMin + (laplaceFS - laplaceNMin)*decay >> 15
	fs0 := laplaceNMin + ((laplaceFS-laplaceNMin)*decay)>>15

	// Decode intensity band index using Laplace distribution
	intensity = d.decodeLaplace(fs0, decay)

	// Decode dual stereo flag
	dualStereo = d.rangeDecoder.DecodeBit(1)

	return intensity, dualStereo
}

// synthesizeSilenceMono synthesizes a mono CELT silence frame.
//
// libopus celt_decode_with_ec() does NOT special-case the silence frame: it
// zeroes the spectrum (denormalise_bands with silence=1 sets bound=0) and runs
// the full celt_synthesis -> clt_mdct_backward overlap-add against the carried
// decode_mem, so the frame still emits the windowed tail of the previous frame
// before fading to silence. Mirror that here by synthesizing a zero spectrum
// through the same overlap-add path used by a normal frame, which both produces
// the correct cross-frame overlap and updates d.overlapBuffer with the (zero)
// tail exactly as libopus's out_syn buffer does.
func (d *Decoder) synthesizeSilenceMono(frameSize int) []float32 {
	if frameSize <= 0 {
		return nil
	}
	spec := ensureFloat32Slice(&d.scratchSilenceSpec, frameSize)
	clear(spec)
	return d.Synthesize(spec, false, 1)
}

func (d *Decoder) synthesizeSilenceStereo(frameSize int) []float32 {
	if frameSize <= 0 {
		return nil
	}
	spec := ensureFloat32Slice(&d.scratchSilenceSpec, frameSize)
	clear(spec)
	return d.SynthesizeStereo(spec, spec, false, 1)
}

// decodeSilenceFrame synthesizes a CELT silence frame from overlap state.
func (d *Decoder) decodeSilenceFrame(frameSize int, newPeriod int, newGain float32, newTapset int) []float32 {
	mode := GetModeConfig(frameSize)
	d.applyPendingPLCPrefilterAndFold()
	var samples []float32
	if d.channels == 2 {
		samples = d.synthesizeSilenceStereo(frameSize)
	} else {
		samples = d.synthesizeSilenceMono(frameSize)
	}
	if len(samples) == 0 {
		return nil
	}

	d.applyPostfilterFloat32(samples, frameSize, mode.LM, newPeriod, newGain, newTapset)
	if len(d.directOutPCM) >= len(samples) {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
		// The scaled, deemphasized PCM has already been written to directOutPCM
		// (matching the normal synthesizeHybridDecodedFrame direct-out path). Return
		// nil so the hybrid wrapper does not overwrite it with the raw, unscaled
		// celt_sig synthesis buffer.
		return nil
	}
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	return samples
}

func (d *Decoder) handleDecodedSilenceFrame(frameSize, lm int, prev1Energy []celtGLog, rd *rangecoding.Decoder) []float32 {
	samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
	channels := int(d.channels)
	silenceE := ensureGLogSlice(&d.scratchSilenceE, MaxBands*channels)
	fillSilenceGLog(silenceE)
	d.updateLogEGLog(silenceE, MaxBands, false)
	d.setPrevEnergyGLogWithPrev(prev1Energy, silenceE)
	d.replicateMonoEnergyToSecondChannel()
	d.updateBackgroundEnergy(lm)
	d.resetPLCCadence(frameSize, channels)
	d.rng = rd.Range()
	return samples
}

// Mirrors libopus celt/celt_decoder.c silence oldBandE setup: -28 stored as celt_glog.
func fillSilenceGLog(energies []celtGLog) {
	for i := range energies {
		energies[i] = -28.0
	}
}
