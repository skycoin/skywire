package celt

import "github.com/thesyncim/gopus/internal/extsupport"

// handleChannelTransition detects and handles mono-to-stereo channel transitions.
// When transitioning from mono to stereo, the right channel overlap buffer must be
// initialized from the left channel to match libopus behavior.
// This ensures smooth crossfade during the transition.
//
// Additionally, energy history arrays (prevEnergy, prevEnergy2, prevLogE, prevLogE2)
// must be copied/initialized for the right channel. In libopus, mono frames always
// copy their energy to the right channel position after decoding:
//
//	if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands);
//
// This means when transitioning to stereo, the right channel already has valid energy.
// We replicate this behavior here for transitions.
//
// Reference: libopus celt/celt_decoder.c - mono-to-stereo handling
//
// Returns true if a mono-to-stereo transition occurred.
func (d *Decoder) handleChannelTransition(streamChannels int) bool {
	prevChannels := d.prevStreamChannels
	d.prevStreamChannels = int32(streamChannels)

	// Detect mono-to-stereo transition: previous was mono (1), current is stereo (2)
	if prevChannels == 1 && streamChannels == 2 && d.channels == 2 {
		// Copy left channel overlap buffer to right channel for smooth transition
		// Overlap buffer layout: [Left: 0..Overlap-1] [Right: Overlap..2*Overlap-1]
		// This matches libopus which copies decode_mem[0] to decode_mem[1] on transition
		if len(d.overlapBuffer) >= Overlap*2 {
			for i := range Overlap {
				d.overlapBuffer[Overlap+i] = d.overlapBuffer[i]
			}
		}

		// Copy left channel energy state to right channel.
		// This matches libopus behavior where mono frames always update both channels'
		// energy history (via OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)).
		// Energy arrays layout: [Left: 0..stride-1] [Right: stride..2*stride-1],
		// where stride is MaxBands for the static codec and the mode's nbEBands for
		// a per-mode custom layout.
		stride := d.predStride()
		if len(d.prevEnergy) >= stride*2 {
			for i := range stride {
				d.prevEnergy[stride+i] = d.prevEnergy[i]
			}
		}
		if len(d.prevEnergy2) >= stride*2 {
			for i := range stride {
				d.prevEnergy2[stride+i] = d.prevEnergy2[i]
			}
		}
		if len(d.prevLogE) >= stride*2 {
			for i := range stride {
				d.prevLogE[stride+i] = d.prevLogE[i]
			}
		}
		if len(d.prevLogE2) >= stride*2 {
			for i := range stride {
				d.prevLogE2[stride+i] = d.prevLogE2[i]
			}
		}
		if len(d.backgroundEnergy) >= stride*2 {
			for i := range stride {
				d.backgroundEnergy[stride+i] = d.backgroundEnergy[i]
			}
		}

		// NOTE: preemphState is NOT copied during transition.
		// In libopus, each channel maintains its own independent de-emphasis filter state.
		// During mono packets on a stereo decoder, both states are updated independently
		// (with the same input but different state histories). At transition to stereo,
		// each channel continues with its own state - no copying is done.

		return true
	}

	// Detect stereo-to-mono transition: previous was stereo (2), current is mono (1)
	if prevChannels == 2 && streamChannels == 1 && d.channels == 2 {
		return true
	}

	return false
}

// ensureEnergyState ensures the decoder has room for the requested channel count
// in its energy/history arrays. This is needed for stereo packets when output is mono.
func (d *Decoder) ensureEnergyState(channels int) {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	needed := MaxBands * channels
	if len(d.prevEnergy) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevEnergy)
		d.prevEnergy = prev
	}
	if len(d.prevEnergy2) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevEnergy2)
		d.prevEnergy2 = prev
	}
	if len(d.prevLogE) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevLogE)
		for i := len(d.prevLogE); i < needed; i++ {
			prev[i] = -28.0
		}
		d.prevLogE = prev
	}
	if len(d.prevLogE2) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.prevLogE2)
		for i := len(d.prevLogE2); i < needed; i++ {
			prev[i] = -28.0
		}
		d.prevLogE2 = prev
	}
	if len(d.backgroundEnergy) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, d.backgroundEnergy)
		for i := len(d.backgroundEnergy); i < needed; i++ {
			prev[i] = 0
		}
		d.backgroundEnergy = prev
	}
	if extsupport.QEXT {
		d.growQEXTOldBandE(needed)
	}
}

func (d *Decoder) ensureQEXTOldBandE() []celtGLog {
	needed := nbQEXTBands * int(d.channels)
	qextState := d.ensureQEXTState()
	if len(qextState.oldBandE) < needed {
		prev := make([]celtGLog, needed)
		copy(prev, qextState.oldBandE)
		qextState.oldBandE = prev
	}
	return qextState.oldBandE
}

func (d *Decoder) allocationScratch() []int32 {
	return ensureInt32Slice(&d.scratchAllocWork, MaxBands*5)
}

func (d *Decoder) snapshotDecodeHistory() ([]celtGLog, []celtGLog, []celtGLog) {
	prev1Energy := ensureGLogSlice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	return prev1Energy, d.prevLogE, d.prevLogE2
}

