// Package visor pkg/visor/api_proxy_tunnel.go
//
// Per-tunnel teardown: close ONE of an app's tunnels and let the app's own
// standby pool replace it.
//
// `proxy mux rm` cannot do this. It drops a LEG from a route group and the
// router refuses to take the last one, which is exactly the case a tunnel is —
// so a bench that wanted one tunnel gone had to cut the underlying transport on
// the host and take every other route over it down with it. The measurement it
// produced was therefore never the measurement it asked for.
//
// The tunnel belongs to the APP, not to the visor: only the app knows which of
// its yamux sessions that route group carries, and only the app can put the
// replacement into the active set. So this is an op queued on the app's
// settings channel (pkg/app/appserver/app_settings.go) rather than a router
// call — the visor resolves the port to a real route group of that app first,
// so a typo is an error here and not silence five seconds later.
package visor

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// CutAppTunnel closes exactly one of appName's tunnels — the one whose route
// group port is rgPort, as `proxy mux info` prints it (desc.dst_port) — and
// returns the sequence of the queued op. The app applies it on its next
// settings pull (one tunnel.probe_interval, 5 s by default) and its pool
// replaces the tunnel from standby.
func (v *Visor) CutAppTunnel(appName string, rgPort uint16) (uint64, error) {
	if v.procM == nil {
		return 0, ErrProcManagerNotAvailable
	}
	if rgPort == 0 {
		return 0, fmt.Errorf("a tunnel is named by its route group port; pass --rg <port> (the dst_port 'proxy mux info' prints)")
	}
	// Resolve it against the live route groups so an unknown port is refused
	// here, with the candidate list, rather than being queued into silence.
	if _, err := v.findRouteDescForApp(appName, rgPort); err != nil {
		return 0, err
	}
	seq := v.procM.QueueAppOp(appName, appserver.AppOpCutTunnel, int64(rgPort))
	v.log.Infof("CutAppTunnel: queued cut of %s tunnel on port %d (op %d)", appName, rgPort, seq)
	return seq, nil
}
