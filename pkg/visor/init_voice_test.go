// Package visor pkg/visor/init_voice_test.go
package visor

import "testing"

// TestResolveVoiceAudioMode: a phone lends its audio device to the visor
// unless SKYWIRE_VOICE_AUDIO says otherwise, and nothing else does.
func TestResolveVoiceAudioMode(t *testing.T) {
	cases := []struct {
		goos, env, want string
	}{
		{"android", "", voiceAudioBridge},
		{"ios", "", voiceAudioBridge},
		{"ios", "  ", voiceAudioBridge},
		{"ios", "off", "off"},
		{"android", "Monitor", "monitor"},
		{"linux", "", ""},
		{"darwin", "", ""},
		{"windows", "bridge", voiceAudioBridge},
	}
	for _, c := range cases {
		if got := resolveVoiceAudioMode(c.goos, c.env); got != c.want {
			t.Errorf("resolveVoiceAudioMode(%q, %q) = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}
