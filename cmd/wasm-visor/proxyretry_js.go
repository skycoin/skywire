//go:build js && wasm

// Package main cmd/wasm-visor/proxyretry_js.go c3-vis-wasm
// proxyretry_js.go — resilience for the default skysocks-client-lite instance:
//
//  1. An "ever connected" bit per exit so a dropped route is handled the way the
//     operator expects: retry the SAME key (exponential backoff) when the exit had
//     actually worked, but rotate to a NEW random exit when the very first
//     connection could never be established.
//  2. A proactive keepalive / liveness loop that yamux-pings the active exit (and
//     keeps the standbys warm) so a silently-dead route triggers the policy instead
//     of only being noticed on the next fetch — mirroring the native client's
//     sessionKeepAliveLoop.
//
// Companion to proxyinstance_js.go (which owns the pool + rotateAwayFromExit).
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Retry / liveness tuning.
const (
	stickyMaxTries          = 5                // same-key reconnect attempts before rotating
	stickyBaseDelay         = 1 * time.Second  // first backoff
	stickyMaxDelay          = 30 * time.Second // backoff cap
	proxyKeepalivePeriod    = 15 * time.Second // liveness ping cadence (mirrors native)
	proxyKeepaliveFailLimit = 2                // consecutive ping failures ⇒ exit declared dead
)

var (
	proxyConnMu        sync.Mutex
	proxyEverConnected = map[cipher.PubKey]bool{} // exits that established a real (non-probe) route
	proxyStickyActive  = map[cipher.PubKey]bool{} // a sticky-reconnect loop owns this exit
)

// isProbeProxyWindow reports the SELECTION probe window (pool-probe-*). A probe
// success only proves momentary routability for pool selection; it must NOT count
// as "ever connected" — otherwise the very first real fetch failure would wrongly
// be treated as a drop-of-a-working-exit instead of an initial-connect failure.
func isProbeProxyWindow(winID string) bool { return strings.HasPrefix(winID, "pool-probe-") }

// isInternalProxyWindow reports the visor's OWN skysocks windows (probe / sticky
// reconnect / keepalive / warm-standby). Their dial failures must NOT auto-trigger
// reportProxyExitDead — each manages its own failure handling — otherwise a
// keepalive/sticky failure would re-enter the policy it is already running.
func isInternalProxyWindow(winID string) bool {
	return strings.HasPrefix(winID, "pool-probe-") ||
		strings.HasPrefix(winID, "sticky-") ||
		strings.HasPrefix(winID, "keepalive-") ||
		strings.HasPrefix(winID, "pool-warm-")
}

// markProxyExitConnected records that a real (non-probe) route to pk came up.
func markProxyExitConnected(pk cipher.PubKey) {
	if pk.Null() {
		return
	}
	proxyConnMu.Lock()
	proxyEverConnected[pk] = true
	proxyConnMu.Unlock()
}

// isActiveAutoExit reports whether pk is the default instance's current
// auto-selected active exit (so pinned / other exits are left alone).
func isActiveAutoExit(pk cipher.PubKey) bool {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	inst := proxyReg[defaultProxyID]
	return inst != nil && inst.Auto && inst.ExitPK == pk
}

// proxyRetryCtx returns the captured auto-selection context (falling back to the
// package ctx) so retry / keepalive loops stop with the visor.
func proxyRetryCtx() context.Context {
	proxyPoolMu.Lock()
	c := proxyAutoCtx
	proxyPoolMu.Unlock()
	if c == nil {
		return ctx
	}
	return c
}

