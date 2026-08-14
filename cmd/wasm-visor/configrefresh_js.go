//go:build js && wasm

// Package main cmd/wasm-visor/configrefresh_js.go c3-vis-wasm
//
// Dynamic config-refresh loop for the browser edge — the wasm port of the
// native visor's startConfigRefresh (pkg/visor/config_refresh.go). A native
// visor re-fetches the deployment conf service hourly and updates its live
// setup-node sets (v.conf.Routing.RouteSetupNodes / Transport.TransportSetupPKs)
// so a long-running visor adopts ROTATED setup nodes without a restart. A
// browser tab is even longer-lived (it may sit open for days) but had no such
// loop, so it never learned about rotated setup nodes.
//
// This mirrors the native loop's ESSENTIAL behavior, reduced to the fields a
// browser edge actually carries (visorcore.Services): on the same cadence
// (2-minute initial settle, then hourly) it re-fetches the conf service over
// dmsg and updates the in-memory route/transport setup-node sets on effectiveSvc.
// SelfRuntimeConfig renders effectiveSvc, so the config view immediately reflects
// the rotation — the browser analog of native updating v.conf. Newly-rotated
// route-setup PKs are also seeded into the dmsg entry cache (the same permanent
// seed StartDmsgSeeded installs at boot) so a DialStream to them resolves rather
// than 404ing against a dmsg-only discovery.
//
// PARITY NOTE — route ORIGINATION uses the router's boot snapshot: the router
// captured SetupNodes at build (router.Config.SetupNodes) and the Router
// interface exposes no live setter, so a freshly-rotated setup node enters the
// route-origination dialer only on the next reload. This is the SAME limitation
// the native visor has (its router likewise snapshots EffectiveRouteSetupNodes()
// and startConfigRefresh does not poke it — the refresh keeps the config/health
// surface current, not the router's dial list). The high-value, immediately-live
// part — the config surface + dmsg reachability of the rotated set — is ported;
// the router-snapshot part is deferred identically on both shells.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
)

// configRefreshInterval matches the native visor's cadence (pkg/visor/config_refresh.go).
const configRefreshInterval = 1 * time.Hour

// startConfigRefresh periodically re-fetches the deployment conf service and
// updates the browser edge's in-memory setup-node sets, so a long-lived tab
// adopts rotated route/transport setup nodes (and drops removed ones) without a
// reload. Started from bootEdge alongside the other background goroutines, on the
// boot ctx. Uses the package vars ctx-scoped dmsgC / effectiveSvc / vlog.
func startConfigRefresh(ctx context.Context) {
	// Initial delay to let dmsg connect before the first fetch — native waits the
	// same 2 minutes for the same reason.
	select {
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(configRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshSetupNodes(ctx)
		}
	}
}

// refreshSetupNodes re-fetches the conf service and updates effectiveSvc's
// route/transport setup-node sets when they changed. Under js/wasm goroutines
// run cooperatively on a single thread (no true parallelism), so the lockless
// update of effectiveSvc is consistent with how the rest of the edge already
// reads it (selfprovider_js.go copies the whole struct without a lock).
func refreshSetupNodes(ctx context.Context) {
	services := fetchServicesConfig(ctx)
	if services == nil {
		return
	}

	// Route setup nodes. A non-empty, changed set replaces the in-memory set;
	// newly-added PKs are seeded into the dmsg entry cache so DialStream to them
	// resolves (route origination dials the setup node over dmsg).
	if len(services.RouteSetupNodes) > 0 && !pubKeysEqual(effectiveSvc.RouteSetupNodes, services.RouteSetupNodes) {
		added := pubKeysAdded(effectiveSvc.RouteSetupNodes, services.RouteSetupNodes)
		effectiveSvc.RouteSetupNodes = services.RouteSetupNodes
		seedServiceEntries(added)
		vlog(fmt.Sprintf("config-refresh: route_setup_nodes updated (%d keys, %d new) from conf service", len(services.RouteSetupNodes), len(added)))
	}

	// Transport setup PKs (the transport-setup accept-side trust list). Updating
	// the in-memory set keeps the config surface current; the already-serving
	// dmsg:47 listener keeps its boot snapshot of the trust list (same as native,
	// whose accept-side trust is also captured at Serve time).
	if len(services.TransportSetupPKs) > 0 && !pubKeysEqual(effectiveSvc.TransportSetupNodes, services.TransportSetupPKs) {
		effectiveSvc.TransportSetupNodes = services.TransportSetupPKs
		vlog(fmt.Sprintf("config-refresh: transport_setup updated (%d keys) from conf service", len(services.TransportSetupPKs)))
	}
}

