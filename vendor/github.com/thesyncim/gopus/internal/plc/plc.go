// Package plc implements Opus-level Packet Loss Concealment (PLC): the
// machinery that synthesizes plausible audio for frames whose packets were
// lost, dropped, or arrived too late. Concealment avoids the jarring silence
// and clicks that would otherwise occur, which is essential for real-time
// audio over unreliable transports.
//
// # Layout
//
// The package is organized around the three Opus operating modes, matching how
// libopus 1.6.1 splits concealment between its two codec layers:
//
//   - State (this file) is the mode-agnostic loss bookkeeping and fade-out
//     cadence: how many consecutive frames have been lost and the residual
//     gain to apply. It coordinates which per-mode routine runs.
//   - silk_plc.go ports the SILK speech concealment from libopus silk/PLC.c
//     (silk_PLC_conceal / silk_PLC_update) plus the fixed-point helpers from
//     silk/Inlines.h and silk/MacroCount.h that it depends on. This is the
//     bit-exact path used for SILK and the low band of Hybrid frames.
//   - celt_plc.go provides the CELT (music / fullband) concealment: band-energy
//     decay with per-band noise fill and IMDCT resynthesis, mirroring the
//     spectral-fold strategy of celt/celt_decoder.c celt_decode_lost. It also
//     serves the high band of Hybrid frames (ConcealCELTHybrid).
//
// # libopus references
//
//   - RFC 6716 Section 4.2.8 (Packet Loss Concealment)
//   - libopus silk/PLC.c, silk/PLC.h (SILK concealment + constants)
//   - libopus celt/celt_decoder.c (celt_decode_lost, the CELT loss path)
//   - libopus silk/dec_API.c (silk_Decode loss/FEC dispatch)
//
// # Type discipline
//
// State and SILKPLCState mirror the libopus C struct field widths exactly
// (opus_int32 -> int32, opus_int16 -> int16, opus_val16 -> float32), because
// the fixed-point SILK path relies on intermediate truncation and overflow
// behavior that only reproduces with matching integer widths. type_parity_test.go
// guards these widths.
//
// # Stability
//
// Most applications should use the top-level gopus decoder APIs, which drive
// this package internally with decoder-owned state. The interfaces and
// functions here are low-level implementation details and may change before the
// first release.
package plc

// Mode indicates which Opus operating mode the last good frame used, and hence
// which concealment routine to drive for the lost frame. It corresponds to the
// SILK / CELT / Hybrid split that libopus selects in src/opus_decoder.c.
type Mode int32

const (
	// ModeSILK indicates SILK-only concealment.
	// Uses LPC extrapolation and pitch prediction for speech.
	ModeSILK Mode = iota

	// ModeCELT indicates CELT-only concealment.
	// Uses energy decay with noise fill for music/general audio.
	ModeCELT

	// ModeHybrid indicates combined SILK+CELT concealment.
	// Coordinates both layers for hybrid mode frames.
	ModeHybrid
)

// MaxConcealedFrames is the consecutive-loss count past which State.IsExhausted
// reports the stream as concealed-out and callers should emit silence. Roughly
// 100ms at 20ms frames (5 frames). This is the gopus-level safety ceiling on
// top of the per-mode attenuation; libopus has no single equivalent constant
// but bounds concealment growth similarly (e.g. celt loss_duration clamping in
// celt/celt_decoder.c).
const MaxConcealedFrames = 5

// FadePerFrame is the per-loss linear gain reduction applied by
// State.RecordLoss to its mode-agnostic fadeFactor. It is deliberately mild
// because the per-mode routines (SILK silk_PLC_conceal harm/rand attenuation,
// CELT band-energy decay) already fade their own output; this factor only
// coordinates an overall envelope and must not double-attenuate hard.
const FadePerFrame float32 = 0.57

