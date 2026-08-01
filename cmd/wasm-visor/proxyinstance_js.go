//go:build js && wasm

// Package main cmd/wasm-visor/proxyinstance_js.go c3-vis-wasm
// proxyinstance_js.go — a registry that models the tab's skysocks-client-lite
// clearnet proxy as a configurable, app-like instance (Phase 1 of the
// skysocks-lite-as-app design: project_skysocks_lite_proxy_control).
//
// Today (pre-registry) the exit PK was passed in on every fetchClearnet call and
// each browser window kept its own session (skysocks_js.go). Phase 1 introduces a
// single DEFAULT instance ("skysocks-client") that, after transports come up,
// auto-selects a RANDOM public proxy exit — so the iframe browser and the wallet
// "just work" without the operator hand-entering an exit — and surfaces it in the
// shared Apps tab (apps_js.go) where the exit is changeable. Multiple named
// instances + per-consumer binding + the ⚙-panel picker are Phases 2-3.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// defaultProxyID is the id/name of the built-in proxy instance. It matches the
// native launcher app name so the shared Angular Apps tab shows it identically.
const defaultProxyID = "skysocks-client"

// proxyInstance is one configurable skysocks-client-lite instance.
type proxyInstance struct {
	ID     string        `json:"name"`   // "skysocks-client"[-N]; native app name
	Label  string        `json:"label"`  // user-facing description
	ExitPK cipher.PubKey `json:"-"`      // current exit; zero = unset
	Exit   string        `json:"exit"`   // ExitPK.Hex() (or "") for JSON/JS
	Auto   bool          `json:"auto"`   // true = auto-select a random exit
	Status int           `json:"status"` // 0 stopped/configured, 1 running
	Detail string        `json:"detail"`
}

var (
	proxyRegMu sync.Mutex
	proxyReg   = map[string]*proxyInstance{
		defaultProxyID: {ID: defaultProxyID, Label: "Default (auto)", Auto: true},
	}
)

// proxyInstanceExit returns the configured exit for an instance id.
func proxyInstanceExit(id string) (cipher.PubKey, bool) {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	inst, ok := proxyReg[id]
	if !ok || inst.ExitPK.Null() {
		return cipher.PubKey{}, false
	}
	return inst.ExitPK, true
}

// proxyDefaultExit returns the default instance's exit (used by consumers that
// don't pin their own exit, so the auto-selected random proxy is the fallback).
func proxyDefaultExit() (cipher.PubKey, bool) { return proxyInstanceExit(defaultProxyID) }

// setProxyExit sets an instance's exit PK (the wasm analog of the native
// SetAppPK). Clearing (null pk) is allowed. Turns off Auto once the operator
// pins an exit explicitly.
func setProxyExit(id string, pk cipher.PubKey) error {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	inst, ok := proxyReg[id]
	if !ok {
		return fmt.Errorf("unknown proxy instance: %s", id)
	}
	inst.ExitPK = pk
	inst.Exit = ""
	if !pk.Null() {
		inst.Exit = pk.Hex()
		inst.Auto = false // an explicit exit overrides auto-selection
	}
	return nil
}

// proxyInstanceRunning reports whether a live skysocks session exists to the
// instance's exit (any window). Used to derive the Apps-tab status.
func proxyInstanceRunning(pk cipher.PubKey) bool {
	if pk.Null() {
		return false
	}
	suffix := "|" + pk.Hex()
	skysocksMu.Lock()
	defer skysocksMu.Unlock()
	for k, s := range skysocksSessions {
		if len(k) >= len(suffix) && k[len(k)-len(suffix):] == suffix && s != nil && !s.IsClosed() {
			return true
		}
	}
	return false
}

// proxyInstancesSnapshot returns a copy of the registry with live status filled
// in, for the Apps tab (selfAppStates) and the JS listing hook.
func proxyInstancesSnapshot() []proxyInstance {
	proxyRegMu.Lock()
	out := make([]proxyInstance, 0, len(proxyReg))
	for _, inst := range proxyReg {
		cp := *inst
		out = append(out, cp)
	}
	proxyRegMu.Unlock()
	for i := range out {
		if proxyInstanceRunning(out[i].ExitPK) {
			out[i].Status, out[i].Detail = 1, "Running — exit "+exitShort(out[i].ExitPK)
		} else if !out[i].ExitPK.Null() {
			out[i].Status, out[i].Detail = 0, "Ready — exit "+exitShort(out[i].ExitPK)
		} else {
			out[i].Status, out[i].Detail = 0, "Stopped (no exit selected)"
		}
	}
	return out
}

// isProxyInstance reports whether id names a registered proxy instance.
func isProxyInstance(id string) bool {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	_, ok := proxyReg[id]
	return ok
}

