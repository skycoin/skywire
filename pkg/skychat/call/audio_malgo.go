//go:build voiceaudio && (windows || darwin)

// Package call pkg/skychat/call/audio_malgo.go c4-app-chat
//
// Windows/macOS audio capture + playback for skychat voice, via
// github.com/gen2brain/malgo (a cgo binding for the single-header miniaudio — no
// external system audio library to install, but it needs a C toolchain at build
// time). Built behind the `voiceaudio` tag so a default Windows/macOS build is
// completely unaffected (it falls back to the silent stub, pulse_other.go); a
// build with `-tags voiceaudio` gets real audio. The Linux backend is the pure-Go
// pulse_linux.go.
//
// NOTE: monitor (system-output/loopback) capture is not implemented on this
// backend yet — NewMicSource always captures the default input device. This
// backend is best-effort and has not been run on Windows/macOS from this Linux
// dev host; treat it as ready-for-testing.
package call

import (
	"encoding/binary"
	"fmt"
	"io"

	malgo "github.com/gen2brain/malgo"
)

// malgoSource is a voice.Source backed by a miniaudio capture device.
type malgoSource struct {
	ctx  *malgo.AllocatedContext
	dev  *malgo.Device
	ring *sampleRing
}

// NewMicSource opens a capture Source at the given sample rate (mono, S16). The
// monitor argument is accepted for API parity but ignored — malgo captures the
// default input device. The returned Source also implements io.Closer.
func NewMicSource(_ bool, rate int) (Source, error) {
	if rate <= 0 {
		rate = sampleRate
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("voice: malgo context: %w", err)
	}
	ring := newSampleRing(rate)
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.SampleRate = uint32(rate) //nolint:gosec // rate is a small positive audio rate

	onData := func(_, in []byte, framecount uint32) {
		n := int(framecount)
		if n*2 > len(in) {
			n = len(in) / 2
		}
		pcm := make([]int16, n)
		for i := 0; i < n; i++ {
			pcm[i] = int16(binary.LittleEndian.Uint16(in[i*2:])) //nolint:gosec // reinterpreting S16 bytes
		}
		ring.push(pcm)
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: onData})
	if err != nil {
		_ = ctx.Uninit() //nolint:errcheck
		ctx.Free()
		return nil, fmt.Errorf("voice: malgo capture device: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit() //nolint:errcheck
		ctx.Free()
		return nil, fmt.Errorf("voice: start capture: %w", err)
	}
	return &malgoSource{ctx: ctx, dev: dev, ring: ring}, nil
}

// Read never blocks: captured audio when available, silence-padded on underrun
// (keepalive for the ticker-paced send loop — see sampleRing).
func (s *malgoSource) Read(pcm []int16) (int, error) { return s.ring.popSilence(pcm), nil }

func (s *malgoSource) Close() error {
	s.ring.close()
	if s.dev != nil {
		s.dev.Uninit()
	}
	if s.ctx != nil {
		_ = s.ctx.Uninit() //nolint:errcheck
		s.ctx.Free()
	}
	return nil
}

// malgoSink is a voice.Sink backed by a miniaudio playback device.
type malgoSink struct {
	ctx  *malgo.AllocatedContext
	dev  *malgo.Device
	ring *sampleRing
}

// NewSpeakerSink opens a playback Sink at the given sample rate (mono, S16). The
// returned Sink also implements io.Closer.
func NewSpeakerSink(rate int) (Sink, error) {
	if rate <= 0 {
		rate = sampleRate
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("voice: malgo context: %w", err)
	}
	ring := newSampleRing(rate)
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1
	cfg.SampleRate = uint32(rate) //nolint:gosec // rate is a small positive audio rate

	onData := func(out, _ []byte, framecount uint32) {
		n := int(framecount)
		if n*2 > len(out) {
			n = len(out) / 2
		}
		pcm := make([]int16, n)
		ring.popSilence(pcm)
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint16(out[i*2:], uint16(pcm[i])) //nolint:gosec // packing S16 bytes
		}
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: onData})
	if err != nil {
		_ = ctx.Uninit() //nolint:errcheck
		ctx.Free()
		return nil, fmt.Errorf("voice: malgo playback device: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit() //nolint:errcheck
		ctx.Free()
		return nil, fmt.Errorf("voice: start playback: %w", err)
	}
	return &malgoSink{ctx: ctx, dev: dev, ring: ring}, nil
}

func (s *malgoSink) Write(pcm []int16) (int, error) {
	s.ring.push(pcm)
	return len(pcm), nil
}

func (s *malgoSink) Close() error {
	if s.dev != nil {
		s.dev.Uninit()
	}
	if s.ctx != nil {
		_ = s.ctx.Uninit() //nolint:errcheck
		s.ctx.Free()
	}
	return nil
}

// compile-time interface checks.
var (
	_ Source    = (*malgoSource)(nil)
	_ Sink      = (*malgoSink)(nil)
	_ io.Closer = (*malgoSource)(nil)
	_ io.Closer = (*malgoSink)(nil)
)
