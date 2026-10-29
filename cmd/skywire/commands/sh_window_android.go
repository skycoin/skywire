//go:build android && cgo

package commands

import (
	"net"
	"time"

	"github.com/0magnet/gio/app"
	_ "github.com/0magnet/gio/app/permission/foreground"   // declares the service in the APK
	_ "github.com/0magnet/gio/app/permission/networkstate" // lets the app follow the network

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Android stops an app soon after it leaves the screen, and a visor or shell
// in it with it, unless a foreground service holds the process.
func init() {
	keepRunning = func() {
		_, _ = app.StartForeground(app.ForegroundOptions{Title: "Skywire", Text: "skywire sh is running"}) //nolint:errcheck // without it the app still runs while in front
		go followNetwork()
	}
}

// networkRetry is how often an undelivered report is offered again, for
// a visor that is not up yet.
const networkRetry = 10 * time.Second

// followNetwork hands the system's network reports to the visor in this
// process: a move, which the visor cannot see from its sockets, and the
// metered flag, which puts it in its metered mode on mobile data.
func followNetwork() {
	reports := make(chan app.NetworkInfo, 1)
	if _, err := app.WatchNetwork(func(i app.NetworkInfo) {
		select {
		case old := <-reports:
			i.Moved = i.Moved || old.Moved
		default:
		}
		reports <- i
	}); err != nil {
		return
	}
	var pending *app.NetworkInfo
	tick := time.NewTicker(networkRetry)
	defer tick.Stop()
	for {
		select {
		case i := <-reports:
			if pending != nil {
				i.Moved = i.Moved || pending.Moved
			}
			pending = &i
		case <-tick.C:
		}
		if pending != nil && tellVisor(*pending) == nil {
			pending = nil
		}
	}
}

func tellVisor(i app.NetworkInfo) error {
	conn, err := net.DialTimeout("tcp", clirpc.DefaultRPCAddr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck
	ev := visorapi.NetworkEvent{Event: "changed", Interface: i.Interface, Source: "android"}
	if i.Moved {
		ev.Event = "moved"
	}
	if i.MeteredKnown {
		m := i.Metered
		ev.Metered = &m
	}
	rc := visorapi.NewRPCClient(logging.MustGetLogger("network_watch"), conn, visorapi.RPCPrefix, 10*time.Second)
	return rc.NetworkEvent(ev)
}
