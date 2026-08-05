// Package main cmd/wasm-visor/configoverride_js.go c3-vis-wasm
//
// Runtime reconfiguration for the browser edge. A wasm visor has no on-disk
// config file, so its service endpoints came straight from the embedded
// deployment defaults (visorcore.ResolveServices(nil)) and were effectively
// fixed. This makes them EDITABLE at runtime: the page persists an override
// object in localStorage (the SharedWorker can't read localStorage, so the
// page owns it) and passes it as the 5th arg to skywireVisor.boot; Go merges it
// onto the resolved services before wiring dmsg / the router / autoconnect. The
// effective result is what SelfRuntimeConfig renders, so the config tab shows —
// and the runtime uses — the same values. Editing is Save-and-reload: overrides
// take effect on the next boot (the page reloads the worker after persisting).
package main

import (
	"encoding/json"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// effectiveSvc is the service config the running edge is ACTUALLY wired to —
// deployment defaults with any boot-time override applied. SelfRuntimeConfig
// renders this (not a fresh ResolveServices(nil)) so the config view never
// drifts from what dmsg/router/autoconnect were built with. Seeded to the pure
// defaults; bootEdge overwrites it once the override (if any) is applied.
var effectiveSvc = visorcore.ResolveServices(nil)

// cfgOverride holds the raw override object supplied at boot (from the page's
// localStorage), so the config view can mark which fields are user-pinned.
var cfgOverride map[string]interface{}

// applyCfgOverride merges a browser-supplied JSON override onto svc. Only keys
// present in the override change; everything else keeps the deployment default.
// Recognised keys mirror the runtime-config view's field names. Malformed JSON
// or unparseable values are logged and skipped rather than aborting boot.
func applyCfgOverride(svc *visorcore.Services, jsonStr string) {
	if jsonStr == "" || jsonStr == "{}" {
		return
	}
	var o map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &o); err != nil {
		vlog("config-override: ignoring malformed JSON: " + err.Error())
		return
	}
	cfgOverride = o

	str := func(k string) (string, bool) {
		v, ok := o[k].(string)
		return v, ok && v != ""
	}
	if v, ok := str("dmsg_discovery"); ok {
		svc.DmsgDiscoveryDmsg = v
	}
	if v, ok := str("transport_discovery"); ok {
		svc.TransportDiscoveryDmsg = v
	}
	if v, ok := str("address_resolver"); ok {
		svc.AddressResolverDmsg = v
	}
	if v, ok := str("route_finder"); ok {
		svc.RouteFinderDmsg = v
	}
	if v, ok := str("service_discovery"); ok {
		svc.ServiceDiscoveryDmsg = v
	}
	if v, ok := str("conf_service"); ok {
		svc.ConfDmsg = v
	}
	if v, ok := str("uptime_tracker"); ok {
		svc.UptimeTrackerDmsg = v
	}
	if v, ok := str("reward_system"); ok {
		svc.RewardSystemDmsg = v
	}
	if v, ok := o["min_hops"].(float64); ok && v >= 0 {
		svc.MinHops = uint16(v)
	}
	if pks, ok := overridePKList(o["route_setup_nodes"]); ok {
		svc.RouteSetupNodes = pks
	}
	if pks, ok := overridePKList(o["transport_setup_nodes"]); ok {
		svc.TransportSetupNodes = pks
	}
	if ss, ok := overrideStrList(o["stun_servers"]); ok {
		svc.StunServers = ss
	}
	vlog("config-override: applied browser overrides to service config")
}

// overridePKList parses a JSON array of hex public keys; invalid entries are
// dropped. ok is false when the value isn't an array (key absent → no override).
func overridePKList(v interface{}) ([]cipher.PubKey, bool) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]cipher.PubKey, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		var pk cipher.PubKey
		if s != "" && pk.Set(s) == nil {
			out = append(out, pk)
		}
	}
	return out, true
}

// overrideStrList parses a JSON array of strings (e.g. stun host:port entries).
func overrideStrList(v interface{}) ([]string, bool) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, true
}
