// Package visorapi pkg/visor/visorapi/voice.go c3-vis-core
package visorapi

// VoiceDialingInfo is one call this visor is placing, before it is answered.
type VoiceDialingInfo struct {
	CallID string `json:"call_id"`
	Peer   string `json:"peer"`
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
