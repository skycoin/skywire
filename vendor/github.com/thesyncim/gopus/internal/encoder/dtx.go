// This file implements the Opus-level DTX activity decision for the encoder.
// A suppressed single frame becomes a TOC-only packet; multi-frame packets keep
// their subframe-count framing. The decoder handles TOC-only DTX packets
// through its concealment path. SILK internal DTX remains a separate decision.
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
	// Standalone VAD storage; Opus-level DTX uses the frame activity decision.
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
	n := len(pcm)
	if n == 0 {
		return 0
	}
	var a0, a1, a2, a3 opusVal32
	for len(pcm) >= 4 {
		a0 += pcm[0] * pcm[0]
		a1 += pcm[1] * pcm[1]
		a2 += pcm[2] * pcm[2]
		a3 += pcm[3] * pcm[3]
		pcm = pcm[4:]
	}
	for _, v := range pcm {
		a0 += v * v
	}
	return (a0 + a1 + a2 + a3) / opusVal32(n)
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

	shouldTrackPeak := !(e.lastAnalysisValid && e.lastAnalysisInfo.VADProb <= dtxActivityThreshold)

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
// continuation packet (opus_encoder.c:2564-2572). The Opus-level decision only
// runs while SILK's own DTX is off; otherwise, and without DTX, the inactivity
// run restarts.
//
// activity is the resolved opus_int activity for this frame: for the SILK
// VAD_NO_DECISION path libopus resolves it to signalType != TYPE_NO_VOICE_ACTIVITY
// before this point (opus_encoder.c:2235).
//
// Returns true if the frame should be emitted as a 1-byte TOC-only DTX packet.
func (e *Encoder) decideDTXSuppress(activity bool, frameSize int) bool {
	if !e.dtxEnabled || e.silkMode.UseDTX || e.dtx == nil {
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

// subframeDTXSuppress runs decide_dtx_mode for a coded frame of a multi-frame
// packet on the activity decided for it (src/opus_encoder.c:2564-2572) and
// reports whether the frame becomes a DTX frame.
func (e *Encoder) subframeDTXSuppress(subFrameSize int) bool {
	if !e.dtxEnabled || e.dtx == nil {
		return false
	}
	return e.decideDTXSuppress(e.resolveDTXActivity(), subFrameSize)
}

// InDTX returns whether the encoder is currently in DTX mode, matching
// OPUS_GET_IN_DTX (src/opus_encoder.c): after a SILK or Hybrid frame coded
// with SILK's own DTX, SILK's no-speech run decides; otherwise the Opus-level
// inactivity run does.
func (e *Encoder) InDTX() bool {
	if e.silkMode.UseDTX && (e.prevMode == ModeSILK || e.prevMode == ModeHybrid) && e.silk != nil {
		return e.silk.InDTX(e.silkMode.NChannelsInternal)
	}
	if !e.dtxEnabled || e.dtx == nil {
		return false
	}
	return e.dtx.noActivityMsQ1 >= NBSpeechFramesBeforeDTX*20*2
}

// GetVADActivity returns the latest available Opus-level activity estimate in
// Q8 (0-255). It reads the existing frame decision and does not run another
// detector. It reports the analyzer or CELT fallback used by Opus activity
// decisions, not the separate SILK VAD state. It returns 0 before a decision,
// after Reset, or when the current activity decision is unavailable.
func (e *Encoder) GetVADActivity() int {
	if e == nil || !e.lastOpusVADActivityObserved || !e.lastOpusVADValid {
		return 0
	}
	prob := e.lastOpusVADProb
	if !(prob > 0) {
		return 0
	}
	if prob >= 1 {
		return 255
	}
	return int(prob * 256)
}

// classifySignal compares mean-square PCM energy with its silence threshold.
// It returns 0 below the threshold and 2 otherwise.
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
