// Package ui2 pkg/visor/ui2/voice.go c3-vis-core
package ui2

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Voice is the call strip of the chat tab, as the voice controls of
// skychat.component.ts. The audio itself is the visor's: a native visor's
// own device, or the desk's microphone and speaker for the in-tab visor.
type Voice struct {
	PK      string
	Peer    string // the open conversation, the callee of the call button
	CanCall bool   // direct message mode
	Off     bool   // the visor has no voice
	Err     string

	Incoming []VoiceRing
	Dialing  []visorapi.VoiceDialingInfo

	// The first active call and what the page keeps of it between polls.
	Call  string
	Since int64 // unix seconds, hypervisor clock, when the page first saw Call
	Secs  int64 // seconds since then
	Mic   bool  // our microphone is muted
	Spk   bool  // the caller is muted
}

// VoiceRing is a ringing inbound call.
type VoiceRing struct {
	ID   string
	From string
}

// ParseVoiceRing reads one entry of VoiceIncoming, "<id> from <pk>".
func ParseVoiceRing(s string) VoiceRing {
	id, from, ok := strings.Cut(strings.TrimSpace(s), " from ")
	if !ok {
		return VoiceRing{ID: strings.TrimSpace(s)}
	}
	return VoiceRing{ID: strings.TrimSpace(id), From: strings.TrimSpace(from)}
}

// vals is the strip's state for a request, with over applied.
func (v Voice) vals(over map[string]string) map[string]string {
	m := map[string]string{"peer": v.Peer, "call": v.Call, "mic": b01(v.Mic), "spk": b01(v.Spk), "since": strconv.FormatInt(v.Since, 10)}
	if !v.CanCall {
		m["mode"] = "groups"
	}
	for k, x := range over {
		m[k] = x
	}
	return m
}

func (v Voice) pollURL() string {
	q := url.Values{}
	for k, x := range v.vals(nil) {
		q.Set(k, x)
	}
	return v.url("?" + q.Encode())
}

func (v Voice) url(rest string) string { return VisorURL(v.PK, "chat/voice") + rest }

// levelsURL is the JSON route the meter polls while a call is up.
func (v Voice) levelsURL() string {
	return "api/visors/" + v.PK + "/skychat/voice/levels?call=" + url.QueryEscape(v.Call)
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func pressed(b bool) string { return strconv.FormatBool(b) }

// muteTip is the Angular tooltip of a mute button, which is "mic" or "caller".
func muteTip(ctx context.Context, muted bool, which string) string {
	if muted {
		return T(ctx, "skychat.voice.unmute-"+which+"-tip")
	}
	return T(ctx, "skychat.voice.mute-"+which+"-tip")
}

// dialState is how far a call being placed has got.
func dialState(d visorapi.VoiceDialingInfo) string {
	s := strings.ReplaceAll(d.State, "_", " ")
	if s == "" {
		s = "calling"
	}
	if d.Reason != "" {
		s += ": " + d.Reason
	}
	return s
}

// voiceClock is mm:ss.
func voiceClock(secs int64) string {
	s := strconv.FormatInt(secs%60, 10)
	if len(s) < 2 {
		s = "0" + s
	}
	return strconv.FormatInt(secs/60, 10) + ":" + s
}
