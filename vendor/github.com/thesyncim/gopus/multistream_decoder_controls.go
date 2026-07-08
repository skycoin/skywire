package gopus

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *MultistreamDecoder) Reset() {
	d.dec.Reset()
	d.lastFrameSize = d.sampleRate / 50
	clear(d.softClipMem)
}

// Channels returns the number of audio channels.
func (d *MultistreamDecoder) Channels() int {
	return int(d.channels)
}

// SampleRate returns the sample rate in Hz.
func (d *MultistreamDecoder) SampleRate() int {
	return int(d.sampleRate)
}

// Streams returns the total number of elementary streams.
func (d *MultistreamDecoder) Streams() int {
	return d.dec.Streams()
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (d *MultistreamDecoder) CoupledStreams() int {
	return d.dec.CoupledStreams()
}

// SetGain sets output gain in Q8 dB units on all elementary stream decoders.
func (d *MultistreamDecoder) SetGain(gainQ8 int) error {
	if gainQ8 < -32768 || gainQ8 > 32767 {
		return ErrInvalidGain
	}
	return d.dec.SetGain(gainQ8)
}

// Gain returns the output gain from the first elementary stream decoder.
func (d *MultistreamDecoder) Gain() int {
	return d.dec.Gain()
}

// SetPhaseInversionDisabled toggles CELT stereo phase inversion on all streams.
func (d *MultistreamDecoder) SetPhaseInversionDisabled(disabled bool) {
	d.dec.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports the first stream decoder's phase inversion setting.
func (d *MultistreamDecoder) PhaseInversionDisabled() bool {
	return d.dec.PhaseInversionDisabled()
}

// SetComplexity sets decoder complexity (0-10) on all elementary streams.
func (d *MultistreamDecoder) SetComplexity(complexity int) error {
	if err := validateComplexity(complexity); err != nil {
		return err
	}
	return d.dec.SetComplexity(complexity)
}

// Complexity returns the decoder complexity from the first elementary stream.
func (d *MultistreamDecoder) Complexity() int {
	return d.dec.Complexity()
}

// Bandwidth returns the bandwidth of the first elementary stream decoder.
func (d *MultistreamDecoder) Bandwidth() Bandwidth {
	return Bandwidth(d.dec.Bandwidth())
}

// LastPacketDuration returns the last packet duration from the first stream decoder.
func (d *MultistreamDecoder) LastPacketDuration() int {
	return d.dec.LastPacketDuration()
}

// GetFinalRange returns the XOR of all elementary stream final range values.
func (d *MultistreamDecoder) GetFinalRange() uint32 {
	return d.dec.GetFinalRange()
}

// FinalRange returns the XOR of all elementary stream final range values.
func (d *MultistreamDecoder) FinalRange() uint32 {
	return d.GetFinalRange()
}

// SetIgnoreExtensions toggles whether unknown packet extensions should be ignored.
func (d *MultistreamDecoder) SetIgnoreExtensions(ignore bool) {
	d.ignoreExtensions = ignore
	if d.dec != nil {
		d.dec.SetIgnoreExtensions(ignore)
	}
}

// IgnoreExtensions reports whether unknown packet extensions are ignored.
func (d *MultistreamDecoder) IgnoreExtensions() bool {
	return d.ignoreExtensions
}