// stickyReconnect retries the SAME exit with exponential backoff (capped) after an
// established exit dropped. On recovery it returns, leaving pk the active exit
// (real fetches re-dial their own routes); after stickyMaxTries it gives up and
// rotates to a standby / new random exit. Single-flight per pk.
func stickyReconnect(pk cipher.PubKey) {
	proxyConnMu.Lock()
	if proxyStickyActive[pk] {
		proxyConnMu.Unlock()
		return
	}
	proxyStickyActive[pk] = true
	proxyConnMu.Unlock()
	defer func() {
		proxyConnMu.Lock()
		delete(proxyStickyActive, pk)
		proxyConnMu.Unlock()
	}()

	actx := proxyRetryCtx()
	win := "sticky-" + pk.Hex()[:8]
	delay := stickyBaseDelay
	for i := 0; i < stickyMaxTries; i++ {
		select {
		case <-actx.Done():
			return
		case <-time.After(delay):
		}
		if !isActiveAutoExit(pk) {
			return // pinned to another exit, or already rotated away
		}
		if _, err := skysocksSession(win, pk); err == nil {
			closeSkysocksWindow(win) // release the probe route; real fetches re-dial
			vlog(fmt.Sprintf("[skysocks-lite] exit %s recovered on retry %d — staying", exitShort(pk), i+1))
			return
		}
		if delay *= 2; delay > stickyMaxDelay {
			delay = stickyMaxDelay
		}
	}
	vlog(fmt.Sprintf("[skysocks-lite] exit %s did not recover after %d tries — rotating", exitShort(pk), stickyMaxTries))
	rotateAwayFromExit(pk)
}

// proxyKeepaliveLoop periodically yamux-pings the active default exit so a
// silently-dead route is detected proactively and routed through reportProxyExitDead
// (which, because the exit had connected, retries the same key). It also keeps the
// standby exits warm — a live route held open — so a post-sticky rotation lands on
// a known-good exit. Started once from main after boot.
func proxyKeepaliveLoop(kctx context.Context) {
	fails := map[cipher.PubKey]int{}
	for {
		select {
		case <-kctx.Done():
			return
		case <-time.After(proxyKeepalivePeriod):
		}
		// Active exit: ping (establishing a keepalive session if needed).
		if pk, ok := proxyDefaultExit(); ok {
			if pingProxyExit("keepalive-"+pk.Hex()[:8], pk) {
				fails[pk] = 0
			} else {
				fails[pk]++
				if fails[pk] >= proxyKeepaliveFailLimit {
					delete(fails, pk)
					closeSkysocksWindow("keepalive-" + pk.Hex()[:8])
					reportProxyExitDead(pk) // policy: sticky (was connected) or rotate
				}
			}
		}
		// Standbys (pool[1:]): keep a warm route so promotion is instant + vetted.
		proxyPoolMu.Lock()
		standbys := append([]cipher.PubKey(nil), proxyPool...)
		proxyPoolMu.Unlock()
		for i, spk := range standbys {
			if i == 0 || spk.Null() {
				continue
			}
			pingProxyExit("pool-warm-"+spk.Hex()[:8], spk) // best-effort warmth
		}
		// Keep the SHARED user-facing sessions (owner = defaultProxyID, the key a
		// browser fetch resolves to) HOT for every pool exit — active AND standbys.
		// The pings above use throwaway windows for liveness POLICY; those do NOT
		// keep the route a real fetch reuses alive, so without this the shared route
		// goes cold and each navigation (and every promotion) cold-dials a fresh
		// multihop route (~30-45s) instead of reusing a live one — the gap vs a
		// native skysocks-client that holds one persistent route. Ping the live
		// session to keep it warm; re-establish (best-effort) any that dropped.
		for _, spk := range standbys {
			if spk.Null() {
				continue
			}
			skysocksMu.Lock()
			s, ok := skysocksSessions[skysocksKey(defaultProxyID, spk)]
			alive := ok && s != nil && !s.IsClosed()
			skysocksMu.Unlock()
			if alive {
				sess := s
				go func() { _, _ = sess.Ping() }() //nolint:errcheck // keep the shared route warm
			} else {
				prewarmDefaultSession(spk) // re-open the shared route in the background
			}
		}
	}
}

// pingProxyExit ensures a session to pk under winID and yamux-pings it, returning
// true only if the ping round-tripped. Best-effort: any error is false.
func pingProxyExit(winID string, pk cipher.PubKey) bool {
	sess, err := skysocksSession(winID, pk)
	if err != nil {
		return false
	}
	if _, perr := sess.Ping(); perr != nil {
		return false
	}
	return true
}
