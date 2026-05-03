// Package visor pkg/visor/hypervisor.go
package visor

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
)

func (hv *Hypervisor) getLANDmsgServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.lanDmsg == nil {
			httputil.WriteJSON(w, r, http.StatusOK, LANDmsgServerInfo{Enabled: false})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, LANDmsgServerInfo{
			Enabled: true,
			PK:      hv.lanDmsg.PK,
			Address: hv.lanDmsg.Address,
		})
	}
}

func (hv *Hypervisor) getDmsg() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := hv.getDmsgSummary()
		httputil.WriteJSON(w, r, http.StatusOK, out)
	}
}

// getDmsgSessions returns per-client dmsg session info: main + embedded
// route setup node + embedded transport setup node. Used by the hypervisor
// UI's dmsg settings panel so operators can see at a glance which dmsg
// servers each of the visor's three independent dmsg clients is connected
// to.
func (hv *Hypervisor) getDmsgSessions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, &DmsgClientSessions{})
			return
		}
		sessions, err := hv.visor.DmsgSessions()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, sessions)
	}
}

// postDmsgConnectAll triggers a one-shot "reach every dmsg server now"
// action on the main visor dmsg client. Used by the UI's "Connect to all"
// button for operators running RSN/TPS visors who want to eliminate
// discovery-stale failure modes on demand.
func (hv *Hypervisor) postDmsgConnectAll() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "visor not available"})
			return
		}
		result, err := hv.visor.DmsgConnectAll()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, result)
	}
}

// putDmsgSessionsCount persists dmsg.sessions_count to the visor config and
// triggers an immediate connect-all so the change takes effect without a
// restart. A value of 0 means "connect to every available dmsg server",
// which is the recommended setting for RSN/TPS visors.
func (hv *Hypervisor) putDmsgSessionsCount() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "visor not available"})
			return
		}
		var req dmsgSessionsCountRequest
		if err := httputil.ReadJSON(r, &req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.Count < 0 {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "count must be >= 0"})
			return
		}
		result, err := hv.visor.SetDmsgSessionsCount(req.Count)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, result)
	}
}

// --- Per-visor variants used by the hvui's per-visor DMSG tab. ---
// withCtx + visorCtx hands us an API value that is either the local
// visor (directly) or an rpcClient to a remote visor — either way,
// calling DmsgSessions / DmsgConnectAll / SetDmsgSessionsCount on it
// reaches the right backend. (The HVDmsg* dispatchers exist for the
// nested-hypervisor case which doesn't apply to the hvui.)

func (hv *Hypervisor) getVisorDmsgSessions() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		sessions, err := ctx.API.DmsgSessions()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if sessions == nil {
			httputil.WriteJSON(w, r, http.StatusOK, &DmsgClientSessions{})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, sessions)
	})
}

func (hv *Hypervisor) postVisorDmsgConnectAll() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		result, err := ctx.API.DmsgConnectAll()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, result)
	})
}

func (hv *Hypervisor) putVisorDmsgSessionsCount() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req dmsgSessionsCountRequest
		if err := httputil.ReadJSON(r, &req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.Count < 0 {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "count must be >= 0"})
			return
		}
		result, err := ctx.API.SetDmsgSessionsCount(req.Count)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, result)
	})
}

func (hv *Hypervisor) getDmsgSummary() []dmsgtracker.DmsgClientSummary {
	hv.mu.RLock()
	pks := make([]cipher.PubKey, 0, len(hv.remoteVisors)+1)
	if hv.visor != nil {
		pks = append(pks, hv.visor.conf.PK)
	}
	for pk := range hv.remoteVisors {
		pks = append(pks, pk)
	}
	hv.mu.RUnlock()

	if hv.visor.isDTMReady() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return hv.visor.dmsgTracker.manager.GetBulk(ctx, pks)
	}
	return []dmsgtracker.DmsgClientSummary{}
}
