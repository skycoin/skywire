// This file implements DTX (Discontinuous Transmission) for the Opus encoder.
// DTX saves bandwidth during silence by emitting 1-byte TOC-only packets,
// allowing the decoder to activate its internal Comfort Noise Generation (CNG).
//
// Activity detection matches libopus opus_encoder.c:1911-1930:
//  1. is_digital_silence: max sample below quantization floor
//  2. Analysis-based: tonality analyzer activity_probability >= 0.1
//  3. CELT fallback: peak-vs-current energy pseudo-SNR check
//
// The SILK multi-band VAD is NOT used for Opus-level DTX (only for SILK-internal DTX).
//
// Reference: RFC 6716 Section 2.1.9, libopus opus_encoder.c, silk/define.h

package encoder

// DTX Constants matching libopus silk/define.h and opus_encoder.c
const (
	// DTXFrameThresholdMs is the duration of silence before DTX activates.
	// Matches NB_SPEECH_FRAMES_BEFORE_DTX * 20 = 200ms.
	DTXFrameThresholdMs = 200

	// DTXMaxConsecutiveMs is the maximum duration for DTX mode.
	// Matches MAX_CONSECUTIVE_DTX * 20 = 400ms.
	DTXMaxConsecutiveMs = 400

	// dtxActivityThreshold matches DTX_ACTIVITY_THRESHOLD = 0.1f from silk/define.h.
	// Used with the tonality analyzer's activity_probability.
	dtxActivityThreshold opusVal16 = 0.1

	// pseudoSNRThreshold matches PSEUDO_SNR_THRESHOLD = 316.23f (10^(25/10))
	// from opus_encoder.c. If peak energy < threshold * current energy,
	// the frame is considered active (not silence).
	pseudoSNRThreshold opusVal16 = 316.23
)

// dtxState holds state for discontinuous transmission.
type dtxState struct {
	// Multi-band VAD state for SILK-mode DTX speech detection
	vad *VADState

	// Counter for consecutive no-activity frames in milliseconds (Q1 format)
	noActivityMsQ1 int32

	// Whether currently in DTX mode (suppressing frames)
	inDTXMode bool

	// Frame duration in milliseconds (for timing calculations)
	frameDurationMs int32

	// Peak signal energy tracker (matching libopus st->peak_signal_energy).
	// Tracks the running peak energy of active frames with slow decay (0.999).
	peakSignalEnergy opusVal32
}

// newDTXState creates initial DTX state with multi-band VAD.
func newDTXState() *dtxState {
	return &dtxState{
		vad:             NewVADState(),
		noActivityMsQ1:  0,
		inDTXMode:       false,
		frameDurationMs: 20, // Default 20ms frames
	}
}

// reset resets DTX state when speech resumes.
func (d *dtxState) reset() {
	d.noActivityMsQ1 = 0
	d.inDTXMode = false
	d.peakSignalEnergy = 0
	// Note: VAD state is NOT reset - noise estimates should persist
}

// isDigitalSilenceRes checks if the PCM frame is true digital silence.
// Matches libopus is_digital_silence() from opus_encoder.c:1060-1077.
//
// For float-point: silence = (sample_max <= 1.0 / (1 << lsb_depth))
// At 24-bit depth: threshold is about 5.96e-8.
func isDigitalSilenceRes(pcm []opusRes, lsbDepth int32) bool {
	if lsbDepth < 8 {
		lsbDepth = 8
	}
	if lsbDepth > 24 {
		lsbDepth = 24
	}
	threshold := opusVal16(1.0 / opusVal16(int32(1)<<uint(lsbDepth)))

	for _, v := range pcm {
		if v > threshold || v < -threshold {
			return false
		}
	}
	return true
}

// computeFrameEnergyRes computes mean energy of the PCM frame.
// Matches libopus compute_frame_energy() from opus_encoder.c:1107-1111.
func computeFrameEnergyRes(pcm []opusRes) opusVal32 {
	if len(pcm) == 0 {
		return 0
	}
	var energy opusVal32
	for _, v := range pcm {
		energy += v * v
	}
	return energy / opusVal32(len(pcm))
}

