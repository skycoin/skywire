// Package visorapi pkg/visor/visorapi/voice.go c3-vis-core
package visorapi

// VoiceDialingInfo is one call this visor is placing, before it is answered.
type VoiceDialingInfo struct {
	CallID string `json:"call_id"`
	Peer   string `json:"peer"`
	// State is how far the call has got: "connecting", "calling" (the peer
	// has the invite but predates ringing reports) or "ringing" — or, for a
	// few seconds after it ended unanswered, why: "offline", "declined",
	// "busy", "no_answer" or "failed". Empty from a visor that predates it.
	State string `json:"state,omitempty"`
	// Reason is the detail behind an outcome.
	Reason string `json:"reason,omitempty"`
	// Ringback is true once the peer's own ringback tone has arrived and can
	// be played (VoiceDialRingback); false means play an ordinary ring.
	Ringback bool `json:"ringback,omitempty"`
}

// VoiceRingback is a ringback tone: what a caller hears while a visor rings.
// Empty Data means none.
type VoiceRingback struct {
	Data []byte `json:"data,omitempty"`
	Mime string `json:"mime,omitempty"`
}

// VoiceMuteReq toggles the mic/speaker mute of a call.
type VoiceMuteReq struct {
	CallID  string
	Mic     bool
	Speaker bool
}

// VoiceAudioSnapshot is the recent sent/received PCM of an active call.
type VoiceAudioSnapshot struct {
	Sent []int16
	Recv []int16
}
