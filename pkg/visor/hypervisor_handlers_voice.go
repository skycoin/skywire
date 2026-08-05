// Package visor pkg/visor/hypervisor_handlers_voice.go c3-vis-core
//
// Hypervisor HTTP routes for skychat VOICE calls — the native-visor
// counterpart to the wasm visor's skychatVoice* JS hooks. These thin
// handlers bridge the hvui skychat panel to the local visor's voice
// surface (ctx.API.Voice*), so the SAME Angular UI drives voice on a
// native visor (over HTTP here) and on a wasm visor (over the in-process
// JS hooks) with no divergence — mirroring the group + proxy handlers.
//
// Scope: the LOCAL visor only (hv.visorCtx), matching the skychat
// password + proxy + group handlers. Voice is disabled (ctx.API returns
// ErrVoiceDisabled) unless the visor was built/started with voice; that
// surfaces here as 503 so the UI can hide the controls.
package visor

import (
	"math"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

// writeVoiceErr maps a voice error to an HTTP status: ErrVoiceDisabled →
// 503 (feature off) so the UI can degrade, everything else → 400.
func (hv *Hypervisor) writeVoiceErr(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	if err == ErrVoiceDisabled {
		status = http.StatusServiceUnavailable
	}
	httputil.WriteJSON(w, r, status, map[string]string{"error": err.Error()})
}

// getVoiceActive → GET /skychat/voice/active : ids of active calls.
func (hv *Hypervisor) getVoiceActive() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ids, err := ctx.API.VoiceActive()
		if err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		if ids == nil {
			ids = []string{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, ids)
	})
}

// getVoiceIncoming → GET /skychat/voice/incoming : ringing inbound calls
// awaiting an answer (only populated in explicit-answer mode).
func (hv *Hypervisor) getVoiceIncoming() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ids, err := ctx.API.VoiceIncoming()
		if err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		if ids == nil {
			ids = []string{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, ids)
	})
}

// postVoiceCall → POST /skychat/voice/call {peer} : place a 1:1 call,
// reply {call_id}. Blocks until the callee answers (or the dial fails).
func (hv *Hypervisor) postVoiceCall() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			Peer string `json:"peer"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(rb.Peer); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad peer pk: " + err.Error()})
			return
		}
		id, err := ctx.API.VoiceCall(pk)
		if err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]string{"call_id": id})
	})
}

// voiceCallIDAction wraps the answer/decline/hangup handlers: read a
// {call_id} body and invoke the matching visor method.
func (hv *Hypervisor) voiceCallIDAction(action func(ctx *httpCtx, callID string) error) http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			CallID string `json:"call_id"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if rb.CallID == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "call_id required"})
			return
		}
		if err := action(ctx, rb.CallID); err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
	})
}

// postVoiceAnswer → POST /skychat/voice/answer {call_id} : accept a ringing call.
func (hv *Hypervisor) postVoiceAnswer() http.HandlerFunc {
	return hv.voiceCallIDAction(func(ctx *httpCtx, callID string) error { return ctx.API.VoiceAnswer(callID) })
}

// postVoiceDecline → POST /skychat/voice/decline {call_id} : reject a ringing call.
func (hv *Hypervisor) postVoiceDecline() http.HandlerFunc {
	return hv.voiceCallIDAction(func(ctx *httpCtx, callID string) error { return ctx.API.VoiceDecline(callID) })
}

// postVoiceHangup → POST /skychat/voice/hangup {call_id} : end an active call.
func (hv *Hypervisor) postVoiceHangup() http.HandlerFunc {
	return hv.voiceCallIDAction(func(ctx *httpCtx, callID string) error { return ctx.API.VoiceHangup(callID) })
}

// getVoiceLevels → GET /skychat/voice/levels?call=<id> : current send/receive
// audio RMS levels (0..1) of a call, for the live level meter. Tiny payload
// (two floats) — the UI polls it fast and keeps its own scrolling history.
func (hv *Hypervisor) getVoiceLevels() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		callID := r.URL.Query().Get("call")
		if callID == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "call required"})
			return
		}
		sent, recv, err := ctx.API.VoiceCallAudio(callID)
		if err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]float64{
			"sent": rmsLevel(sent),
			"recv": rmsLevel(recv),
		})
	})
}

// getVoiceDialing → GET /skychat/voice/dialing : calls this visor is placing
// and that have not been answered yet.
//
// The caller's missing half of the picture: a call being dialed is in neither
// the ringing list (that is the callee's) nor the active list (that starts at
// "answered"), so without this a UI has nothing to show for the whole ring.
// Local visor only — see (*Visor).VoiceDialing.
func (hv *Hypervisor) getVoiceDialing() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if ctx.isRemote || hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusOK, []VoiceDialingInfo{})
			return
		}
		out := hv.visor.VoiceDialing()
		if out == nil {
			out = []VoiceDialingInfo{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, out)
	})
}

// getVoiceAudio → GET /skychat/voice/audio?call=<id> : recent sent/received PCM
// (int16) of a call, for the spectrogram. Heavier — the UI only polls it while
// the spectrogram view is open.
func (hv *Hypervisor) getVoiceAudio() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		callID := r.URL.Query().Get("call")
		if callID == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "call required"})
			return
		}
		sent, recv, err := ctx.API.VoiceCallAudio(callID)
		if err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		if sent == nil {
			sent = []int16{}
		}
		if recv == nil {
			recv = []int16{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string][]int16{"sent": sent, "recv": recv})
	})
}

// rmsLevel returns the RMS amplitude of the tail of pcm, normalized to 0..1.
func rmsLevel(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	// Last ~0.1s (4800 samples @ 48k) is "now".
	const window = 4800
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

// postVoiceMute → POST /skychat/voice/mute {call_id, mic, speaker} : toggle the
// mic (what the peer hears from us) and speaker (what we hear) mute of a call.
func (hv *Hypervisor) postVoiceMute() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			CallID  string `json:"call_id"`
			Mic     bool   `json:"mic"`
			Speaker bool   `json:"speaker"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if rb.CallID == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "call_id required"})
			return
		}
		if err := ctx.API.VoiceMute(rb.CallID, rb.Mic, rb.Speaker); err != nil {
			hv.writeVoiceErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
	})
}
