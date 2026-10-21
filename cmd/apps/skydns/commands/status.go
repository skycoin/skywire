// Package commands cmd/apps/skydns/commands/status.go
package commands

import (
	"context"
	"errors"
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
// proxy, as a browser pointed at the proxy's SOCKS5 port does. Nil when the
// visor runs no resolver, and then the status names are not answered.
func statusDialer(appCl *app.Client, log logrus.FieldLogger) meshgw.LocalDial {
	svcs, err := appCl.LocalServices()
	if err != nil || len(svcs) == 0 {
		log.WithError(err).Debug("skydns: no resolving proxy on this visor, status pages not served")
		return nil
	}
	visorPK := appCl.Config().VisorPK
	res := skysocks.NewLocalResolvers(svcs, func(port routing.Port) (net.Conn, error) {
		return appCl.Dial(appnet.Addr{Net: appnet.TypeSkynet, PubKey: visorPK, Port: port})
	})
	return resolverStatusDial(res, svcs[0].Port)
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
