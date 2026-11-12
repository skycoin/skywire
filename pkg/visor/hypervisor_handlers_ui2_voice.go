// Package visor pkg/visor/hypervisor_handlers_ui2_voice.go c3-vis-core
package visor

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The chat tab's voice calls, through the same ctx.API calls as the
// /skychat/voice routes the Angular tab uses. Local visor only, like them.

func (hv *Hypervisor) ui2VoiceRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/chat/voice", hv.ui2Voice(""))
	for _, op := range []string{"call", "answer", "decline", "hangup", "mute"} {
		r.Post("/ui2/visors/{pk}/chat/voice/"+op, hv.ui2Voice(op))
	}
}

// ui2VoiceFrom reads the strip's state carried by a request.
func ui2VoiceFrom(r *http.Request) ui2.Voice {
	v := ui2.Voice{
		PK:      r.PathValue("pk"),
		Peer:    r.FormValue("peer"),
		CanCall: r.FormValue("mode") != "groups",
		Call:    r.FormValue("call"),
		Mic:     r.FormValue("mic") == "1",
		Spk:     r.FormValue("spk") == "1",
	}
	v.Since, _ = strconv.ParseInt(r.FormValue("since"), 10, 64) //nolint:errcheck
	var pk cipher.PubKey
	if v.Peer != "" && pk.Set(v.Peer) != nil {
		v.Peer = ""
	}
	return v
}

// ui2VoiceState fills the calls. A new active call starts the timer and
// clears the mute buttons, as the Angular tab does.
func ui2VoiceState(api visorapi.API, v ui2.Voice) ui2.Voice {
	active, err := api.VoiceActive()
	if err != nil {
		if isVoiceOff(err) {
			v.Off = true
		} else {
			v.Err = err.Error()
		}
		return v
	}
	now := time.Now().Unix()
	switch {
	case len(active) == 0:
		v.Call, v.Since, v.Mic, v.Spk = "", 0, false, false
	case active[0] != v.Call || v.Since == 0:
		v.Call, v.Since, v.Mic, v.Spk = active[0], now, false, false
	}
	if v.Call != "" {
		v.Secs = max(now-v.Since, 0)
	}
	if in, err := api.VoiceIncoming(); err == nil {
		for _, s := range in {
			v.Incoming = append(v.Incoming, ui2.ParseVoiceRing(s))
		}
	}
	if ds, err := api.VoiceDialing(); err == nil {
		v.Dialing = ds
	}
	return v
}

// isVoiceOff is ErrVoiceDisabled, also after an RPC hop turned it into text.
func isVoiceOff(err error) bool {
	return err != nil && (err == ErrVoiceDisabled || err.Error() == ErrVoiceDisabled.Error())
}

func (hv *Hypervisor) ui2Voice(op string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		v := ui2VoiceFrom(r)
		if v.PK != hv.c.PK.Hex() || hv.visor == nil {
			v.Off = true
			hv.render(w, r, ui2.ChatVoice(v))
			return
		}
		id := r.FormValue("call_id")
		var err error
		switch op {
		case "call":
			peer := v.Peer
			if peer == "" {
				peer = strings.TrimSpace(r.FormValue("to"))
			}
			var pk cipher.PubKey
			if err = pk.Set(peer); err == nil {
				_, err = ctx.API.VoiceDial(pk)
			}
		case "answer":
			err = ctx.API.VoiceAnswer(id)
		case "decline":
			err = ctx.API.VoiceDecline(id)
		case "hangup":
			err = ctx.API.VoiceHangup(id)
		case "mute":
			err = ctx.API.VoiceMute(id, v.Mic, v.Spk)
		}
		v = ui2VoiceState(ctx.API, v)
		if err != nil && !v.Off {
			v.Err = err.Error()
		}
		hv.render(w, r, ui2.ChatVoice(v))
	})
}
