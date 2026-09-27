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
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// registerVoiceHTTPHandlers wires the /voice endpoints onto mux. No-op when
// --pair-enable is off. All paths are exact (no trailing-slash subtree), so
// ServeMux matches them directly.
func registerVoiceHTTPHandlers(mux *http.ServeMux) {
	if !pairEnable {
		return
	}
	mux.HandleFunc("/voice/call", requireAuthFunc(voiceCallHandler()))
	mux.HandleFunc("/voice/answer", requireAuthFunc(voiceActionHandler(func(c visorapi.API, id string) error { return c.VoiceAnswer(id) })))
	mux.HandleFunc("/voice/decline", requireAuthFunc(voiceActionHandler(func(c visorapi.API, id string) error { return c.VoiceDecline(id) })))
	mux.HandleFunc("/voice/hangup", requireAuthFunc(voiceActionHandler(func(c visorapi.API, id string) error { return c.VoiceHangup(id) })))
	mux.HandleFunc("/voice/mute", requireAuthFunc(voiceMuteHandler()))
	mux.HandleFunc("/voice/active", requireAuthFunc(voiceListHandler("VoiceActive", func(c visorapi.API) ([]string, error) { return c.VoiceActive() })))
	mux.HandleFunc("/voice/incoming", requireAuthFunc(voiceListHandler("VoiceIncoming", func(c visorapi.API) ([]string, error) { return c.VoiceIncoming() })))
	mux.HandleFunc("/voice/dialing", requireAuthFunc(voiceDialingHandler()))
	mux.HandleFunc("/voice/ringback", requireAuthFunc(voiceDialRingbackHandler()))
	mux.HandleFunc("/voice/my-ringback", requireAuthFunc(voiceMyRingbackHandler()))
	mux.HandleFunc("/voice/levels", requireAuthFunc(voiceLevelsHandler()))
	mux.HandleFunc("/voice/audio", requireAuthFunc(voiceAudioHandler()))
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
		// Dial, not Call: this server's write timeout is 10s and a ring runs
		// far longer, so a blocking call could never answer the browser — every
		// outgoing call reported "Call failed" while ringing perfectly well.
		if err := pairRPCCall("VoiceDial", func(c visorapi.API) error {
			id, e := c.VoiceDial(peer)
			callID = id
			return e
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		// The call log's only chance to learn who an OUTGOING call is with:
		// VoiceActive answers with bare ids, and a call we placed never
		// appears in the ringing list that carries a peer.
		noteOutgoingCall(callID, body.Peer)
		writeJSON(w, map[string]string{"call_id": callID})
	}
}

// voiceActionHandler serves POST {call_id} → {ok:true} for answer/decline/hangup.
func voiceActionHandler(action func(c visorapi.API, callID string) error) http.HandlerFunc {
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
		if err := pairRPCCall("VoiceAction", func(c visorapi.API) error {
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
		if err := pairRPCCall("VoiceMute", func(c visorapi.API) error {
			return c.VoiceMute(body.CallID, body.Mic, body.Speaker)
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// voiceDialingHandler serves GET /voice/dialing → [{call_id, peer, state,
// reason, ringback}] for the calls this visor is PLACING and that have not been
// answered yet — how each is going (connecting, calling, ringing), and for a
// few seconds after one ended unanswered, why (offline, declined, busy,
// no_answer, failed). ringback says the callee's own tone is at
// /voice/ringback.
//
// Not part of voiceListHandler because these are not bare ids: hanging up
// needs the id and the UI needs the peer to say who is being called, and the
// ringing list's "<id> from <pk>" string was a shape to copy, not to repeat.
//
// This is what gives an outbound call a Cancel. Until the callee answers, the
// call exists only in the caller's dialing set — it is in no active list and
// no ringing list — so without this route the UI had no id, and the hang-up
// button, which works off an id, had nothing to act on.
func voiceDialingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		var calls []visorapi.VoiceDialingInfo
		if err := pairRPCCall("VoiceDialing", func(c visorapi.API) error {
			out, e := c.VoiceDialing()
			calls = out
			return e
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		if calls == nil {
			calls = []visorapi.VoiceDialingInfo{}
		}
		writeJSON(w, calls)
	}
}

// maxRingbackUpload caps a ringback tone upload. The visor enforces the real
// limit (call.MaxRingbackSize, the same megabyte); this only stops a larger
// body being read into memory to be told so.
const maxRingbackUpload = 1 << 20

// isNoRingback reports whether a proxied error is the visor saying there is no
// tone to serve — a 404, not a failure.
func isNoRingback(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no ringback tone")
}

// writeRingback serves a tone as audio and as nothing else. Its type came from
// the visor, which only ever hands out audio types — and for a peer's tone,
// from a whitelist, since another visor chose it. Checked again here all the
// same: it is served from this page's own origin, where anything but audio
// would be a page with the user's session.
func writeRingback(w http.ResponseWriter, tone visorapi.VoiceRingback) {
	mime := strings.ToLower(strings.TrimSpace(tone.Mime))
	if !strings.HasPrefix(mime, "audio/") {
		mime = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("Content-Length", strconv.Itoa(len(tone.Data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(tone.Data) //nolint:errcheck
}

// voiceDialRingbackHandler serves GET /voice/ringback?call=<id> → the ringback
// tone the peer of an outbound call plays, for this page to play while the
// call rings. 404 until it has arrived — and for a peer that plays none, when
// the page plays an ordinary ring of its own.
func voiceDialRingbackHandler() http.HandlerFunc {
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
		var tone visorapi.VoiceRingback
		err := pairRPCCall("VoiceDialRingback", func(c visorapi.API) error {
			t, e := c.VoiceDialRingback(callID)
			tone = t
			return e
		})
		if isNoRingback(err) || (err == nil && len(tone.Data) == 0) {
			http.Error(w, "no ringback tone for that call", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		writeRingback(w, tone)
	}
}

// voiceMyRingbackHandler serves /voice/my-ringback — the tone people hear
// while THIS visor rings:
//
//	GET    → the tone as audio (to preview it), 404 when none is set;
//	         with ?info=1, {set, size, type} instead of the audio
//	PUT    → set it: the body is the audio, Content-Type its type
//	DELETE → clear it; callers hear an ordinary ring again
//
// The visor keeps it (and across restarts): it is the visor that rings, and
// that answers the caller asking for the tone.
func voiceMyRingbackHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			var tone visorapi.VoiceRingback
			if err := pairRPCCall("VoiceRingback", func(c visorapi.API) error {
				t, e := c.VoiceRingback()
				tone = t
				return e
			}); err != nil {
				http.Error(w, err.Error(), voiceErrStatus(err))
				return
			}
			if r.URL.Query().Get("info") != "" {
				writeJSON(w, map[string]any{"set": len(tone.Data) > 0, "size": len(tone.Data), "type": tone.Mime})
				return
			}
			if len(tone.Data) == 0 {
				http.Error(w, "no ringback tone set", http.StatusNotFound)
				return
			}
			writeRingback(w, tone)
		case http.MethodPut:
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRingbackUpload))
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					http.Error(w, "ringback tone too large (the limit is 1 MB)", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if len(data) == 0 {
				http.Error(w, "empty body — DELETE clears the tone", http.StatusBadRequest)
				return
			}
			tone := visorapi.VoiceRingback{Data: data, Mime: r.Header.Get("Content-Type")}
			if err := pairRPCCall("VoiceSetRingback", func(c visorapi.API) error {
				return c.VoiceSetRingback(tone)
			}); err != nil {
				status := voiceErrStatus(err)
				if strings.Contains(err.Error(), "audio format") || strings.Contains(err.Error(), "the limit is") {
					status = http.StatusBadRequest
				}
				http.Error(w, err.Error(), status)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "size": len(data)})
		case http.MethodDelete:
			if err := pairRPCCall("VoiceSetRingback", func(c visorapi.API) error {
				return c.VoiceSetRingback(visorapi.VoiceRingback{})
			}); err != nil {
				http.Error(w, err.Error(), voiceErrStatus(err))
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		default:
			http.Error(w, "GET, PUT or DELETE", http.StatusMethodNotAllowed)
		}
	}
}

// voiceListHandler serves GET → a JSON array of strings for active/incoming.
// VoiceIncoming entries are formatted "<call-id> from <peer-pk>" by the visor.
func voiceListHandler(op string, list func(c visorapi.API) ([]string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if voiceRPCDown(w) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		var ids []string
		if err := pairRPCCall(op, func(c visorapi.API) error {
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
		if err := pairRPCCall("VoiceCallAudio", func(c visorapi.API) error {
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

// voiceAudioHandler serves GET /voice/audio?call=<id> → {sent:[], recv:[]} the
// recent sent/received PCM (int16) of a call, for the live spectrogram. Heavier
// than /voice/levels (a full ~1s ring), so the UI only polls it while the
// spectrogram view is open.
func voiceAudioHandler() http.HandlerFunc {
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
		if err := pairRPCCall("VoiceCallAudio", func(c visorapi.API) error {
			s, rc, e := c.VoiceCallAudio(callID)
			sent, recv = s, rc
			return e
		}); err != nil {
			http.Error(w, err.Error(), voiceErrStatus(err))
			return
		}
		if sent == nil {
			sent = []int16{}
		}
		if recv == nil {
			recv = []int16{}
		}
		writeJSON(w, map[string][]int16{"sent": sent, "recv": recv})
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