// shouldUseDTXRes determines if frame should be suppressed (DTX mode).
//
// Activity detection matches libopus opus_encoder.c:1911-1930:
//  1. is_digital_silence -> inactive
//  2. analysis_info.valid -> activity_probability >= DTX_ACTIVITY_THRESHOLD,
//     with pseudo-SNR energy check as safety net
//  3. CELT-only fallback -> peak energy vs current energy pseudo-SNR check
//
// The SILK multi-band VAD is NOT used here (it's only for SILK-internal DTX).
//
// Returns: (suppressFrame bool, sendComfortNoise bool)
func (e *Encoder) shouldUseDTXRes(pcm []opusRes) (bool, bool) {
	if !e.dtxEnabled || e.dtx == nil {
		if e.dtx != nil {
			e.dtx.noActivityMsQ1 = 0
			e.dtx.inDTXMode = false
		}
		return false, false
	}

	frameLength := len(pcm)
	if e.channels == 2 {
		frameLength /= 2
	}
	fsKHz := int(e.sampleRate) / 1000
	switch fsKHz {
	case 8, 12, 16, 24, 48:
	default:
		fsKHz = 48
	}
	frameDurationMs := (frameLength * 1000) / (fsKHz * 1000)
	if frameDurationMs <= 0 {
		frameDurationMs = 20
	}
	e.dtx.frameDurationMs = int32(frameDurationMs)

	isSilence := isDigitalSilenceRes(pcm, e.lsbDepth)

	var isActive bool
	if isSilence {
		isActive = false
	} else if e.lastAnalysisValid {
		isActive = e.lastAnalysisInfo.VADProb >= dtxActivityThreshold
		if !isActive {
			frameEnergy := computeFrameEnergyRes(pcm)
			isActive = e.dtx.peakSignalEnergy < pseudoSNRThreshold*frameEnergy
		}
	} else {
		frameEnergy := computeFrameEnergyRes(pcm)
		isActive = e.dtx.peakSignalEnergy < pseudoSNRThreshold*0.5*frameEnergy
	}

	shouldTrackPeak := true
	if e.lastAnalysisValid && e.lastAnalysisInfo.VADProb <= dtxActivityThreshold {
		shouldTrackPeak = false
	}
	if shouldTrackPeak && !isSilence {
		frameEnergy := computeFrameEnergyRes(pcm)
		e.dtx.peakSignalEnergy = maxf(0.999*e.dtx.peakSignalEnergy, frameEnergy)
	}

	frameSizeMsQ1 := int32(frameDurationMs * 2)

	if !isActive {
		e.dtx.noActivityMsQ1 += frameSizeMsQ1

		thresholdMsQ1 := int32(NBSpeechFramesBeforeDTX * 20 * 2)
		maxDTXMsQ1 := int32((NBSpeechFramesBeforeDTX + MaxConsecutiveDTX) * 20 * 2)

		if e.dtx.noActivityMsQ1 > thresholdMsQ1 {
			if e.dtx.noActivityMsQ1 <= maxDTXMsQ1 {
				e.dtx.inDTXMode = true
				return true, false
			}
			e.dtx.noActivityMsQ1 = thresholdMsQ1
			e.dtx.inDTXMode = false
		}
	} else {
		e.dtx.noActivityMsQ1 = 0
		e.dtx.inDTXMode = false
	}

	return false, false
}

// frameSizeMsQ1 returns the Q1-millisecond duration of a frame, matching
// libopus's 2*1000*frame_size/st->Fs integer arithmetic (opus_encoder.c:2567).
func (e *Encoder) frameSizeMsQ1(frameSize int) int32 {
	fs := int(e.sampleRate)
	if fs <= 0 {
		fs = 48000
	}
	return int32(2 * 1000 * frameSize / fs)
}

