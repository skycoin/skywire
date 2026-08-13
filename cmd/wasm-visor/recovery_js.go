//go:build js && wasm

// Package main cmd/wasm-visor/recovery_js.go c3-vis-wasm
// dmsg self-recovery for the browser wasm-visor, mirroring the native visor's
// pkg/visor/init_selfprobe.go recovery action (dmsgC.ForceReconnect on
// persistent failure) without its self-DIAL probe. A browser tab whose dmsg
// sessions die or wedge otherwise has no recovery short of a manual reload.
//
// DESIGN — signal-driven, NOT periodic self-dial. The native visor dials its
// OWN dmsg listeners each interval to detect an inbound wedge; doing that from
// every browser tab would add fleet traffic that scales with tab count, so
// inbound-wedge detection via self-dial is intentionally out of scope here for
// bandwidth reasons. Instead we (a) watch for the local session count dropping
// to zero (a cheap ConnectedServers() read, no network) and (b) let the loops
// that already send over dmsg (uptime heartbeat, telemetry announce) feed their
// pass/fail into reportDmsgSuccess/reportDmsgFailure. Either path, once it
// persists past its threshold and the cooldown has elapsed, triggers a single
// dmsgC.ForceReconnect() — closing all sessions and nudging a discovery-entry
// refresh so the reconnect loop re-dials.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	// recoveryCheckInterval is how often the supervisor samples session health.
	recoveryCheckInterval = 60 * time.Second
	// recoveryNoSessionGrace is how long the tab may sit with zero connected
	// dmsg servers before a force-reconnect is warranted. Kept generous so a
	// normal reconnect (which the dmsg client already retries on its own) has
	// time to complete before we intervene.
	recoveryNoSessionGrace = 2 * time.Minute
	// recoveryFailThreshold is the number of consecutive failure reports from a
	// dmsg-using loop that will trigger a force-reconnect. Kept >1 so a single
	// transient send failure doesn't churn sessions.
	recoveryFailThreshold = 2
	// recoveryCooldown is the minimum wait between two recovery reconnects.
	// Prevents thrashing when the root cause is server-side and reconnecting
	// won't help — we only try once per cooldown.
	recoveryCooldown = 5 * time.Minute
)

var (
	recoveryMu             sync.Mutex
	lastForceReconnect     time.Time
	firstUnhealthyAt       time.Time
	consecutiveFailReports int
)

// startRecoverySupervisor runs a background loop that samples dmsg session
// health every recoveryCheckInterval and forces a reconnect if the tab has had
// zero connected dmsg servers for longer than recoveryNoSessionGrace (subject
// to cooldown). Runs until ctx is done.
func startRecoverySupervisor(ctx context.Context) {
	t := time.NewTicker(recoveryCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			recoveryCheckTick()
		}
	}
}

// recoveryCheckTick performs one health sample. Zero connected servers for
// longer than the grace window (and past cooldown) triggers a reconnect; any
// connected server resets the unhealthy timer.
func recoveryCheckTick() {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()

	if dmsgC == nil {
		return
	}
	now := time.Now()
	healthy := len(dmsgC.ConnectedServers()) > 0
	if healthy {
		firstUnhealthyAt = time.Time{}
		return
	}
	if firstUnhealthyAt.IsZero() {
		firstUnhealthyAt = now
		return
	}
	if now.Sub(firstUnhealthyAt) >= recoveryNoSessionGrace &&
		now.Sub(lastForceReconnect) >= recoveryCooldown {
		forceDmsgReconnect("no connected dmsg servers")
	}
}

// reportDmsgFailure is called by dmsg-using loops (uptime heartbeat, telemetry
// announce) when a send fails. After recoveryFailThreshold consecutive
// failures — and past cooldown — it forces a reconnect.
func reportDmsgFailure(reason string) {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	consecutiveFailReports++
	if consecutiveFailReports >= recoveryFailThreshold &&
		time.Since(lastForceReconnect) >= recoveryCooldown {
		forceDmsgReconnect(reason)
	}
}

// reportDmsgSuccess is called by dmsg-using loops on a successful send; it
// clears the consecutive-failure counter.
func reportDmsgSuccess() {
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	consecutiveFailReports = 0
}

// forceDmsgReconnect tears down and re-dials all dmsg sessions if the cooldown
// has elapsed. The caller MUST hold recoveryMu. No-op inside cooldown; on
// action it stamps lastForceReconnect and resets the failure counters.
func forceDmsgReconnect(reason string) {
	now := time.Now()
	if now.Sub(lastForceReconnect) < recoveryCooldown {
		return
	}
	lastForceReconnect = now
	firstUnhealthyAt = time.Time{}
	consecutiveFailReports = 0
	if dmsgC != nil {
		n := dmsgC.ForceReconnect()
		vlog(fmt.Sprintf("wasm self-recovery: forced dmsg reconnect (reason=%s, closed %d sessions)", reason, n))
	}
}
