//go:build js && wasm

// Package main cmd/wasm-visor/uptime_js.go c3-vis-wasm
// Uptime-heartbeat parity with the native visor: a long-lived browser tab must
// report presence to the TPD-integrated uptime tracker the same way a native
// visor does (pkg/visor/init_services.go initUptimeTracker), or it reads as
// offline. The heartbeat is a signed GET /v4/update?version=<v> against the
// TPD — issued net/http-free over dmsg through the tpdclient the tab already
// holds (tpdclient.UptimeUpdater), reusing its shared auth nonce.
//
// Browser edges are not reward-eligible today (reward eligibility is gated
// server-side, independent of this heartbeat), so sending it is safe and keeps
// the wasm visor at parity with native for testing.
package main

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
)

// startUptimeHeartbeat runs the tab's uptime-heartbeat loop until ctx is done,
// mirroring the native initUptimeTracker cadence: fire immediately (a
// (re)loaded tab registers presence in the current slot instead of forfeiting
// the first tick), then every 5 minutes, each round bounded by a 2-minute
// timeout and skipped if the previous round is still in flight (so a slow send
// never piles up goroutines or drops the next slot).
func startUptimeHeartbeat(ctx context.Context, u tpdclient.UptimeUpdater, log *logging.Logger) {
	const (
		tickDuration         = 5 * time.Minute
		heartbeatSendTimeout = 2 * time.Minute
	)

	sendOnce := func() {
		c, cancel := context.WithTimeout(ctx, heartbeatSendTimeout)
		defer cancel()
		if err := u.UpdateUptime(c, buildinfo.Version()); err != nil {
			log.WithError(err).Warn("uptime heartbeat failed")
			reportDmsgFailure("uptime heartbeat failed")
		} else {
			log.Debug("uptime heartbeat sent")
			reportDmsgSuccess()
		}
	}

	sem := make(chan struct{}, 1)
	runRound := func() {
		select {
		case sem <- struct{}{}:
			go func() {
				defer func() { <-sem }()
				sendOnce()
			}()
		default:
		}
	}

	ticker := time.NewTicker(tickDuration)
	defer ticker.Stop()
	runRound()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRound()
		}
	}
}
