// Package visor pkg/visor/hypervisor_handlers_ui2_chat.go c3-vis-core
package visor

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	skychatgroup "github.com/skycoin/skywire/pkg/skychat/group"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The chat tab. Direct messages go through the local skychat app as the
// Angular tab's /skychat/proxy calls do, rooms through the group RPCs.
// Like the Angular tab it serves only the hypervisor's own visor.

func (hv *Hypervisor) ui2ChatRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/chat", hv.ui2Chat(""))
	r.Get("/ui2/visors/{pk}/chat/log", hv.ui2Chat("log"))
	r.Post("/ui2/visors/{pk}/chat/send", hv.ui2Chat("send"))
	r.Post("/ui2/visors/{pk}/chat/delete", hv.ui2Chat("delete"))
	r.Get("/ui2/visors/{pk}/chat/groups/log", hv.ui2Chat("glog"))
	r.Post("/ui2/visors/{pk}/chat/groups/send", hv.ui2Chat("gsend"))
	r.Post("/ui2/visors/{pk}/chat/groups/create", hv.ui2Chat("gcreate"))
	r.Post("/ui2/visors/{pk}/chat/groups/join", hv.ui2Chat("gjoin"))
	r.Get("/ui2/visors/{pk}/chat/groups/invite", hv.ui2Chat("ginvite"))
	r.Post("/ui2/visors/{pk}/chat/groups/add-member", hv.ui2Chat("gadd"))
	r.Post("/ui2/visors/{pk}/chat/groups/ask-again", hv.ui2Chat("gask"))
	r.Post("/ui2/visors/{pk}/chat/password", hv.ui2Chat("password"))
	hv.ui2VoiceRoutes(r)
}

// ui2SkychatCall runs one request against the local skychat app.
func (hv *Hypervisor) ui2SkychatCall(method, path, query string, body any) ([]byte, int, error) {
	var rd *bytes.Reader
	hdr := http.Header{}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rd = bytes.NewReader(b)
		hdr.Set("Content-Type", "application/json")
	} else {
		rd = bytes.NewReader(nil)
	}
	code, _, out, err := hv.visor.SkychatProxy(method, path, query, hdr, rd)
	if err == nil && code/100 != 2 {
		err = errors.New(strings.TrimSpace(string(out)))
		if len(out) == 0 {
			err = errors.New(http.StatusText(code))
		}
	}
	return out, code, err
}

// ui2ChatDM fills the direct message view for peer.
func (hv *Hypervisor) ui2ChatDM(c ui2.Chat) ui2.Chat {
	c.History = true
	b, code, err := hv.ui2SkychatCall(http.MethodGet, "history", "limit=500", nil)
	var ms []ui2.ChatMsg
	switch {
	case code == http.StatusServiceUnavailable:
		c.History = false
	case err != nil:
		c.Err = err.Error()
	default:
		if ms, err = ui2.ParseChatHistory(b); err != nil {
			c.Err = err.Error()
		}
	}
	return ui2.ChatOf(c, ms)
}

// ui2ChatRooms fills the room view for the selected room.
func ui2ChatRooms(api visorapi.API, c ui2.Chat, group string) ui2.Chat {
	gs, err := api.GroupList()
	if err != nil {
		c.Err = err.Error()
		return c
	}
	c.Groups = gs
	for i := range gs {
		if gs[i].ID == group {
			c.Group = &gs[i]
		}
	}
	if c.Group == nil {
		return c
	}
	ms, err := api.GroupPoll(time.Time{})
	if err != nil {
		c.Err = err.Error()
		return c
	}
	for _, m := range ms {
		if m.GroupID == group {
			c.GMsgs = append(c.GMsgs, m)
		}
	}
	return c
}

