package celt

// OverlapBuffer returns the overlap buffer for CELT overlap.
// Size is Overlap * channels samples.
func (d *Decoder) OverlapBuffer() []float32 {
	return d.overlapBuffer
}

// SetOverlapBuffer copies the given samples to the overlap buffer.
func (d *Decoder) SetOverlapBuffer(samples []float32) {
	copy(d.overlapBuffer, samples)
}

// PreemphState returns the de-emphasis filter state.
// One value per channel.
func (d *Decoder) PreemphState() []float32 {
	return d.preemphState
}

// SetPreemphState copies the given samples to the de-emphasis memory.
func (d *Decoder) SetPreemphState(samples []float32) {
	copy(d.preemphState, samples)
}

// PostfilterPeriod returns the pitch period for the postfilter.
func (d *Decoder) PostfilterPeriod() int {
	return int(d.postfilterPeriod)
}

// PostfilterGain returns the comb filter gain.
func (d *Decoder) PostfilterGain() float32 {
	return d.postfilterGain
}

// PostfilterTapset returns the filter tap configuration.
func (d *Decoder) PostfilterTapset() int {
	return int(d.postfilterTapset)
}

// PostfilterState is a snapshot of the decoder postfilter parameters for the
// current and previous frame.
type PostfilterState struct {
	Period         int
	Gain           float32
	Tapset         int
	PreviousPeriod int
	PreviousGain   float32
	PreviousTapset int
}

// PostfilterState returns the current and previous postfilter parameters. It
// returns the zero value when d is nil.
func (d *Decoder) PostfilterState() PostfilterState {
	if d == nil {
		return PostfilterState{}
	}
	return PostfilterState{
		Period:         int(d.postfilterPeriod),
		Gain:           d.postfilterGain,
		Tapset:         int(d.postfilterTapset),
		PreviousPeriod: int(d.postfilterPeriodOld),
		PreviousGain:   d.postfilterGainOld,
		PreviousTapset: int(d.postfilterTapsetOld),
	}
}

// SetPostfilter sets the postfilter parameters.
func (d *Decoder) SetPostfilter(period int, gain float32, tapset int) {
	d.postfilterPeriod = int32(period)
	d.postfilterGain = gain
	d.postfilterTapset = int32(tapset)
}

// RNG returns the current RNG state.
func (d *Decoder) RNG() uint32 {
	return d.rng
}

// SetRNG sets the RNG state.
func (d *Decoder) SetRNG(seed uint32) {
	d.rng = seed
}

// NextRNG advances the RNG and returns the new value.
// Uses the same LCG as libopus for deterministic behavior.
func (d *Decoder) NextRNG() uint32 {
	d.rng = d.rng*1664525 + 1013904223
	return d.rng
}

// GetEnergy returns the energy for a specific band and channel from prevEnergy.
func (d *Decoder) GetEnergy(band, channel int) float32 {
	stride := d.predStride()
	if band < 0 || band >= stride || channel < 0 || channel >= int(d.channels) {
		return 0
	}
	return float32(d.prevEnergy[channel*stride+band])
}

// SetEnergy sets the energy for a specific band and channel.
func (d *Decoder) SetEnergy(band, channel int, energy float32) {
	stride := d.predStride()
	if band < 0 || band >= stride || channel < 0 || channel >= int(d.channels) {
		return
	}
	d.prevEnergy[channel*stride+band] = celtGLog(energy)
}
