package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func denormalizeBandsPackedDownsampleIntoFloat32(dst []float32, src []celtNorm, energies []celtGLog, start, end, lm int, edges []int, downsample int) {
	if len(dst) == 0 || len(src) == 0 || len(energies) == 0 || end <= start || len(edges) < end+1 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > len(energies) {
		end = len(energies)
	}
	if end <= start {
		return
	}

	M := 1 << lm
	bound := edges[end] * M
	if downsample > 1 {
		if limit := len(dst) / downsample; bound > limit {
			bound = limit
		}
	}
	if bound > len(dst) {
		bound = len(dst)
	}
	if start != 0 {
		prefix := min(edges[start]*M, len(dst))
		clear(dst[:prefix])
	}
	f := min(edges[start]*M, len(dst))

	for band := start; band < end; band++ {
		j := edges[band] * M
		bandEnd := edges[band+1] * M
		if j >= len(src) {
			break
		}
		if bandEnd > len(src) {
			bandEnd = len(src)
		}
		gain := denormalizeBandGain(energies, band)
		count := bandEnd - j
		if room := len(dst) - f; count > room {
			count = room
		}
		if count <= 0 {
			if f >= len(dst) {
				break
			}
			continue
		}
		// Low bands are only a few bins wide; their NEON call/setup cost beats the
		// per-lane win, so keep them on the tight inline loop and vector only the
		// wide bands. Each product is bare, so the result matches on every build.
		if count < 8 {
			for ; j < bandEnd && f < len(dst); j++ {
				dst[f] = float32(src[j]) * gain
				f++
			}
			continue
		}
		scaleFloat32IntoNEON(dst[f:f+count], src[j:j+count], gain)
		f += count
	}
	if bound < len(dst) {
		clear(dst[bound:])
	}
}

func (d *Decoder) synthesizeDecodedFrame(frameSize, modeLM, end, lm, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float32, postfilterTapset int, energies []celtGLog, coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) []float32 {
	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float32
	channels := int(d.channels)
	downsample := d.downsampleFactor()
	outputFrameSize := frameSize
	downsampleOutput := false
	if downsample > 1 && frameSize%downsample == 0 {
		apiFrameSize := frameSize / downsample
		if len(d.directOutPCM) < frameSize*channels && len(d.directOutPCM) >= apiFrameSize*channels {
			outputFrameSize = apiFrameSize
			downsampleOutput = true
		}
	}
	// The native 96 kHz HD mode needs the HD-specific de-emphasis (2-tap) and
	// comb-filter postfilter (comb_filter_qext), which live on the non-direct
	// synthesis path. Disable the direct-output fast paths so HD frames route
	// through Synthesize/SynthesizeStereo + the HD-aware deemphasis/postfilter,
	// which still write into directOutPCM at the end of this function.
	hdMode := d.synthOverlap == 240 || d.customScaleBase > 0
	directStereoFloat32 := !hdMode && d.channels == 2 && len(d.directOutPCM) >= outputFrameSize*2
	directMonoFloat32 := !hdMode && d.channels == 1 &&
		len(d.directOutPCM) >= outputFrameSize &&
		!transient &&
		d.postfilterGainOld == 0 &&
		d.postfilterGain == 0 &&
		postfilterGain == 0

	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
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
			specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energiesL, 0, end, lm, d.modeEdges(), downsample)
			denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energiesR, 0, end, lm, d.modeEdges(), downsample)
		}
		if directStereoFloat32 && !transient {
			if d.synthTrace != nil {
				d.synthTrace.captureSpec(0, specL[:frameSize])
				d.synthTrace.captureSpec(1, specR[:frameSize])
			}
			samplesL, samplesR := d.synthesizeStereoPlanarLongToFloat32(specL, specR)
			if d.synthTrace != nil {
				d.synthTrace.captureIMDCT(0, samplesL[:frameSize])
				d.synthTrace.captureIMDCT(1, samplesR[:frameSize])
			}
			if d.postfilterGainOld == 0 && d.postfilterGain == 0 && postfilterGain == 0 {
				d.applyPostfilterNoGainStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			} else {
				d.applyPostfilterStereoPlanarFromFloat32(samplesL[:frameSize], samplesR[:frameSize], frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			if downsampleOutput {
				d.applyDeemphasisAndScaleStereoPlanarFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize*2], samplesL[:frameSize], samplesR[:frameSize], downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], samplesL[:frameSize], samplesR[:frameSize], 1.0/32768.0)
			}
		} else if directStereoFloat32 {
			if d.synthTrace != nil {
				d.synthTrace.captureSpec(0, specL[:frameSize])
				d.synthTrace.captureSpec(1, specR[:frameSize])
			}
			samplesL, samplesR := d.synthesizeStereoPlanar(specL, specR, transient, shortBlocks)
			if d.synthTrace != nil {
				d.synthTrace.captureIMDCT(0, samplesL[:frameSize])
				d.synthTrace.captureIMDCT(1, samplesR[:frameSize])
			}
			d.applyPostfilterStereoPlanarFromFloat32(samplesL, samplesR, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if downsampleOutput {
				d.applyDeemphasisAndScaleStereoPlanarFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize*2], samplesL, samplesR, downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(d.directOutPCM[:frameSize*2], samplesL, samplesR, 1.0/32768.0)
			}
		} else {
			samples = d.SynthesizeStereo(specL, specR, transient, shortBlocks)
		}
	} else {
		var specL []float32
		if extsupport.QEXT && qext != nil && qext.end > 0 {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, 0, end, lm, EBands[:], downsample)
			if qext.coeffsL != nil {
				denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
			}
		} else {
			specL = ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
			denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies, 0, end, lm, d.modeEdges(), downsample)
		}
		if directMonoFloat32 {
			samplesF32 := d.synthesizeMonoLongToFloat32(specL)
			d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)
			if downsampleOutput {
				d.applyDeemphasisAndScaleMonoFloat32DownsampleToFloat32(d.directOutPCM[:outputFrameSize], samplesF32, downsample, 1.0/32768.0)
			} else {
				d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
			}
		} else {
			if d.synthTrace != nil {
				d.synthTrace.captureSpec(0, specL[:frameSize])
			}
			samples = d.Synthesize(specL, transient, shortBlocks)
			if d.synthTrace != nil {
				d.synthTrace.captureIMDCT(0, samples[:frameSize])
			}
		}
	}

	if directStereoFloat32 || directMonoFloat32 {
		return samples
	}

	d.applyPostfilterFloat32(samples, frameSize, modeLM, postfilterPeriod, postfilterGain, postfilterTapset)

	// Step 7: Apply de-emphasis filter
	if downsampleOutput && len(d.directOutPCM) >= outputFrameSize*channels {
		d.applyDeemphasisAndScaleDownsampleToFloat32(d.directOutPCM[:outputFrameSize*channels], samples, downsample, 1.0/32768.0)
		return nil
	} else if len(d.directOutPCM) >= len(samples) {
		d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
	} else {
		d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	}

	return samples
}