// prepareMonoEnergyFromStereo mirrors libopus behavior for mono streams by
// using the max of L/R energies for prediction when stereo history exists.
func (d *Decoder) prepareMonoEnergyFromStereo() {
	stride := d.predStride()
	if d.channels != 1 || len(d.prevEnergy) < stride*2 {
		return
	}
	for i := range stride {
		right := d.prevEnergy[stride+i]
		if right > d.prevEnergy[i] {
			d.prevEnergy[i] = right
		}
	}
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy decoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (d *Decoder) PrevEnergy() []float32 {
	out := make([]float32, len(d.prevEnergy))
	copy(out, d.prevEnergy)
	return out
}

// PrevEnergy2 returns the band energies from two frames ago.
// Used for anti-collapse detection.
func (d *Decoder) PrevEnergy2() []float32 {
	out := make([]float32, len(d.prevEnergy2))
	copy(out, d.prevEnergy2)
	return out
}

// SetPrevEnergy copies the given energies to the previous energy buffer.
// Also shifts current prev to prev2.
func (d *Decoder) SetPrevEnergy(energies []float32) {
	// Shift: current prev becomes prev2
	copy(d.prevEnergy2, d.prevEnergy)
	// Copy new energies to prev
	copy(d.prevEnergy, energies)
}

// SetPrevEnergyWithPrev updates prevEnergy using the provided previous state.
// This avoids losing the prior frame when prevEnergy is updated during decoding.
// The energies array uses compact layout [L0..L(n-1), R0..R(n-1)] where n = nbBands.
// The prevEnergy array uses full layout [L0..L20, R0..R20] where 21 = MaxBands.
func (d *Decoder) SetPrevEnergyWithPrev(prev, energies []float32) {
	if len(prev) == len(d.prevEnergy2) {
		copy(d.prevEnergy2, prev)
	} else {
		copy(d.prevEnergy2, d.prevEnergy)
	}

	// Determine nbBands from the energies array length
	channels := int(d.channels)
	nbBands := min(len(energies)/channels, MaxBands)

	// Copy with layout conversion: compact [c*nbBands+band] -> prediction-stride
	// [c*predStride+band] (predStride == MaxBands for the static codec, the mode's
	// nbEBands for a per-mode custom layout).
	stride := d.predStride()
	for c := range channels {
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := c*stride + band
			if src < len(energies) {
				d.prevEnergy[dst] = energies[src]
			}
		}
	}
}

func (d *Decoder) setPrevEnergyGLog(energies []celtGLog) {
	d.setPrevEnergyGLogWithPrev(nil, energies)
}

func (d *Decoder) setPrevEnergyGLogWithPrev(prev []celtGLog, energies []celtGLog) {
	if len(prev) == len(d.prevEnergy2) {
		copy(d.prevEnergy2, prev)
	} else {
		copy(d.prevEnergy2, d.prevEnergy)
	}

	// Determine nbBands from the energies array length
	channels := int(d.channels)
	nbBands := min(len(energies)/channels, MaxBands)

	// Copy with layout conversion: compact [c*nbBands+band] -> prediction-stride.
	stride := d.predStride()
	for c := range channels {
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := c*stride + band
			if src < len(energies) {
				d.prevEnergy[dst] = energies[src]
			}
		}
	}
}

func (d *Decoder) updateLogEGLog(energies []celtGLog, nbBands int, transient bool) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	channels := int(d.channels)
	if len(energies) < nbBands*channels {
		nbBands = len(energies) / channels
	}
	if nbBands <= 0 {
		return
	}

	if !transient {
		copy(d.prevLogE2, d.prevLogE)
	}
	stride := d.predStride()
	for c := range channels {
		base := c * stride
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := base + band
			e := energies[src]
			if transient {
				if e < d.prevLogE[dst] {
					d.prevLogE[dst] = e
				}
			} else {
				d.prevLogE[dst] = e
			}
		}
	}
}

func (d *Decoder) ensureBackgroundEnergyState() {
	if len(d.backgroundEnergy) == len(d.prevEnergy) {
		return
	}
	if len(d.backgroundEnergy) < len(d.prevEnergy) {
		prev := make([]celtGLog, len(d.prevEnergy))
		copy(prev, d.backgroundEnergy)
		for i := len(d.backgroundEnergy); i < len(prev); i++ {
			prev[i] = 0
		}
		d.backgroundEnergy = prev
		return
	}
	d.backgroundEnergy = d.backgroundEnergy[:len(d.prevEnergy)]
}

func (d *Decoder) updateBackgroundEnergy(lm int) {
	d.ensureBackgroundEnergyState()
	if lm < 0 {
		lm = 0
	}
	if lm > 30 {
		lm = 30
	}
	m := 1 << uint(lm)
	maxIncUnits := min(int(d.plcLossDuration)+m, 160)
	maxBackgroundIncrease := celtGLog(float32(maxIncUnits) * 0.001)
	for i := range d.backgroundEnergy {
		bg := d.backgroundEnergy[i] + maxBackgroundIncrease
		e := d.prevEnergy[i]
		if bg > e {
			bg = e
		}
		d.backgroundEnergy[i] = bg
	}
}
