//go:build linux

// Package voice pkg/skychat/voice/pulse_linux.go c4-app-chat
//
// PulseAudio/PipeWire capture + playback for skychat voice, via
// github.com/jfreymuth/pulse — a PURE-GO PulseAudio client (no cgo, no system
// libs to link; it just needs a running PulseAudio/PipeWire at runtime). This is
// the Linux audio backend; a cgo malgo backend for Windows/macOS is a follow-up.
//
// NewMicSource(monitor=false) captures the default microphone. NewMicSource(
// monitor=true) captures the default sink's MONITOR — i.e. whatever audio the
// system is playing — so a call (or the spectrogram demo) can carry internal
// audio with no mic plugged in, the way `pactl`/PulseAudio monitor sources work.
package voice

import (
	"fmt"
	"io"

	"github.com/jfreymuth/pulse"
)

// The sampleRing shared with the malgo backend lives in audio_ring.go.

// pulseSource is a voice.Source backed by a PulseAudio record stream.
type pulseSource struct {
	client *pulse.Client
	stream *pulse.RecordStream
	ring   *sampleRing
}

// NewMicSource opens a capture Source at the package sample rate (mono). When
// monitor is true it records the default sink's monitor (system output) instead
// of the microphone. The returned Source also implements io.Closer.
func NewMicSource(monitor bool, rate int) (Source, error) {
	if rate <= 0 {
		rate = sampleRate
	}
	c, err := pulse.NewClient()
	if err != nil {
		return nil, fmt.Errorf("voice: pulse connect: %w", err)
	}
	ring := newSampleRing(rate) // ~1s cap
	opts := []pulse.RecordOption{pulse.RecordSampleRate(rate), pulse.RecordLatency(0.05)}
	if monitor {
		sink, serr := c.DefaultSink()
		if serr != nil {
			c.Close()
			return nil, fmt.Errorf("voice: default sink for monitor capture: %w", serr)
		}
		opts = append(opts, pulse.RecordMonitor(sink))
	}
	st, err := c.NewRecord(pulse.Int16Writer(func(s []int16) (int, error) {
		ring.push(s)
		return len(s), nil
	}), opts...)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("voice: open record stream: %w", err)
	}
	st.Start()
	return &pulseSource{client: c, stream: st, ring: ring}, nil
}

// Read implements Source (pulls the next PCM frame; silence when quiet). Never
// blocks — silence-pads on underrun so the ticker-paced send loop always emits a
// keepalive frame and a quiet side never starves the peer into the dmsg
// idle-read timeout (which was dropping calls).
func (p *pulseSource) Read(pcm []int16) (int, error) { return p.ring.popSilence(pcm), nil }

// Close stops and releases the capture stream.
func (p *pulseSource) Close() error {
	p.ring.close() // unblock any waiting Read
	if p.stream != nil {
		p.stream.Stop()
		p.stream.Close()
	}
	if p.client != nil {
		p.client.Close()
	}
	return nil
}

// pulseSink is a voice.Sink backed by a PulseAudio playback stream.
type pulseSink struct {
	client *pulse.Client
	stream *pulse.PlaybackStream
	ring   *sampleRing
}

// NewSpeakerSink opens a playback Sink at the package sample rate (mono). The
// returned Sink also implements io.Closer.
func NewSpeakerSink(rate int) (Sink, error) {
	if rate <= 0 {
		rate = sampleRate
	}
	c, err := pulse.NewClient()
	if err != nil {
		return nil, fmt.Errorf("voice: pulse connect: %w", err)
	}
	ring := newSampleRing(rate)
	st, err := c.NewPlayback(pulse.Int16Reader(func(s []int16) (int, error) {
		ring.popSilence(s)
		return len(s), nil
	}), pulse.PlaybackSampleRate(sampleRate), pulse.PlaybackLatency(0.05))
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("voice: open playback stream: %w", err)
	}
	st.Start()
	return &pulseSink{client: c, stream: st, ring: ring}, nil
}

// Write implements Sink (queues a PCM frame for playback).
func (p *pulseSink) Write(pcm []int16) (int, error) {
	p.ring.push(pcm)
	return len(pcm), nil
}

// Close drains and releases the playback stream.
func (p *pulseSink) Close() error {
	if p.stream != nil {
		p.stream.Drain()
		p.stream.Stop()
		p.stream.Close()
	}
	if p.client != nil {
		p.client.Close()
	}
	return nil
}

// compile-time interface checks.
var (
	_ Source    = (*pulseSource)(nil)
	_ Sink      = (*pulseSink)(nil)
	_ io.Closer = (*pulseSource)(nil)
	_ io.Closer = (*pulseSink)(nil)
)