// State tracks mode-agnostic PLC bookkeeping across frames: the consecutive
// loss count, the last good frame's mode/size/channels, and the overall fade
// envelope. It is the gopus-level coordinator that decides which per-mode
// concealment routine (ConcealSILK / ConcealCELT / ConcealCELTHybrid) to drive
// and with what residual gain. The numerically exact loss state for SILK lives
// separately in SILKPLCState (the port of libopus silk_PLC_struct).
type State struct {
	// lostCount tracks consecutive lost packets.
	// Reset to 0 when a good packet is received.
	lostCount int32

	// mode indicates which concealment algorithm to use.
	// Set from the mode of the last successfully decoded packet.
	mode Mode

	// fadeFactor is the current gain multiplier (1.0 = full volume).
	// Decays toward 0 with each consecutive loss.
	fadeFactor float32

	// lastFrameSize stores the frame size from the last good packet.
	// Used to generate concealment of the same duration.
	lastFrameSize int32

	// lastChannels stores the channel count from the last good packet.
	lastChannels int32
}

// NewState creates a new PLC state with initial values.
// The state starts with full gain (fadeFactor = 1.0) and
// zero lost packet count.
func NewState() *State {
	return &State{
		lostCount:     0,
		mode:          ModeSILK, // Default; will be set by actual decoding
		fadeFactor:    1.0,
		lastFrameSize: 960, // Default 20ms at 48kHz
		lastChannels:  1,
	}
}

// Reset clears PLC state after receiving a good packet.
// This should be called whenever a packet is successfully decoded.
// It resets the lost count and restores full gain.
func (s *State) Reset() {
	s.lostCount = 0
	s.fadeFactor = 1.0
}

// RecordLoss records one lost packet and returns the updated fade factor to
// apply to the concealment audio for this frame. Call it once per lost frame
// before generating concealment.
//
// The factor decays geometrically by FadePerFrame each call and is snapped to
// exactly 0 once it drops below 0.001, so extended loss settles to silence:
//   - First loss:  fadeFactor = 1.0 * FadePerFrame
//   - Second loss: fadeFactor = FadePerFrame^2
//   - After several losses: fadeFactor == 0
func (s *State) RecordLoss() float32 {
	s.lostCount++

	// Default fade for extended loss or CELT-mode concealment.
	s.fadeFactor *= FadePerFrame

	// Clamp to minimum (effectively zero)
	if s.fadeFactor < 0.001 {
		s.fadeFactor = 0.0
	}

	return s.fadeFactor
}

// LostCount returns the number of consecutive lost packets.
// This can be used to determine if we're in extended loss condition.
func (s *State) LostCount() int {
	return int(s.lostCount)
}

// FadeFactor returns the current fade level (0.0 to 1.0), the gain to apply to
// concealment audio:
//   - 1.0: full volume, no loss recorded yet (or freshly reset)
//   - between 0 and 1: decaying after one or more consecutive losses
//   - 0.0: concealed out after several consecutive losses (silent)
func (s *State) FadeFactor() float32 {
	return s.fadeFactor
}

// Mode returns the current concealment mode.
func (s *State) Mode() Mode {
	return s.mode
}

// SetLastFrameParams stores parameters from the last good frame.
// These parameters are used to generate concealment of the correct
// duration and channel configuration.
//
// Parameters:
//   - mode: The Opus mode (SILK, CELT, or Hybrid)
//   - frameSize: Frame size in samples at 48kHz
//   - channels: Number of channels (1 or 2)
func (s *State) SetLastFrameParams(mode Mode, frameSize, channels int) {
	s.mode = mode
	s.lastFrameSize = int32(frameSize)
	s.lastChannels = int32(channels)
}

// LastFrameSize returns the frame size from the last good packet.
func (s *State) LastFrameSize() int {
	return int(s.lastFrameSize)
}

// LastChannels returns the channel count from the last good packet.
func (s *State) LastChannels() int {
	return int(s.lastChannels)
}

// IsExhausted returns true if PLC has exceeded its maximum concealment.
// After this, the output should effectively be silence.
func (s *State) IsExhausted() bool {
	return s.lostCount >= MaxConcealedFrames || s.fadeFactor <= 0.001
}
