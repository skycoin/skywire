// Package visor pkg/visor/hypervisor_handlers_dmsg.go c3-vis-core
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
			Enabled:       true,
			PK:            hv.lanDmsg.PK,
			Address:       hv.lanDmsg.Address,
			PublicAddress: hv.lanDmsg.PublicAddress,
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

// postDmsgReconnect closes every dmsg session so the client re-dials at once.
//
// This is the "the network under me moved" action. A phone that switches
// Wi-Fi ↔ cellular, or that the carrier hands a new IP during a handover,
// keeps its old TCP sockets in ESTABLISHED: nothing has been sent on them
// since the move, so neither end knows they are dead. The visor only finds
// out when yamux's keepalive fails to write (30s interval + 45s write
// timeout), and until then it is gone from the network — the hypervisor
// sees the peer stop answering, dmsg peers cannot reach its listeners, and
// the whole thing repeats on the next handover. Mobile data is where this
// is constant rather than occasional.
//
// The OS knows the moment it happens, so the host app tells us (Android:
// ConnectivityManager's default-network callback) and we drop the dead
// sockets immediately instead of waiting out a timeout nobody can shorten
// safely. Everything that rides dmsg recovers behind it: the serve loop
// re-dials servers and republishes the discovery entry, and the hypervisor
// RPC conn (a stream on one of those sessions) fails and is redialled by
// ServeRPCClient.
//
// Idempotent and cheap when nothing was wrong — reconnecting costs about a
// second — so a caller that is unsure is better off calling it.
func (hv *Hypervisor) postDmsgReconnect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "visor not available"})
			return
		}
		closed, err := hv.visor.DmsgReconnect()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		hv.logger.WithField("sessions_closed", closed).
			Info("Dmsg sessions dropped on request; the client will re-dial.")
		httputil.WriteJSON(w, r, http.StatusOK, DmsgReconnectResult{SessionsClosed: closed})
	}
}

// DmsgReconnectResult reports how many sessions postDmsgReconnect tore down.
type DmsgReconnectResult struct {
	SessionsClosed int `json:"sessions_closed"`
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
	pks := make([]cipher.PubKey, 0, len(hv.remoteVisors)+1)
	if hv.visor != nil {
		pks = append(pks, hv.visor.conf.PK) // always track self
	}

	// Track dmsg round-trip ONLY for visors that are currently
	// connected — i.e. answered a Summary RPC within cacheFreshWindow.
	// remoteVisors accumulates every visor that ever connected (it's
	// pruned on a broken-conn Summary in the UI handler, but only while
	// the UI is polling), so tracking it directly made the dmsg-tracker
	// re-resolve + ping long-disconnected peers every cycle — hammering
	// the dmsg-discovery (the source of the 202 → HTTP-fallback churn)
	// and emitting "Failed to re-create dmsgtracker" warns for dead
	// peers. A disconnected visor stops refreshing its summaryCache
	// entry and ages out here, so the tracker follows live connections.
	hv.mu.RLock()
	remotes := make([]cipher.PubKey, 0, len(hv.remoteVisors))
	for pk := range hv.remoteVisors {
		remotes = append(remotes, pk)
	}
	hv.mu.RUnlock()

	now := time.Now()
	hv.summaryCacheMx.RLock()
	for _, pk := range remotes {
		if c, ok := hv.summaryCache[pk]; ok && now.Sub(c.seenAt) < cacheFreshWindow {
			pks = append(pks, pk)
		}
	}
	hv.summaryCacheMx.RUnlock()

	if hv.visor.isDTMReady() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return hv.visor.dmsgTracker.manager.GetBulk(ctx, pks)
	}
	return []dmsgtracker.DmsgClientSummary{}
}
