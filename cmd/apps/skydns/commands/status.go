// Package commands cmd/apps/skydns/commands/status.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skysocks"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// statusDialer reaches the visor's status pages through its own resolving
// proxy, as a browser pointed at the proxy's SOCKS5 port does. The resolvers
// are looked up on each page fetch: this app can start before the visor has
// published them, and asking once at start left the names unanswered.
func statusDialer(appCl *app.Client, log logrus.FieldLogger) meshgw.LocalDial {
	if appCl == nil {
		return nil
	}
	visorPK := appCl.Config().VisorPK
	return func(ctx context.Context, host string, port uint16) (net.Conn, error) {
		svcs, err := appCl.LocalServices()
		if err != nil {
			return nil, fmt.Errorf("skydns: list the visor's resolving proxies: %w", err)
		}
		if len(svcs) == 0 {
			log.Debug("skydns: no resolving proxy on this visor, status page not served")
			return nil, errors.New("skydns: this visor runs no resolving proxy")
		}
		res := skysocks.NewLocalResolvers(svcs, func(port routing.Port) (net.Conn, error) {
			return appCl.Dial(appnet.Addr{Net: appnet.TypeSkynet, PubKey: visorPK, Port: port})
		})
		return resolverStatusDial(res, svcs[0].Port)(ctx, host, port)
	}
}

// resolverStatusDial reaches host:port through the resolver that answers host,
// or through fallback for a name no resolver suffix matches.
func resolverStatusDial(res *skysocks.LocalResolvers, fallback routing.Port) meshgw.LocalDial {
	return func(ctx context.Context, host string, port uint16) (net.Conn, error) {
		// status.skywire and status.skysocks have no resolver suffix; every
		// resolver answers status.skywire, and passes status.skysocks along.
		p, ok := res.PortFor(host)
		if !ok {
			p = fallback
		}
		d, err := proxy.SOCKS5("tcp", "", nil, resolverDialer{res, p})
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("skydns: SOCKS5 dialer without DialContext")
		}
		return cd.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	}
}

// resolverDialer opens the connection to the resolver that a SOCKS5 client
// then speaks over.
type resolverDialer struct {
	res  *skysocks.LocalResolvers
	port routing.Port
}

func (d resolverDialer) Dial(string, string) (net.Conn, error) { return d.res.Dial(d.port) }
