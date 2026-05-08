// Package visor pkg/visor/hypervisor_handlers_disc_proxy.go
//
// Hypervisor-hosted dmsg-discovery proxy.
//
// Visors configured with the hypervisor's HTTP address as their dmsg
// discovery URL hit these routes. For Entry GETs we check the embedded
// dmsg server's active client sessions first and synthesize a minimal
// entry (`DelegatedServers: [H]`) when the requested PK is locally
// connected; that lets sibling visors dial each other through the
// hypervisor's relay even when public dmsg-discovery is unreachable.
// Anything else falls through to the upstream public discovery via a
// reverse proxy — POSTs (entry registration) included, so non-sibling
// peers continue to find these visors via the canonical discovery.
//
// `/dmsg-discovery/local/...` mirrors the local-only view for operator
// inspection (UI / CLI), without the upstream fall-through.
package visor

import (
	"encoding/json"
	"fmt"
	"net/http"
	gohttputil "net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/httputil"
)

// localEntryFor synthesizes a dmsg-discovery client entry for `pk` if
// the embedded dmsg server currently has an active session with it.
// Returns nil otherwise. The synthesized entry advertises only the
// embedded server (H) as a delegated server — the local view cannot
// know about whatever public servers the client is also connected to.
// That's fine: the resilience path we care about is "public is down,
// route through H," and `[H]` is exactly what makes that work.
func (hv *Hypervisor) localEntryFor(pk cipher.PubKey) *dmsgdisc.Entry {
	if hv.lanDmsg == nil || hv.lanDmsg.Server == nil {
		return nil
	}
	sessions := hv.lanDmsg.Server.GetSessions()
	if _, ok := sessions[pk]; !ok {
		return nil
	}
	return &dmsgdisc.Entry{
		Static: pk,
		Client: &dmsgdisc.Client{
			DelegatedServers: []cipher.PubKey{hv.lanDmsg.PK},
		},
	}
}

// upstreamDiscProxy returns a reverse proxy to the configured upstream
// dmsg-discovery service, or nil when no upstream URL is configured.
// Used as the fall-through for any path the local view doesn't serve.
func (hv *Hypervisor) upstreamDiscProxy() *gohttputil.ReverseProxy {
	if hv.c.DmsgDiscovery == "" {
		return nil
	}
	target, err := url.Parse(hv.c.DmsgDiscovery)
	if err != nil {
		hv.logger.WithError(err).WithField("url", hv.c.DmsgDiscovery).
			Warn("Could not parse upstream dmsg-discovery URL; proxy fall-through disabled")
		return nil
	}
	rp := gohttputil.NewSingleHostReverseProxy(target)
	// SingleHostReverseProxy forwards exactly the inbound URL.Path to
	// the upstream — paths arrive as "/dmsg-discovery/..." which is
	// what dmsg-discovery expects, so no rewriting needed.
	return rp
}

// discProxyEntryGet returns an http.HandlerFunc that resolves Entry
// lookups: local synthesis if the PK has an active session, otherwise
// proxy to upstream public discovery.
func (hv *Hypervisor) discProxyEntryGet(upstream *gohttputil.ReverseProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pkStr := chi.URLParam(r, "pk")
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid public key"})
			return
		}

		if entry := hv.localEntryFor(pk); entry != nil {
			httputil.WriteJSON(w, r, http.StatusOK, entry)
			return
		}

		if upstream == nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, map[string]string{"error": "entry not found locally and no upstream configured"})
			return
		}
		upstream.ServeHTTP(w, r)
	}
}

// discProxyForward returns an http.HandlerFunc that forwards the
// request to the upstream public discovery unmodified. Used for
// POST/PUT/DELETE on entries (so visors register / update with
// upstream as usual) and the various servers-listing endpoints.
func (hv *Hypervisor) discProxyForward(upstream *gohttputil.ReverseProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if upstream == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "upstream dmsg-discovery not configured"})
			return
		}
		upstream.ServeHTTP(w, r)
	}
}

// discLocalEntryGet returns the local view of `pk`, never falling
// through to upstream. 404 when not locally known.
func (hv *Hypervisor) discLocalEntryGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pkStr := chi.URLParam(r, "pk")
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid public key"})
			return
		}
		entry := hv.localEntryFor(pk)
		if entry == nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, map[string]string{"error": "entry not in local registry"})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, entry)
	}
}

// discLocalAll returns every PK currently sessioned with the embedded
// dmsg server, with synthesized entries. Useful for operator
// inspection ("who is connected to my hypervisor's relay right now?").
func (hv *Hypervisor) discLocalAll() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := []*dmsgdisc.Entry{}
		if hv.lanDmsg != nil && hv.lanDmsg.Server != nil {
			for pk := range hv.lanDmsg.Server.GetSessions() {
				entries = append(entries, &dmsgdisc.Entry{
					Static: pk,
					Client: &dmsgdisc.Client{
						DelegatedServers: []cipher.PubKey{hv.lanDmsg.PK},
					},
				})
			}
		}
		httputil.WriteJSON(w, r, http.StatusOK, entries)
	}
}

// localPKsJSONHandler is a tiny convenience for tooling that just
// wants the list of locally-connected PK strings without the entry
// envelopes. Same data as discLocalAll, simpler shape.
func (hv *Hypervisor) localPKsJSONHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pks := []string{}
		if hv.lanDmsg != nil && hv.lanDmsg.Server != nil {
			for pk := range hv.lanDmsg.Server.GetSessions() {
				pks = append(pks, pk.String())
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pks) //nolint:errcheck
	}
}

// discoveryURLForVisor returns the URL visors should use as their
// dmsg-discovery primary when this hypervisor is acting as proxy.
// Empty when the hypervisor's HTTP address isn't remotely reachable
// (loopback / unspecified bind) — operator should pin HTTPAddr to a
// non-loopback host:port for this to work, and ensure the chosen
// port is reachable from the visors that will use it (firewall /
// port-forward as appropriate).
func (hv *Hypervisor) discoveryURLForVisor() string {
	addr := hv.c.HTTPAddr
	if addr == "" {
		return ""
	}
	hostPart := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		hostPart = addr[:i]
	}
	if hostPart == "" || hostPart == "127.0.0.1" || hostPart == "localhost" || hostPart == "0.0.0.0" || hostPart == "::" {
		return ""
	}
	scheme := "http"
	if hv.c.EnableTLS {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, addr)
}
