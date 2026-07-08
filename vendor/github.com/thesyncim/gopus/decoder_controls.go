package gopus

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.silkDecoder.Reset()
	d.celtDecoder.Reset()
	d.hybridDecoder.Reset()
	// Use the internal 48 kHz rate for lastFrameSize when the API is 96 kHz.
	// C ref: opus_decoder.c OPUS_RESET_STATE, st->frame_size = Fs/400.
	internalRate := int32(d.sampleRate)
	if d.is96kHz() {
		internalRate = 48000
	}
	d.lastFrameSize = internalRate / 50
	d.lastPacketDuration = 0
	d.lastDataLen = 0
	d.mainDecodeRng = 0
	d.redundantRng = 0
	d.prevMode = ModeHybrid
	d.lastPacketMode = ModeHybrid
	d.lastBandwidth = BandwidthFullband
	d.bandwidthKnown = false
	d.prevRedundancy = false
	d.prevPacketStereo = false
	d.haveDecoded = false
	d.clearSoftClipMem()
	d.clearDREDPayloadState()
	d.resetDREDRuntimeState()
	d.resetDRED48kNeuralBridge()
	d.resetOSCELACEPostfilterState(d.channels == 2)
	d.resetOSCEBWEPostfilterState()

	// Clear FEC state
	d.clearFECState()
}

func (d *Decoder) clearSoftClipMem() {
	d.softClipMem[0] = 0
	d.softClipMem[1] = 0
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return int(d.channels)
}

// SampleRate returns the sample rate in Hz.
func (d *Decoder) SampleRate() int {
	return int(d.sampleRate)
}

// SetGain sets output gain in Q8 dB units (libopus OPUS_SET_GAIN semantics).
//
// Valid range is [-32768, 32767], where 256 = +1 dB and -256 = -1 dB.
func (d *Decoder) SetGain(gainQ8 int) error {
	if gainQ8 < -32768 || gainQ8 > 32767 {
		return ErrInvalidGain
	}
	d.decodeGainQ8 = gainQ8
	return nil
}

// Gain returns the current decoder output gain in Q8 dB units.
func (d *Decoder) Gain() int {
	return d.decodeGainQ8
}

// SetPhaseInversionDisabled toggles CELT stereo phase inversion during decoding.
func (d *Decoder) SetPhaseInversionDisabled(disabled bool) {
	d.celtDecoder.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports whether CELT stereo phase inversion is disabled.
func (d *Decoder) PhaseInversionDisabled() bool {
	return d.celtDecoder.PhaseInversionDisabled()
}

// SetComplexity sets decoder complexity (0-10).
func (d *Decoder) SetComplexity(complexity int) error {
	if err := validateComplexity(complexity); err != nil {
		return err
	}
	if err := d.celtDecoder.SetComplexity(complexity); err != nil {
		return err
	}
	if err := d.hybridDecoder.SetComplexity(complexity); err != nil {
		return err
	}
	d.complexity = int32(complexity)
	return nil
}

// Complexity returns the current decoder complexity setting.
func (d *Decoder) Complexity() int {
	return int(d.complexity)
}

// SetIgnoreExtensions toggles whether unknown packet extensions should be ignored.
//
// This mirrors libopus OPUS_SET_IGNORE_EXTENSIONS semantics.
func (d *Decoder) SetIgnoreExtensions(ignore bool) {
	d.ignoreExtensions = ignore
	if ignore {
		d.clearDREDPayloadState()
	}
}

// IgnoreExtensions reports whether unknown packet extensions are ignored.
func (d *Decoder) IgnoreExtensions() bool {
	return d.ignoreExtensions
}

// Pitch returns the most recent decoded pitch period.
func (d *Decoder) Pitch() int {
	if d.lastPacketMode == ModeCELT {
		if d.celtDecoder == nil {
			return 0
		}
		return d.celtDecoder.PostfilterPeriod()
	}
	if d.silkDecoder == nil || d.silkDecoder.GetLastSignalType() != 2 {
		return 0
	}
	return d.silkDecoder.GetLagPrev() * silkPitchScale(d.lastBandwidth)
}

func silkPitchScale(bandwidth Bandwidth) int {
	switch bandwidth {
	case BandwidthNarrowband:
		return 6
	case BandwidthMediumband:
		return 4
	default:
		return 3
	}
}

// Bandwidth returns the bandwidth of the last successfully decoded packet.
//
// Returns 0 before any non-PLC packet has been decoded, matching libopus
// OPUS_GET_BANDWIDTH which returns st->bandwidth (zeroed by OPUS_CLEAR at init
// and after OPUS_RESET_STATE).
//
// C ref: opus_decoder.c OPUS_GET_BANDWIDTH_REQUEST → "*value = st->bandwidth"
func (d *Decoder) Bandwidth() Bandwidth {
	if !d.bandwidthKnown {
		return 0
	}
	return d.lastBandwidth
}

// LastPacketDuration returns the duration in samples per channel at the decoder API rate.
func (d *Decoder) LastPacketDuration() int {
	return int(d.lastPacketDuration)
}

// InDTX reports whether the most recently decoded packet was a DTX packet.
func (d *Decoder) InDTX() bool {
	return d.lastDataLen > 0 && d.lastDataLen <= 2
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after Decode() to get a meaningful value.
//
// Per libopus, the final range is XORed with any redundancy frame's range.
// If the packet length was <= 1, FinalRange returns 0.
func (d *Decoder) FinalRange() uint32 {
	// Per libopus: if len <= 1, rangeFinal = 0
	if d.lastDataLen <= 1 {
		return 0
	}

	// Use the captured main decode range (not the current decoder state,
	// which may have been modified by redundancy decoding)
	return d.mainDecodeRng ^ d.redundantRng
}
