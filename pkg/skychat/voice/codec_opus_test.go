//go:build opus

// Package voice pkg/skychat/voice/codec_opus_test.go c2-app-chat
//
// Run with: go test -tags opus ./pkg/skychat/voice/  (needs libopus).
package voice

import (
	"math"
	"testing"
)

// TestOpusRoundTrip encodes a tone frame and decodes it, checking the packet is
// much smaller than PCM and the decoded frame recovers the signal's energy
// (Opus is lossy, so we don't expect sample-exact recovery).
func TestOpusRoundTrip(t *testing.T) {
	c, err := NewOpusCodec()
	if err != nil {
		t.Fatalf("NewOpusCodec: %v", err)
	}
	// One 20 ms frame of a 440 Hz tone at 48 kHz.
	pcm := make([]int16, frameSamples)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}

	pkt, err := c.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(pkt) == 0 || len(pkt) >= frameSamples*2 {
		t.Fatalf("opus packet size %d not a compression win over %d PCM bytes", len(pkt), frameSamples*2)
	}

	out, err := c.Decode(pkt)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != frameSamples {
		t.Fatalf("decoded %d samples, want %d", len(out), frameSamples)
	}
	var energy float64
	for _, s := range out {
		energy += float64(s) * float64(s)
	}
	if energy == 0 {
		t.Fatal("decoded frame is silent — codec lost the signal")
	}
	t.Logf("opus: %d PCM bytes -> %d packet bytes (%.0f%% smaller)",
		frameSamples*2, len(pkt), 100*(1-float64(len(pkt))/float64(frameSamples*2)))
}