// decideDTXSuppress runs libopus decide_dtx_mode (opus_encoder.c:1115-1140),
// called after the frame has been fully encoded so that the encoder state is
// advanced exactly as libopus does before discarding the payload for a DTX
// continuation packet (opus_encoder.c:2564-2572).
//
// activity is the resolved opus_int activity for this frame: for the SILK
// VAD_NO_DECISION path libopus resolves it to signalType != TYPE_NO_VOICE_ACTIVITY
// before this point (opus_encoder.c:2235).
//
// Returns true if the frame should be emitted as a 1-byte TOC-only DTX packet.
func (e *Encoder) decideDTXSuppress(activity bool, frameSize int) bool {
	if !e.dtxEnabled || e.dtx == nil {
		if e.dtx != nil {
			e.dtx.noActivityMsQ1 = 0
			e.dtx.inDTXMode = false
		}
		return false
	}

	if activity {
		e.dtx.noActivityMsQ1 = 0
		e.dtx.inDTXMode = false
		return false
	}

	e.dtx.noActivityMsQ1 += e.frameSizeMsQ1(frameSize)

	thresholdMsQ1 := int32(NBSpeechFramesBeforeDTX * 20 * 2)
	maxDTXMsQ1 := int32((NBSpeechFramesBeforeDTX + MaxConsecutiveDTX) * 20 * 2)

	if e.dtx.noActivityMsQ1 > thresholdMsQ1 {
		if e.dtx.noActivityMsQ1 <= maxDTXMsQ1 {
			e.dtx.inDTXMode = true
			return true
		}
		e.dtx.noActivityMsQ1 = thresholdMsQ1
		e.dtx.inDTXMode = false
	}
	return false
}

// subframeDTXSuppress runs the per-sub-frame Opus-level DTX decision for one
// internal frame of a multi-frame packet. It mirrors libopus
// opus_encode_frame_native, which computes the per-frame activity (analysis /
// peak-energy branch) and then calls decide_dtx_mode once per sub-frame with the
// sub-frame's duration (opus_encoder.c:1911-1930, 2564-2572). It must be called
// AFTER the sub-frame is encoded so the encoder state has already advanced
// exactly as libopus does before the payload is discarded for a suppressed
// sub-frame.
//
// subVADPCM is the unfiltered sub-frame PCM (the same buffer the whole-frame VAD
// would use). When vadAlreadyComputed is true the caller has already populated
// the Opus-level VAD decision (e.g. the DRED path ran updateOpusVADRes for this
// sub-frame), so the activity is read back from that decision instead of being
// recomputed — this avoids double-counting peak_signal_energy.
//
// Returns true when the sub-frame should be emitted as a length-0 (suppressed)
// frame in the repacketized packet.
func (e *Encoder) subframeDTXSuppress(mode Mode, subVADPCM []opusRes, subFrameSize int, vadAlreadyComputed bool) bool {
	if !e.dtxEnabled || e.dtx == nil {
		return false
	}
	if !vadAlreadyComputed {
		// Compute the Opus-level activity + peak-energy tracking for this
		// sub-frame exactly as the whole-frame path does for short packets.
		if mode == ModeCELT {
			e.updateCELTOnlyOpusVADRes(subVADPCM, subFrameSize)
		} else {
			e.updateOpusVADRes(subVADPCM, subFrameSize)
		}
	}
	activity := e.resolveDTXActivity()
	return e.decideDTXSuppress(activity, subFrameSize)
}

// InDTX returns whether the encoder is currently in DTX mode.
// This matches OPUS_GET_IN_DTX from libopus.
func (e *Encoder) InDTX() bool {
	if !e.dtxEnabled || e.dtx == nil {
		return false
	}
	return e.dtx.noActivityMsQ1 >= NBSpeechFramesBeforeDTX*20*2
}

// GetVADActivity returns the current VAD speech activity level (0-255).
func (e *Encoder) GetVADActivity() int {
	if e.dtx == nil || e.dtx.vad == nil {
		return 0
	}
	return int(e.dtx.vad.SpeechActivityQ8)
}

// classifySignal determines signal type using energy-based detection.
// This is a legacy function kept for compatibility; new code uses VAD.
// Returns: 0 = inactive (silence), 1 = unvoiced, 2 = voiced
func classifySignal(pcm []float32) (int, float32) {
	if len(pcm) == 0 {
		return 0, 0
	}

	var energy opusVal32
	for _, s := range pcm {
		energy += s * s
	}
	energy /= opusVal32(len(pcm))

	const silenceThreshold = 0.0001 // ~-40 dBFS
	if energy < silenceThreshold {
		return 0, energy
	}

	return 2, energy
}
