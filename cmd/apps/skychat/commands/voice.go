// Package commands cmd/apps/skychat/commands/voice.go c4-app-chat
//
// Browser-facing HTTP proxy for skychat 1:1 VOICE CALLS. Like the group
// endpoints, these relay to the visor's net/rpc surface (visor.API Voice*, via
// the existing pairRPCCall seam) — the SAME surface the hypervisor UI and
// `skywire-cli skychat voice` drive. The call manager, the media session, and
// the microphone/speaker all live in the VISOR (pkg/skychat/call, brought up by
// init_voice.go with host audio + explicit-answer); this app is only a control
// surface + level meter, so there is no audio code here.
//
// Model: audio is HOST audio on the machine the visor runs on. The realistic
// deployment is a desktop where the visor and this browser UI share the same
// machine, so "host audio" is the user's own mic/speakers. Calls RING and are
// answered explicitly — a visor never streams its mic without an Answer.
//
// Gated behind --pair-enable (needs the visor RPC); every endpoint 503s when
// the RPC is down or voice is disabled (no dmsg / built without audio), so the
// UI can hide the call controls.
package commands

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/visor"
)

// registerVoiceHTTPHandlers wires the /voice endpoints onto mux. No-op when
// --pair-enable is off. All paths are exact (no trailing-slash subtree), so
// ServeMux matches them directly.
func registerVoiceHTTPHandlers(mux *http.ServeMux) {
	if !pairEnable {
		return
	}
	mux.HandleFunc("/voice/call", requireAuthFunc(voiceCallHandler()))
	mux.HandleFunc("/voice/answer", requireAuthFunc(voiceActionHandler(func(c visor.API, id string) error { return c.VoiceAnswer(id) })))
	mux.HandleFunc("/voice/decline", requireAuthFunc(voiceActionHandler(func(c visor.API, id string) error { return c.VoiceDecline(id) })))
	mux.HandleFunc("/voice/hangup", requireAuthFunc(voiceActionHandler(func(c visor.API, id string) error { return c.VoiceHangup(id) })))
	mux.HandleFunc("/voice/mute", requireAuthFunc(voiceMuteHandler()))
	mux.HandleFunc("/voice/active", requireAuthFunc(voiceListHandler("VoiceActive", func(c visor.API) ([]string, error) { return c.VoiceActive() })))
	mux.HandleFunc("/voice/incoming", requireAuthFunc(voiceListHandler("VoiceIncoming", func(c visor.API) ([]string, error) { return c.VoiceIncoming() })))
	mux.HandleFunc("/voice/levels", requireAuthFunc(voiceLevelsHandler()))
}

// voiceRPCDown reports whether the visor RPC is unavailable and, if so, writes a
// 503 so the UI hides the call controls.
func voiceRPCDown(w http.ResponseWriter) bool {
	if !pairRPCAlive() {
		http.Error(w, "voice disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
		return true
	}
	return false
}

// voiceErrStatus maps a proxied voice error to a status: a disabled voice
// manager (no dmsg / no audio) → 503 so the UI degrades; anything else → 502.
func voiceErrStatus(err error) int {
	if err != nil && strings.Contains(err.Error(), "disabled") {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

// voiceCallHandler serves POST /voice/call {peer} → {call_id}. Blocks (via the
// visor) until the callee answers or the dial fails.
func voiceCallHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Peer string `json:"peer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		peer, err := parsePK(body.Peer)
		if err != nil {
			http.Error(w, "invalid peer pk: "+err.Error(), http.StatusBadRequest)
			return
		}
		var callID string
		if err := pairRPCCall("VoiceCall", func(c visor.API) error {
			id, e := c.VoiceCall(peer)
			callID = id
			return e
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		writeJSON(w, map[string]string{"call_id": callID})
	}
}

// voiceActionHandler serves POST {call_id} → {ok:true} for answer/decline/hangup.
func voiceActionHandler(action func(c visor.API, callID string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			CallID string `json:"call_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.CallID) == "" {
			http.Error(w, "call_id required", http.StatusBadRequest)
			return
		}
		if err := pairRPCCall("VoiceAction", func(c visor.API) error {
			return action(c, body.CallID)
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// voiceMuteHandler serves POST /voice/mute {call_id, mic, speaker}.
func voiceMuteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			CallID  string `json:"call_id"`
			Mic     bool   `json:"mic"`
			Speaker bool   `json:"speaker"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.CallID) == "" {
			http.Error(w, "call_id required", http.StatusBadRequest)
			return
		}
		if err := pairRPCCall("VoiceMute", func(c visor.API) error {
			return c.VoiceMute(body.CallID, body.Mic, body.Speaker)
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// voiceListHandler serves GET → a JSON array of strings for active/incoming.
// VoiceIncoming entries are formatted "<call-id> from <peer-pk>" by the visor.
func voiceListHandler(op string, list func(c visor.API) ([]string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		var ids []string
		if err := pairRPCCall(op, func(c visor.API) error {
			out, e := list(c)
			ids = out
			return e
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		if ids == nil {
			ids = []string{}
		}
		writeJSON(w, ids)
	}
}

// voiceLevelsHandler serves GET /voice/levels?call=<id> → {sent, recv} RMS
// levels (0..1) for the live meter, computed from the call's recent PCM.
func voiceLevelsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		callID := strings.TrimSpace(r.URL.Query().Get("call"))
		if callID == "" {
			http.Error(w, "call required", http.StatusBadRequest)
			return
		}
		var sent, recv []int16
		if err := pairRPCCall("VoiceCallAudio", func(c visor.API) error {
			s, rc, e := c.VoiceCallAudio(callID)
			sent, recv = s, rc
			return e
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		writeJSON(w, map[string]float64{"sent": voiceRMS(sent), "recv": voiceRMS(recv)})
	}
}

// voiceRMS returns the RMS amplitude of the tail of pcm, normalized to 0..1 —
// the same computation the hypervisor voice handler uses for its meter.
func voiceRMS(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	const window = 4800 // last ~0.1s @ 48 kHz
	if len(pcm) > window {
		pcm = pcm[len(pcm)-window:]
	}
	var sum float64
	for _, s := range pcm {
		f := float64(s) / 32768.0
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(pcm)))
}