// fetchServicesConfig fetches the deployment conf service over dmsg and returns
// the parsed prod service set. Browser-edge only: unlike the native visor there
// is no clearnet-HTTP fallback (the deployment is dmsg-only for a browser tab).
// Mirrors the DMSG branch of the native fetchServicesConfig.
func fetchServicesConfig(ctx context.Context) *deployment.Services {
	confDmsg := effectiveSvc.ConfDmsg
	if confDmsg == "" || dmsgC == nil {
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := &http.Client{
		Transport: dmsghttp.MakeHTTPTransport(fetchCtx, dmsgC),
		Timeout:   25 * time.Second,
	}
	resp, err := client.Get(confDmsg)
	if err != nil {
		vlog("config-refresh: fetch conf service over dmsg failed: " + err.Error())
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck
	return parseServicesResponse(resp.Body)
}

// parseServicesResponse decodes the conf-service response. The service returns
// {prod: {...}, test: {...}}; we take prod. Uses the deployment package's own
// types (EnvServices/Services) — the native visor decodes into the same shapes
// via the visorconfig aliases (which are excluded on the GOOS=js build path, so
// the deployment types are used directly here).
func parseServicesResponse(body io.Reader) *deployment.Services {
	data, err := io.ReadAll(body)
	if err != nil {
		vlog("config-refresh: read conf response failed: " + err.Error())
		return nil
	}
	var env deployment.EnvServices
	if err := json.Unmarshal(data, &env); err != nil {
		vlog("config-refresh: parse conf response failed: " + err.Error())
		return nil
	}
	var services deployment.Services
	if err := json.Unmarshal(env.Prod, &services); err != nil {
		vlog("config-refresh: parse prod services failed: " + err.Error())
		return nil
	}
	return &services
}

// seedServiceEntries permanently seeds the given PKs as direct-client entries
// delegated to the deployment's dmsg servers — the same seed StartDmsgSeeded
// installs at boot for the service/setup-node set, so a DialStream to a
// freshly-rotated setup node resolves instead of 404ing against a dmsg-only
// discovery (setup nodes are direct clients and never publish their own entry).
func seedServiceEntries(pks []cipher.PubKey) {
	if len(pks) == 0 || dmsgC == nil {
		return
	}
	var seedServerPKs []cipher.PubKey
	for _, ds := range effectiveSvc.DmsgServers {
		var spk cipher.PubKey
		if spk.UnmarshalText([]byte(ds.Static)) == nil && !spk.Null() {
			seedServerPKs = append(seedServerPKs, spk)
		}
	}
	for _, pk := range pks {
		if pk.Null() {
			continue
		}
		dmsgC.SeedEntryCache(pk, &disc.Entry{
			Version: "0.0.1",
			Static:  pk,
			Client:  &disc.Client{DelegatedServers: seedServerPKs},
		})
	}
}

// pubKeysEqual reports whether a and b hold the same set of public keys
// (order-independent). Mirrors the native helper of the same name.
func pubKeysEqual(a, b []cipher.PubKey) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[cipher.PubKey]struct{}, len(a))
	for _, k := range a {
		set[k] = struct{}{}
	}
	for _, k := range b {
		if _, ok := set[k]; !ok {
			return false
		}
	}
	return true
}

// pubKeysAdded returns the keys present in next but not in prev (the rotated-in
// set), so only genuinely new setup nodes are seeded into the dmsg cache.
func pubKeysAdded(prev, next []cipher.PubKey) []cipher.PubKey {
	old := make(map[cipher.PubKey]struct{}, len(prev))
	for _, k := range prev {
		old[k] = struct{}{}
	}
	var added []cipher.PubKey
	for _, k := range next {
		if _, ok := old[k]; !ok {
			added = append(added, k)
		}
	}
	return added
}
