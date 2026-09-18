// Package skysocks pkg/skysocks/disconnected.go c4-app-proxy
//
// Disconnected-state loopback listener. skysocks-client dials the exit BEFORE it
// binds :1080 (see cmd/apps/skysocks-client): the live Client is constructed from
// the yamux session conn, so until the dial succeeds there is no Client and nothing
// listens on :1080. During that "still connecting / exit route group never came up"
// window a browser pointed at :1080 got connection-refused — including for the
// reserved status.skysocks diagnostic host, which is exactly when a user wants to
// see why the proxy isn't connected.
//
// ServeDisconnected fills that window: it owns :1080 while the dial is in flight and
// answers each browser SOCKS5 connection LOCALLY. The reserved diagnostic hosts
// (proxystatus.Match, e.g. status.skysocks) are rendered in-process with zero exit
// involvement; every real target gets the branded "building a route" interstitial
// (plaintext HTTP) or is declined (other ports). No real traffic is ever tunneled
// here — there is no exit to tunnel to — so a listening-but-not-connected :1080
// never silently serves or blackholes real requests; it only surfaces the local
// diagnostic/interstitial pages, which never needed the exit anyway.
package skysocks

import (
	"context"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
)

// ServeDisconnected serves lis while the client has NO session to the exit yet
// (still dialing, or the exit route group never came up). Each accepted connection
// is answered locally by disconnectedConn. It returns when ctx is canceled (the
// dial succeeded and the caller is handing :1080 to the live Client, or the proc is
// shutting down) or lis fails; lis is closed on return so the port is released for
// the live listener. The caller owns lis.
func ServeDisconnected(ctx context.Context, lis net.Listener, appCl *app.Client) {
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck,gosec
	}()
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		go disconnectedConn(conn, appCl)
	}
}

// ServeDisconnectedWait owns addr for d (or until ctx is canceled), answering
// browser connections locally, then releases the port and returns.
//
// It exists to close the RECONNECT GAP — the one proxy state in which
// status.skysocks was not answered in-process at all. When every tunnel dies,
// (*Client).ListenAndServe tears the live :1080 listener down and returns, and
// the app's reconnect loop then SLEEPS (2 s doubling to 30 s) before the next
// cycle binds its disconnected listener and starts dialing. Across that sleep —
// and it is entered on every visor restart, `proxy stop`/`proxy start` and exit
// flap — nothing held :1080, so every browser request was connection-refused:
// status.skysocks included, and the branded interstitial's own auto-retry
// included. The browser then shows its "proxy server is refusing connections"
// page, which has no retry of its own, and the tab is dead until a human
// reloads. Serving the sleep instead of sleeping through it means the reserved
// diagnostic page is answerable in EVERY proxy state — starting, dialing,
// no route, route down, and now between cycles.
//
// The port is released on return, so the next cycle's listener (and ultimately
// the live Client's) rebinds cleanly; a bind failure degrades to a plain wait
// rather than skipping the delay, so it can never turn into a dial hot loop.
func ServeDisconnectedWait(ctx context.Context, addr string, appCl *app.Client, d time.Duration) {
	wctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	lis, err := ReuseListen(addr)
	if err == nil {
		ServeDisconnected(wctx, lis, appCl)
	}
	<-wctx.Done()
}

// disconnectedConn answers one browser SOCKS5 connection while disconnected from
// the exit. The reserved-host override renders the status page in-process for a
// proxystatus.Match host (regardless of port, resolved before ServeSOCKS5's
// HTTP-only gate), so status.skysocks stays reachable with no session at all; every
// other target falls through to the branded interstitial (plaintext HTTP) or is
// declined. exitReachable is nil — there is no session, so a real HTTP target gets
// the waiting interstitial rather than a fall-through reload.
func disconnectedConn(conn net.Conn, appCl *app.Client) {
	defer conn.Close() //nolint:errcheck,gosec
	override := func(host string) []byte {
		if surface, ok := proxystatus.Match(host); ok && surface == proxystatus.SurfaceSkysocks {
			return statusHTTPResponse(proxystatus.Render(disconnectedSnapshot(appCl)))
		}
		return nil
	}
	_ = proxyinterstitial.ServeSOCKS5(conn, "not connected to the exit yet", "skysocks", override, nil) //nolint:errcheck
}

// disconnectedSnapshot is the status.skysocks snapshot for the sessionless state:
// the app-RPC base (rich per-leg view when the visor has one, else the minimal
// local base) overlaid with the "no active session to the exit" note — mirroring
// the else branch of (*Client).statusSnapshot for a torn-down session.
func disconnectedSnapshot(appCl *app.Client) proxystatus.Snapshot {
	snap := baseStatusSnapshot(appCl)
	snap.Running = false
	snap.Note = "no active session to the exit"
	return snap
}
