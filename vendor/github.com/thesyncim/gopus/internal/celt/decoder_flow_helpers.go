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
	// Band k covers src[edges[k]*M : edges[k+1]*M] and lands at the same
	// offset of dst, as the freq and X cursors of libopus denormalise_bands()
	// advance together.
	limit := min(len(src), len(dst))
	dstL, srcL := dst[:limit], src[:limit]
	edges = edges[:end+1]

	var gainBuf [denormGainBands]float32
	var gains []float32
	if end <= denormGainBands {
		gains = gainBuf[:end]
		denormalizeBandGains(gains, energies, start, end)
	}
	for band := start; band < end; band++ {
		j := edges[band] << lm
		if j >= limit {
			break
		}
		bandEnd := min(edges[band+1]<<lm, limit)
		if bandEnd <= j {
			continue
		}
		var gain float32
		if gains != nil {
			gain = gains[band]
		} else {
			gain = denormalizeBandGain(energies, band)
		}
		out := dstL[j:bandEnd]
		in := srcL[j:bandEnd]
		// Low bands are only a few bins wide; their vector call/setup cost
		// beats the per-lane win, so keep them on the tight inline loop and
		// vector only the wide bands. Each product is bare, so the result
		// matches on every build.
		if len(out) < 8 {
			for k := range out {
				out[k] = float32(in[k]) * gain
			}
			continue
		}
		scaleFloat32Into(out, in, gain)
	}
	if bound < len(dst) {
		clear(dst[bound:])
	}
}

func (d *Decoder) synthesizeDecodedFrame(frameSize, modeLM, end, lm, shortBlocks int, transient bool, postfilterPeriod int, postfilterGain float32, postfilterTapset int, energies []celtGLog, coeffsL, coeffsR []celtNorm, qext *preparedQEXTDecode) []float32 {
	channels := int(d.channels)
	if d.synthTrace != nil {
		d.synthTrace.captureBaseEnergy(energies, end, channels)
		d.synthTrace.captureBaseNorm(0, coeffsL, frameSize)
		if channels == 2 {
			d.synthTrace.captureBaseNorm(1, coeffsR, frameSize)
		}
	}
	if d.synthTrace != nil && extsupport.QEXT && qext != nil {
		d.synthTrace.captureQEXTEnergy(qext.energies, qext.end, channels)
		d.synthTrace.captureQEXTNorm(0, qext.coeffsL, frameSize)
		if channels == 2 {
			d.synthTrace.captureQEXTNorm(1, qext.coeffsR, frameSize)
		}
	}
	// celt_synthesis: denormalise each channel's bands (and its QEXT bands)
	// into the frequency buffer the inverse MDCT reads.
	downsample := d.downsampleFactor()
	edges := d.modeEdges()
	withQEXT := extsupport.QEXT && qext != nil && qext.end > 0
	if withQEXT {
		edges = EBands[:]
	}
	specL := ensureFloat32Slice(&d.scratchStereoF32, len(coeffsL))
	denormalizeBandsPackedDownsampleIntoFloat32(specL, coeffsL, energies[:end], 0, end, lm, edges, downsample)
	if withQEXT && qext.coeffsL != nil {
		denormalizeBandsPackedDownsampleIntoFloat32(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands, downsample)
	}
	var specR []float32
	if channels == 2 {
		specR = ensureFloat32Slice(&d.scratchSpecRF32, len(coeffsR))
		denormalizeBandsPackedDownsampleIntoFloat32(specR, coeffsR, energies[end:], 0, end, lm, edges, downsample)
		if withQEXT && qext.coeffsR != nil {
			denormalizeBandsPackedDownsampleIntoFloat32(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands, downsample)
		}
	}
	return d.synthesizeFrame(specL, specR, frameSize, modeLM, shortBlocks, transient, postfilterPeriod, postfilterGain, postfilterTapset)
}

func (d *Decoder) finalizeDecodedFrameState(frameSize, start, end, lm int, transient bool, energies []celtGLog, qext *preparedQEXTDecode, rd *rangecoding.Decoder) error {
	d.updateEnergyHistory(energies, start, end, lm, transient)
	channels := int(d.channels)
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

// updateEnergyHistory is the band-energy history update that ends
// celt_decode_with_ec(): it stores the frame's band energies (compact layout
// [c*end+band]) as oldBandE, mirrors a mono stream's energies into the second
// channel, advances oldLogE/oldLogE2 and the backgroundLogE floor over both
// channels, and resets the bands outside [start,end).
func (d *Decoder) updateEnergyHistory(energies []celtGLog, start, end, lm int, transient bool) {
	d.ensureBackgroundEnergyState()
	stride := d.predStride()
	nbEBands := min(d.modeNbEBands(), stride)
	oldBandE := d.prevEnergy[:2*stride]
	oldLogE := d.prevLogE[:2*stride]
	oldLogE2 := d.prevLogE2[:2*stride]
	backgroundLogE := d.backgroundEnergy[:2*stride]
	channels := int(d.channels)
	for c := range channels {
		copy(oldBandE[c*stride:c*stride+end], energies[c*end:(c+1)*end])
	}
	if channels == 1 {
		copy(oldBandE[stride:stride+nbEBands], oldBandE[:nbEBands])
	}
	if !transient {
		copy(oldLogE2, oldLogE)
		copy(oldLogE, oldBandE)
	} else {
		for i, e := range oldBandE {
			// MING(oldLogE[i], oldBandE[i])
			if !(oldLogE[i] < e) {
				oldLogE[i] = e
			}
		}
	}
	// In normal circumstances the noise floor rises by at most 2.4 dB/s;
	// after a loss (DTX) it may rise by the whole missing duration.
	maxBackgroundIncrease := float32(min(int(d.plcLossDuration)+1<<uint(lm), 160)) * 0.001
	for i, e := range oldBandE {
		// MING(backgroundLogE[i] + max_background_increase, oldBandE[i])
		bg := backgroundLogE[i] + maxBackgroundIncrease
		if !(bg < e) {
			bg = e
		}
		backgroundLogE[i] = bg
	}
	for c := range 2 {
		bandE := oldBandE[c*stride : c*stride+nbEBands]
		logE := oldLogE[c*stride : c*stride+nbEBands]
		logE2 := oldLogE2[c*stride : c*stride+nbEBands]
		for i := range start {
			bandE[i] = 0
			logE[i], logE2[i] = -28, -28
		}
		for i := end; i < nbEBands; i++ {
			bandE[i] = 0
			logE[i], logE2[i] = -28, -28
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
