// Package plc implements the codec-specific concealment used after a lost Opus
// frame. The SILK path predicts speech from pitch and LPC history; the CELT
// path decays band energies, fills uncoded spectrum, and runs CELT synthesis.
// Hybrid decoding uses the SILK path for the low band and CELT for the high
// band, as described in RFC 6716 Section 4.2.8 and libopus's `silk/PLC.c` and
// `celt/celt_decoder.c`.
//
// [State] stores the last frame's mode, size, channel count, and the package's
// loss counter and fade envelope. The codec-specific signal history remains in
// the owning SILK and CELT decoder states; keep those states with one stream and
// serialize access. Most callers should use the top-level gopus decoder, which
// supplies the required history.
//
// The fixed-point SILK path preserves the reference Q-format widths and
// arithmetic.
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

// MaxConcealedFrames is the consecutive-loss count at which State.IsExhausted
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

// IsExhausted returns true when the consecutive-loss ceiling is reached or the
// fade factor falls to 0.001 or lower. Callers can use it to replace further
// predicted output with silence.
func (s *State) IsExhausted() bool {
	return s.lostCount >= MaxConcealedFrames || s.fadeFactor <= 0.001
}