// closeSkysocksExit closes every session to the given exit PK (any window /
// instance), so "stop" on a proxy instance actually severs its egress.
func closeSkysocksExit(pk cipher.PubKey) int {
	if pk.Null() {
		return 0
	}
	suffix := "|" + pk.Hex()
	skysocksMu.Lock()
	defer skysocksMu.Unlock()
	n := 0
	for k, s := range skysocksSessions {
		if len(k) >= len(suffix) && k[len(k)-len(suffix):] == suffix {
			if s != nil {
				_ = s.Close() //nolint:errcheck
			}
			delete(skysocksSessions, k)
			n++
		}
	}
	return n
}

// exitShort renders an exit PK for log/status ("none" when unset), reusing the
// package-wide shortPK(string) helper for the hex-truncation.
func exitShort(pk cipher.PubKey) string {
	if pk.Null() {
		return "none"
	}
	return shortPK(pk.Hex())
}

// startDefaultProxyAuto waits for transports to come up, then auto-selects a
// random public proxy exit for the default instance (unless the operator already
// pinned one). Connection itself is lazy — the first clearnet fetch establishes
// the route to the selected exit (skysocks_js.go). Retries while the SD list is
// empty / until an exit is chosen.
func startDefaultProxyAuto(ctx context.Context, sdPK cipher.PubKey) {
	// Only auto-select for the default instance while it's in Auto mode and
	// hasn't already been given an exit.
	settle := time.Now().Add(8 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		proxyRegMu.Lock()
		inst := proxyReg[defaultProxyID]
		done := inst == nil || !inst.Auto || !inst.ExitPK.Null()
		proxyRegMu.Unlock()
		if done {
			return
		}
		// Wait for at least one transport (settle window) before picking.
		if tpM == nil || tpM.TransportCount() < 1 {
			if time.Now().Before(settle) {
				continue
			}
		}
		if pk, ok := pickRandomProxyExit(ctx, sdPK); ok {
			if err := setProxyExit(defaultProxyID, pk); err == nil {
				// Re-arm Auto: setProxyExit clears it, but an auto-selected
				// exit should remain "auto" so a later manual change is what
				// pins it.
				proxyRegMu.Lock()
				if p := proxyReg[defaultProxyID]; p != nil {
					p.Auto = true
				}
				proxyRegMu.Unlock()
				vlog(fmt.Sprintf("[skysocks-lite] default proxy exit auto-selected: %s", exitShort(pk)))
				return
			}
		}
		// SD empty / no reachable candidate yet — retry.
	}
}

// pickRandomProxyExit fetches the public proxy list from service discovery over
// dmsg and returns a pseudo-randomly chosen exit PK.
func pickRandomProxyExit(ctx context.Context, sdPK cipher.PubKey) (cipher.PubKey, bool) {
	if dmsgC == nil {
		return cipher.PubKey{}, false
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	status, _, body, err := dmsgclient.FetchOverDmsg(fctx, dmsgC, "GET", sdPK.Hex(), "/api/services?type=proxy", nil, nil)
	cancel()
	if err != nil || status != 200 {
		return cipher.PubKey{}, false
	}
	var services []servicedisc.Service
	if err := json.Unmarshal(body, &services); err != nil || len(services) == 0 {
		return cipher.PubKey{}, false
	}
	// Pseudo-random start offset (no math/rand import; time is monotonic enough
	// for exit spreading across tabs).
	off := int(time.Now().UnixNano()) % len(services)
	if off < 0 {
		off += len(services)
	}
	for i := 0; i < len(services); i++ {
		pk := services[(off+i)%len(services)].Addr.PubKey()
		if !pk.Null() && pk != selfPK {
			return pk, true
		}
	}
	return cipher.PubKey{}, false
}

// jsProxyInstances() → JSON array of the proxy instances (name/label/exit/auto/
// status/detail) for the ⚙ picker + Apps tab + the ip-check links.
func jsProxyInstances(_ js.Value, _ []js.Value) interface{} {
	b, err := json.Marshal(proxyInstancesSnapshot())
	if err != nil {
		return "[]"
	}
	return string(b)
}

// jsSetProxyExit(id, pkHex) sets an instance's exit ("" clears it). Returns an
// error string on failure, else null.
func jsSetProxyExit(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 || args[0].String() == "" {
		return "setProxyExit(id, pkHex)"
	}
	id := args[0].String()
	var pk cipher.PubKey
	if len(args) > 1 && !args[1].IsNull() && !args[1].IsUndefined() && args[1].String() != "" {
		if err := pk.UnmarshalText([]byte(args[1].String())); err != nil {
			return "bad pk: " + err.Error()
		}
	}
	if err := setProxyExit(id, pk); err != nil {
		return err.Error()
	}
	return nil
}
