//go:build !linux

// Package voice pkg/skychat/voice/pulse_other.go c2-app-chat
//
// Non-Linux stub for the PulseAudio backend (pulse_linux.go). Real audio
// capture/playback on Windows/macOS is a cgo malgo follow-up; until then these
// return an error so callers degrade gracefully to silent audio.
package voice

import "errors"

// ErrAudioUnsupported is returned by the audio-device constructors on platforms
// without a native backend yet.
var ErrAudioUnsupported = errors.New("voice: live audio capture/playback not supported on this platform yet (Linux/PulseAudio only)")

// NewMicSource is unavailable off Linux.
func NewMicSource(_ bool, _ int) (Source, error) { return nil, ErrAudioUnsupported }

// NewSpeakerSink is unavailable off Linux.
func NewSpeakerSink(_ int) (Sink, error) { return nil, ErrAudioUnsupported }