func (hv *Hypervisor) ui2Chat(op string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		c := ui2.Chat{PK: r.PathValue("pk"), Mode: r.FormValue("mode"), Peer: r.FormValue("peer")}
		if c.PK != hv.c.PK.Hex() || hv.visor == nil {
			c.Remote = true
			hv.render(w, r, ui2.ChatPage(c))
			return
		}
		var pk cipher.PubKey
		group := r.FormValue("group")
		switch op {
		case "":
			if c.Peer != "" && pk.Set(c.Peer) != nil {
				c.Peer = ""
			}
			c.Voice = ui2VoiceState(ctx.API, ui2.Voice{PK: c.PK, Peer: c.Peer, CanCall: c.Mode != "groups"})
			if c.Mode == "groups" {
				hv.render(w, r, ui2.ChatPage(ui2ChatRooms(ctx.API, c, group)))
				return
			}
			c.PwSet, _ = ctx.API.SkychatPasswordIsSet() //nolint:errcheck
			hv.render(w, r, ui2.ChatPage(hv.ui2ChatDM(c)))
		case "log":
			c = hv.ui2ChatDM(c)
			hv.render(w, r, ui2.ChatLog(c), ui2.ChatPeers(c, true))
		case "send", "delete":
			var err error
			if op == "send" {
				c.Peer = strings.TrimSpace(r.FormValue("to"))
				if err = pk.Set(c.Peer); err == nil {
					body := map[string]any{"recipient": c.Peer, "message": r.FormValue("message"), "network": "auto"}
					if rt := r.FormValue("reply_to"); rt != "" {
						body["reply_to"] = rt
					}
					_, _, err = hv.ui2SkychatCall(http.MethodPost, "message", "", body)
				}
			} else {
				_, _, err = hv.ui2SkychatCall(http.MethodPost, "delete", "", map[string]string{"pk": c.Peer, "id": r.FormValue("id")})
			}
			c = hv.ui2ChatDM(c)
			if err != nil {
				c.Err = err.Error()
			}
			hv.render(w, r, ui2.ChatLog(c), ui2.ChatPeers(c, true))
		case "glog":
			hv.render(w, r, ui2.ChatRoomLog(ui2ChatRooms(ctx.API, c, group)))
		case "gsend":
			err := ctx.API.GroupSend(visorapi.GroupSendArgs{ID: group, Text: r.FormValue("text")})
			c = ui2ChatRooms(ctx.API, c, group)
			if err != nil {
				hv.render(w, r, ui2.ChatRoomLog(c), ui2.Toast(err.Error(), true))
				return
			}
			hv.render(w, r, ui2.ChatRoomLog(c))
		case "password":
			hv.render(w, r, ui2.ChatPassword(hv.ui2ChatPassword(r, ctx, c)))
		case "gcreate", "gjoin", "ginvite", "gadd", "gask":
			var err error
			switch op {
			case "gcreate":
				mode := skychatgroup.ModePublic
				if r.FormValue("mode") == "private" {
					mode = skychatgroup.ModePrivate
				}
				var info visorapi.GroupInfo
				info, c.Invite, err = ctx.API.GroupCreate(visorapi.GroupCreateArgs{Name: strings.TrimSpace(r.FormValue("name")), Mode: mode})
				group = info.ID
			case "gjoin":
				var info visorapi.GroupInfo
				info, err = ctx.API.GroupJoin(visorapi.GroupJoinArgs{Invite: strings.TrimSpace(r.FormValue("invite"))})
				group = info.ID
			case "gask":
				_, err = ctx.API.GroupAskAgain(group)
			case "ginvite":
				c.Invite, err = ctx.API.GroupInvite(group)
			case "gadd":
				if err = pk.Set(strings.TrimSpace(r.FormValue("pk"))); err == nil {
					_, err = ctx.API.GroupAddMember(group, pk)
				}
			}
			c = ui2ChatRooms(ctx.API, c, group)
			if err != nil {
				c.Err = err.Error()
			}
			hv.render(w, r, ui2.ChatRooms(c, false))
		}
	})
}

// ui2ChatPassword sets or clears the skychat password with the Angular
// panel's checks: 6 to 64 characters, typed twice, and the current one to
// change or clear a set password.
func (hv *Hypervisor) ui2ChatPassword(r *http.Request, ctx *httpCtx, c ui2.Chat) ui2.Chat {
	c.PwSet, _ = ctx.API.SkychatPasswordIsSet() //nolint:errcheck
	old, nw := r.FormValue("old"), r.FormValue("new")
	var err error
	switch {
	case r.FormValue("clear") != "":
		if old == "" {
			c.PwErr = hv.ui2T(r, "skychat.password.errors.old-required")
			return c
		}
		err = ctx.API.ClearSkychatPassword(old)
	case len(nw) < 6 || len(nw) > 64:
		c.PwErr = hv.ui2T(r, "skychat.password.errors.length")
		return c
	case nw != r.FormValue("confirm"):
		c.PwErr = hv.ui2T(r, "skychat.password.errors.mismatch")
		return c
	default:
		err = ctx.API.SetSkychatPassword(old, nw)
	}
	if err != nil {
		c.PwErr = err.Error()
		return c
	}
	c.PwSet, _ = ctx.API.SkychatPasswordIsSet() //nolint:errcheck
	return c
}
