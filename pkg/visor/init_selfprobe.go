// Package visor pkg/visor/init_selfprobe.go
package visor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// selfProbeInterval is how often the visor probes its own dmsg listeners.
const selfProbeInterval = 60 * time.Second

// selfProbeTimeout bounds each individual probe attempt.
const selfProbeTimeout = 10 * time.Second

// initSelfProbe starts a background loop that periodically verifies the
// visor's own dmsg listeners are reachable end-to-end. Each probe dials
// the visor's own PK through the dmsg server — exercising the full path
// that remote clients use:
//
//	visor → session → server → forward back to visor → listener → accept
//
// If a probe fails, the visor logs a warning. Future work can gate the
// discovery entry refresh on probe health so the visor stops advertising
// itself as reachable when it isn't.
func initSelfProbe(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg to be ready before starting probes.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	go selfProbeLoop(ctx, v, dmsgC, log)
	return nil
}

func selfProbeLoop(ctx context.Context, v *Visor, dmsgC *dmsg.Client, log *logging.Logger) {
	// Give listeners time to start before the first probe.
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(selfProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeResults := runSelfProbes(ctx, v, dmsgC, log)
			allHealthy := true
			for port, ok := range probeResults {
				if !ok {
					allHealthy = false
					log.WithField("port", port).Warn("Self-probe FAILED: dmsg listener unreachable")
				}
			}
			if allHealthy && len(probeResults) > 0 {
				log.WithField("ports", len(probeResults)).Debug("Self-probe: all dmsg listeners healthy")
			}
		}
	}
}

// runSelfProbes probes each critical dmsg port and returns a map of port → healthy.
func runSelfProbes(ctx context.Context, v *Visor, dmsgC *dmsg.Client, log *logging.Logger) map[uint16]bool {
	results := make(map[uint16]bool)
	myPK := dmsgC.LocalPK()

	// Probe port 136 (route-setup await port) — raw DialStream + Close.
	// The visor's router.serveSetup accepts the stream, checks
	// SetupIsTrusted (our own PK won't be in the setup-nodes list),
	// closes for untrusted — but the dial success confirms reachability.
	results[skyenv.DmsgAwaitSetupPort] = probeRawDial(ctx, dmsgC, myPK, skyenv.DmsgAwaitSetupPort)

	// Probe port 80 (dmsghttp log server) — HTTP GET /ping over dmsg.
	// Uses the visor's own dmsg client to make an HTTP request through
	// the server bridge back to itself. The /ping endpoint returns 200
	// with no body — minimal overhead.
	results[visorconfig.DmsgHTTPPort] = probeDmsgHTTP(ctx, dmsgC, myPK, log)

	return results
}

// probeRawDial does a bare DialStream + Close to confirm the listener is alive.
func probeRawDial(ctx context.Context, dmsgC *dmsg.Client, pk cipher.PubKey, port uint16) bool {
	probeCtx, cancel := context.WithTimeout(ctx, selfProbeTimeout)
	defer cancel()
	return dmsgC.Probe(probeCtx, pk, port)
}

// probeDmsgHTTP does an HTTP GET /ping over dmsg to the visor's own log server.
func probeDmsgHTTP(ctx context.Context, dmsgC *dmsg.Client, myPK cipher.PubKey, log *logging.Logger) bool {
	probeCtx, cancel := context.WithTimeout(ctx, selfProbeTimeout)
	defer cancel()

	tr := dmsghttp.MakeHTTPTransport(probeCtx, dmsgC)
	client := &http.Client{Transport: tr, Timeout: selfProbeTimeout}

	url := fmt.Sprintf("dmsg://%s:%d/ping", myPK.Hex(), visorconfig.DmsgHTTPPort)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		log.WithError(err).Debug("Self-probe HTTP: failed to create request")
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		log.WithError(err).Debug("Self-probe HTTP: request failed")
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck,gosec
	_ = resp.Body.Close()                 //nolint:errcheck,gosec
	return resp.StatusCode == http.StatusOK
}
