// Package vpn pkg/vpn/meshdial_resolver.go c4-app-vpn
package vpn

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skynetweb"
)

// localServicesTTL is how long the visor's list of local services is reused.
// It changes only when a resolving proxy starts or stops.
const localServicesTTL = 30 * time.Second

// resolverPorts finds the visor's resolving proxies for the mesh dialer. A
// mesh connection that goes through one shares its routes, so only the first
// connection to a visor waits for a route. A direct app dial sets up a route
// for every connection, several seconds each, and gets no interstitial page.
type resolverPorts struct {
	appCl *app.Client
	mu    sync.Mutex
	at    time.Time
	svcs  []appnet.LocalService
}

func (r *resolverPorts) portFor(scheme string) (routing.Port, bool) {
	r.mu.Lock()
	if time.Since(r.at) > localServicesTTL {
		if svcs, err := r.appCl.LocalServices(); err == nil {
			r.svcs = svcs
		}
		r.at = time.Now()
	}
	svcs := r.svcs
	r.mu.Unlock()
	return pickResolverPort(svcs, scheme)
}

// pickResolverPort returns the port of the local service that answers for
// names in scheme ("skynet", "dmsg").
func pickResolverPort(svcs []appnet.LocalService, scheme string) (routing.Port, bool) {
	for _, s := range svcs {
		for _, suf := range s.Suffixes {
			if strings.TrimPrefix(suf, ".") == scheme {
				return s.Port, true
			}
		}
	}
	return 0, false
}

// visorServiceDialer reaches a local service on this app's own visor.
type visorServiceDialer struct {
	appCl *app.Client
	port  routing.Port
}

func (d visorServiceDialer) Dial(_, _ string) (net.Conn, error) {
	return d.appCl.Dial(appnet.Addr{Net: appnet.TypeSkynet, PubKey: d.appCl.Config().VisorPK, Port: d.port})
}

// dialViaResolver asks the resolving proxy on port for
// [<route labels>.]<label>.<scheme>:dport, so a pinned transport reaches it.
func dialViaResolver(ctx context.Context, appCl *app.Client, port routing.Port, scheme string, dest cipher.PubKey, dport uint16, route []skynetweb.RouteLabel) (net.Conn, error) {
	d, err := proxy.SOCKS5("tcp", "visor", nil, visorServiceDialer{appCl: appCl, port: port})
	if err != nil {
		return nil, err
	}
	var host strings.Builder
	for _, rl := range route {
		if rl.IsTpID {
			host.WriteString(rl.TpID.String())
		} else {
			host.WriteString(rl.PK.DNSLabel())
		}
		host.WriteByte('.')
	}
	host.WriteString(dest.DNSLabel() + "." + scheme)
	addr := net.JoinHostPort(host.String(), strconv.Itoa(int(dport)))
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, "tcp", addr)
	}
	return d.Dial("tcp", addr)
}
