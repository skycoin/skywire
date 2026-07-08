//go:build !linux && !(js && wasm) && (!voiceaudio || (!windows && !darwin))

// Package voice pkg/skychat/voice/pulse_other.go c2-app-chat
//
// Audio stub for platforms without a native backend: everything that isn't Linux
// (pure-Go PulseAudio), isn't a Windows/macOS `voiceaudio` build (cgo malgo,
// audio_malgo.go), and isn't js/wasm (browser WebAudio, audio_wasm.go). These
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
