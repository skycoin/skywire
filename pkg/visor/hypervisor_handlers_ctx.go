// Package visor pkg/visor/hypervisor.go
package visor

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

func (hv *Hypervisor) visorConn(pk cipher.PubKey) (Conn, bool) {
	hv.mu.RLock()
	conn, ok := hv.remoteVisors[pk]
	hv.mu.RUnlock()

	return conn, ok
}

// proxiedVisorConn looks up pk among the visors reachable through
// any connected sub-hypervisor. When found, returns a Conn whose
// API is a proxiedVisorAPI that re-routes each operator action
// through the sub-hypervisor's HVxxx RPC. Returns ok=false when no
// sub-hypervisor reports the PK as directly connected to it.
//
// Walks each direct remote that's also a hypervisor (i.e. has the
// HVListDirectVisors method) and asks for its visor list. Skips
// sub-hypervisors that timeout or aren't running as hypervisors.
// Bounded by hv.mu / RPC timeouts; the goroutine fan-out matches
// the pattern HVListVisors uses internally so the latency is
// comparable to one sub-hypervisor round-trip.
func (hv *Hypervisor) proxiedVisorConn(pk cipher.PubKey) (Conn, bool) {
	hv.mu.RLock()
	remotes := make(map[cipher.PubKey]Conn, len(hv.remoteVisors))
	for hk, hc := range hv.remoteVisors {
		remotes[hk] = hc
	}
	hv.mu.RUnlock()

	for _, hyperConn := range remotes {
		if hyperConn.API == nil {
			continue
		}
		subEntries, err := hyperConn.API.HVListDirectVisors()
		if err != nil {
			// Most common: remote isn't a hypervisor. Quietly skip.
			continue
		}
		for _, e := range subEntries {
			if e.PK != pk {
				continue
			}
			return Conn{
				Addr: hyperConn.Addr,
				API:  newProxiedVisorAPI(pk, hyperConn.API),
			}, true
		}
	}
	return Conn{}, false
}

func (hv *Hypervisor) withCtx(vFunc valuesFunc, hFunc handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rv, ok := vFunc(w, r)
		if !ok {
			return
		}
		// For remote visors, enforce a timeout so slow/dead visors don't hang the UI.
		// Uses a buffered response writer so the handler goroutine writes to a buffer,
		// and only the winner (handler or timeout) writes to the real ResponseWriter.
		// Skip the timeout wrapper for WebSocket upgrades — the buffered writer
		// doesn't implement http.Hijacker, which websocket.Accept requires.
		isWebSocket := r.Header.Get("Upgrade") == "websocket"
		if rv.isRemote && !isWebSocket {
			tw := newTimeoutResponseWriter()
			done := make(chan struct{})
			go func() {
				defer close(done)
				hFunc(tw, r, rv)
			}()
			select {
			case <-done:
				tw.copyTo(w)
			case <-time.After(remoteVisorTimeout):
				httputil.WriteJSON(w, r, http.StatusGatewayTimeout,
					fmt.Errorf("remote visor %s did not respond within %v", rv.Addr.PK, remoteVisorTimeout))
			}
		} else {
			hFunc(w, r, rv)
		}
	}
}

func (hv *Hypervisor) visorCtx(w http.ResponseWriter, r *http.Request) (*httpCtx, bool) {
	pk, err := pkFromParam(r, "pk")
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, err)
		return nil, false
	}

	if useCsrf && (r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE") {
		csrfToken := r.Header.Get(CSRFHeaderName)
		if csrfToken == "" {
			errMsg := fmt.Errorf("no csrf token for %s request", r.Method)
			httputil.WriteJSON(w, r, http.StatusForbidden, errMsg)
			return nil, false
		}

		err = verifyCSRFToken(csrfToken)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusForbidden, err)
			return nil, false
		}
	}

	if pk != hv.c.PK {
		v, ok := hv.visorConn(pk)
		if ok {
			return &httpCtx{
				Conn:     v,
				isRemote: true,
			}, true
		}

		// Not directly connected. Check whether one of our connected
		// sub-hypervisors has this visor, and if so wrap their API
		// in a proxiedVisorAPI so the operator can drill into the
		// node via this hypervisor's UI even though the routing
		// goes via the sub-hypervisor. Read-side methods become
		// HVVisorSummary projections; write-side methods route
		// through the corresponding HVxxx RPCs.
		if proxyConn, ok := hv.proxiedVisorConn(pk); ok {
			return &httpCtx{
				Conn:     proxyConn,
				isRemote: true,
			}, true
		}

		// Soften the "never heard of this PK" 404 to a "we knew it
		// but it's reconnecting" 503 when the summaryCache still
		// holds a recent snapshot. The UI's retry loop on 503 then
		// catches the peer the moment its hypervisor_client redials
		// and re-registers, rather than surfacing a hard-fail "not
		// found" mid-flap. Caller still gets a clean error message
		// for the case where the cache itself doesn't know the PK.
		hv.summaryCacheMx.RLock()
		cached, hasCache := hv.summaryCache[pk]
		hv.summaryCacheMx.RUnlock()
		if hasCache {
			elapsed := time.Since(cached.seenAt).Round(time.Second)
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				fmt.Errorf("visor '%s' is currently disconnected (last seen %s ago) — retrying", pk, elapsed))
			return nil, false
		}

		httputil.WriteJSON(w, r, http.StatusNotFound, fmt.Errorf("visor of pk '%s' not found", pk))
		return nil, false
	}
	hv.mu.Lock()
	conn := hv.selfConn
	hv.mu.Unlock()

	return &httpCtx{
		Conn: conn,
	}, true
}

func (hv *Hypervisor) appCtx(w http.ResponseWriter, r *http.Request) (*httpCtx, bool) {
	ctx, ok := hv.visorCtx(w, r)
	if !ok {
		return nil, false
	}

	appName := chi.URLParam(r, "app")

	app, err := ctx.API.App(appName)
	if err != nil {
		errMsg := fmt.Errorf("can not find app of name %s from visor %s", appName, ctx.Addr.PK)
		httputil.WriteJSON(w, r, http.StatusNotFound, errMsg)
		return nil, false
	}

	ctx.App = app

	return ctx, true
}

func (hv *Hypervisor) tpCtx(w http.ResponseWriter, r *http.Request) (*httpCtx, bool) {
	ctx, ok := hv.visorCtx(w, r)
	if !ok {
		return nil, false
	}

	tid, err := uuidFromParam(r, "tid")
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, err)
		return nil, false
	}

	tp, err := ctx.API.Transport(tid)
	if err != nil {
		if err.Error() == ErrNotFound.Error() {
			errMsg := fmt.Errorf("transport of ID %s is not found", tid)
			httputil.WriteJSON(w, r, http.StatusNotFound, errMsg)

			return nil, false
		}

		httputil.WriteJSON(w, r, http.StatusInternalServerError, err)

		return nil, false
	}

	ctx.Tp = tp

	return ctx, true
}

func (hv *Hypervisor) routeCtx(w http.ResponseWriter, r *http.Request) (*httpCtx, bool) {
	ctx, ok := hv.visorCtx(w, r)
	if !ok {
		return nil, false
	}

	rid, err := ridFromParam(r, "rid")
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, err)
		return nil, false
	}

	ctx.RtKey = rid

	return ctx, true
}
