// Package cliskychat cmd/skywire-cli/commands/skychat/voice_spectrogram_test.go c4-vis-cli
package cliskychat

import (
	"math"
	"testing"

	"github.com/skycoin/skywire/pkg/skychat/voice/spectrogram"
)

// TestSpectrogramColumnFreqPosition verifies the column mapping matches
// audioprism-go's tcell model: the vertical axis is 0..12 kHz linear (bin =
// freq*fftSize/SampleRate), so a pure tone lights up the row at
// freq/12000 * bufferHeight. A 3 kHz tone (at the 24 kHz spectrogram rate) must
// peak ~1/4 of the way up the fixed buffer.
func TestSpectrogramColumnFreqPosition(t *testing.T) {
	v := newSpecView()
	const (
		rate = spectrogram.SampleRate // 24000
		freq = 3000.0
	)
	samp := make([]float32, 4096)
	for i := range samp {
		samp[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / rate))
	}
	v.push(samp)
	v.drainInto()

	v.mu.RLock()
	col := v.history[(v.head-1+specBufferWidth)%specBufferWidth]
	v.mu.RUnlock()

	peak, best := 0, -1.0
	for y, c := range col {
		r, g, b, _ := c.RGBA()
		if lum := float64(r) + float64(g) + float64(b); lum > best {
			best, peak = lum, y
		}
	}
	want := int(freq / specMaxFreqHz * specBufferHeight) // 3000/12000 * 1024 = 256
	if d := peak - want; d < -specBufferHeight/20 || d > specBufferHeight/20 {
		t.Fatalf("brightest row %d, want ~%d (freq→row mapping doesn't match audioprism)", peak, want)
	}
}
