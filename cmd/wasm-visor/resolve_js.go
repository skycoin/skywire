//go:build js && wasm

// Package main — in-tab resolver-alias resolution for fetchDmsg / the browse
// overlay, so a browser visor resolves home.dmsg + named aliases (tpd, rf,
// dmsg0, …) + <pk>.dmsg exactly like the native socks5 resolving proxy.
package main

import (
	"fmt"
	"net"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// resolverAliases maps resolver labels (tpd, rf, dmsg0, …) to PKs, built from the
// deployment service set so the in-tab browser resolves the same names as the
// socks5 resolving proxy. selfFetchPK is this visor's PK (for the home directory's
// "this visor" grouping).
var (
	resolverAliases = map[string]cipher.PubKey{}
	selfFetchPK     cipher.PubKey
)

// initResolver builds the alias→PK map from the resolved service set.
func initResolver(svc visorcore.Services, self cipher.PubKey) {
	selfFetchPK = self
	m := map[string]cipher.PubKey{}
	add := func(alias, url string) {
		if pk, err := dmsgURLPK(url); err == nil && !pk.Null() {
			m[alias] = pk
		}
	}
	add("tpd", svc.TransportDiscoveryDmsg)
	add("ar", svc.AddressResolverDmsg)
	add("rf", svc.RouteFinderDmsg)
	add("sd", svc.ServiceDiscoveryDmsg)
	add("dmsgd", svc.DmsgDiscoveryDmsg)
	add("conf", svc.ConfDmsg)
	add("ut", svc.UptimeTrackerDmsg)
	for i, ds := range svc.DmsgServers {
		var pk cipher.PubKey
		if err := pk.Set(ds.Static); err == nil && !pk.Null() {
			m[fmt.Sprintf("dmsg%d", i)] = pk
		}
	}
	resolverAliases = m
}

// resolveFetchHost maps a resolver hostname to a dmsg pk[:port] for FetchOverDmsg,
// or returns an in-process home-directory body for home.dmsg / home.skynet.
func resolveFetchHost(pkHost string) (resolved string, homeBody []byte) {
	host, port := pkHost, ""
	if h, p, err := net.SplitHostPort(pkHost); err == nil {
		host, port = h, p
	}
	if dmsgweb.IsHomeHost(host, ".dmsg") || dmsgweb.IsHomeHost(host, ".skynet") {
		return "", dmsgweb.RenderHomePage(resolverAliases, ".dmsg", selfFetchPK)
	}
	bare := strings.TrimSuffix(strings.TrimSuffix(host, ".dmsg"), ".skynet")
	if pk, ok := resolverAliases[strings.ToLower(bare)]; ok {
		resolved = pk.String()
	} else {
		resolved = bare // assume it's already a raw PK
	}
	if port != "" {
		resolved += ":" + port
	}
	return resolved, nil
}
