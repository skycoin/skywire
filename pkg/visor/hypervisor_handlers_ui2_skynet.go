// Package visor pkg/visor/hypervisor_handlers_ui2_skynet.go c3-vis-core
package visor

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The skynet tab: forwarded ports and reverse proxies. Writes go through
// the JSON handlers the Angular tab calls.

func (hv *Hypervisor) ui2SkynetRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/skynet", hv.ui2Skynet(""))
	for _, op := range []string{"ports", "toggle", "whitelist", "remove", "connect", "disconnect"} {
		r.Post("/ui2/visors/{pk}/skynet/"+op, hv.ui2Skynet(op))
	}
}

func ui2LoadSkynet(api visorapi.API, pk string) ui2.Skynet {
	s := ui2.Skynet{PK: pk}
	ports, err := api.ListForwardedPorts()
	if err != nil {
		s.Err = err.Error()
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	s.Ports = ports
	if fw, err := api.ListRawTCP(); err == nil {
		b, _ := json.Marshal(fw) //nolint:errcheck
		var m map[string]ui2.SkynetForward
		if json.Unmarshal(b, &m) == nil {
			for id, f := range m {
				f.ID = id
				s.Forwards = append(s.Forwards, f)
			}
			sort.Slice(s.Forwards, func(i, j int) bool { return s.Forwards[i].LocalPort < s.Forwards[j].LocalPort })
		}
	}
	return s
}

// ui2ParsePKs reads public keys separated by commas or spaces.
func ui2ParsePKs(raw string) ([]cipher.PubKey, error) {
	var pks []cipher.PubKey
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r' }) {
		var pk cipher.PubKey
		if err := pk.Set(f); err != nil {
			return nil, errors.New(f)
		}
		pks = append(pks, pk)
	}
	return pks, nil
}

func (hv *Hypervisor) ui2Skynet(op string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		if op == "" {
			hv.render(w, r, ui2.SkynetPage(ui2LoadSkynet(ctx.API, pk)))
			return
		}
		fail := func(msg string) {
			s := ui2LoadSkynet(ctx.API, pk)
			hv.render(w, r, ui2.SkynetBody(s), ui2.Toast(msg, true))
		}
		port, _ := strconv.Atoi(r.FormValue("port")) //nolint:errcheck
		find := func() (visorapi.ForwardedPort, bool) {
			ports, err := ctx.API.ListForwardedPorts()
			if err == nil {
				for _, p := range ports {
					if p.Port == port {
						return p, true
					}
				}
			}
			return visorapi.ForwardedPort{}, false
		}
		var ok bool
		switch op {
		case "ports":
			if port < 1 || port > 65535 {
				fail(hv.ui2T(r, "ui2.skynet.bad-port"))
				return
			}
			wl, err := ui2ParsePKs(r.FormValue("whitelist"))
			if err != nil {
				fail(hv.ui2T(r, "ui2.skynet.bad-pk", "pk", err.Error()))
				return
			}
			local, _ := strconv.Atoi(r.FormValue("local_port")) //nolint:errcheck
			fp := visorapi.ForwardedPort{
				Port: port, LocalPort: local, ProxyAddr: strings.TrimSpace(r.FormValue("proxy_addr")),
				Label: strings.TrimSpace(r.FormValue("label")), Description: strings.TrimSpace(r.FormValue("description")),
				Skynet: r.FormValue("skynet") != "", DMSG: r.FormValue("dmsg") != "", ShowOnLanding: r.FormValue("show_on_landing") != "",
				Whitelist: wl,
			}
			ok = ui2CallAPI(w, r, hv.postRegisterForwardedPort(), fp)
		case "toggle", "whitelist":
			fp, found := find()
			if !found {
				fail(hv.ui2T(r, "ui2.skynet.bad-port"))
				return
			}
			if op == "toggle" {
				switch r.FormValue("field") {
				case "skynet":
					fp.Skynet = !fp.Skynet
				case "dmsg":
					fp.DMSG = !fp.DMSG
				case "show_on_landing":
					fp.ShowOnLanding = !fp.ShowOnLanding
				}
			} else {
				wl, err := ui2ParsePKs(r.FormValue("whitelist"))
				if err != nil {
					fail(hv.ui2T(r, "ui2.skynet.bad-pk", "pk", err.Error()))
					return
				}
				fp.Whitelist = wl
			}
			ok = ui2CallAPI(w, r, hv.postUpdateForwardedPort(), fp)
		case "remove":
			ok = ui2CallAPI(w, r, hv.postDeregisterSkynetPort(), map[string]int{"port": port})
		case "connect":
			var rpk cipher.PubKey
			if err := rpk.Set(strings.TrimSpace(r.FormValue("remote_pk"))); err != nil {
				fail(hv.ui2T(r, "ui2.skynet.bad-pk", "pk", r.FormValue("remote_pk")))
				return
			}
			rp, _ := strconv.Atoi(r.FormValue("remote_port")) //nolint:errcheck
			lp, _ := strconv.Atoi(r.FormValue("local_port"))  //nolint:errcheck
			if rp < 1 || rp > 65535 || lp < 1 || lp > 65535 {
				fail(hv.ui2T(r, "ui2.skynet.bad-port"))
				return
			}
			ok = ui2CallAPI(w, r, hv.postSkynetConnect(), map[string]any{"network": r.FormValue("network"), "remote_pk": rpk, "remote_port": rp, "local_port": lp})
		case "disconnect":
			ok = ui2CallAPI(w, r, hv.postSkynetDisconnect(), map[string]string{"id": r.FormValue("id")})
		}
		if !ok {
			return
		}
		hv.render(w, r, ui2.SkynetBody(ui2LoadSkynet(ctx.API, pk)), ui2.Toast(hv.ui2T(r, "apps.operation-completed"), false))
	})
}