func (d *Decoder) finalizeDecodedFrameState(frameSize, start, end, lm int, transient bool, energies, prev1Energy []celtGLog, qext *preparedQEXTDecode, rd *rangecoding.Decoder) error {
	// Update energy state for next frame.
	d.updateLogEGLog(energies, end, transient)
	d.setPrevEnergyGLogWithPrev(prev1Energy, energies)
	// libopus mirrors the left channel into the right slot on every mono frame
	// (`if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)`), keeping
	// oldBandE/oldLogE/oldLogE2 two-channel-symmetric. This must happen before the
	// background-floor and outside-range updates so the right shadow stays a true
	// copy: after a concealed loss the noise PLC only decays the left channel, and
	// the recovery frame folds the (undecayed) right shadow back in.
	d.replicateMonoEnergyToSecondChannel()
	d.updateBackgroundEnergy(lm)

	// Mirror libopus: clear energies/logs outside [start,end) for both channels.
	channels := int(d.channels)
	clearChannels := channels
	if channels == 1 && len(d.prevEnergy) >= d.predStride()*2 {
		clearChannels = 2
	}
	d.clearFrameHistoryOutsideRange(start, end, clearChannels)
	if extsupport.QEXT && qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return ErrInvalidFrame
	}

	var extDec *rangecoding.Decoder
	if extsupport.QEXT && qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)

	// Reset PLC state after successful decode.
	d.resetPLCCadence(frameSize, channels)
	return nil
}

// replicateMonoEnergyToSecondChannel copies the left-channel energy-prediction
// history (prevEnergy/prevLogE/prevLogE2) into the right-channel slot for a mono
// decoder, matching libopus celt_decode_with_ec()'s per-frame mono shadow update
// (`if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)` followed by the
// two-channel oldLogE/oldLogE2 refresh). backgroundLogE is left to
// updateBackgroundEnergy, which preserves the two-channel symmetry once the
// prediction history is symmetric. For a stereo decoder this is a no-op.
func (d *Decoder) replicateMonoEnergyToSecondChannel() {
	if d.channels != 1 {
		return
	}
	stride := d.predStride()
	if stride <= 0 || len(d.prevEnergy) < stride*2 {
		return
	}
	nbEBands := min(d.modeNbEBands(), stride)
	for band := 0; band < nbEBands; band++ {
		d.prevEnergy[stride+band] = d.prevEnergy[band]
		if len(d.prevLogE) >= stride*2 {
			d.prevLogE[stride+band] = d.prevLogE[band]
		}
		if len(d.prevLogE2) >= stride*2 {
			d.prevLogE2[stride+band] = d.prevLogE2[band]
		}
	}
}

func (d *Decoder) clearFrameHistoryOutsideRange(start, end, channels int) {
	// libopus clears the energy/log history outside [start,end) up to nbEBands.
	// For a per-mode custom layout that is the mode's band count, and the buffers
	// use the same nbEBands per-channel prediction stride (mono keeps c==0, so the
	// static MaxBands stride and the per-mode nbEBands stride coincide).
	nbEBands := d.modeNbEBands()
	stride := d.predStride()
	for c := range channels {
		base := c * stride
		for band := range start {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
		for band := end; band < nbEBands; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
	}
}
