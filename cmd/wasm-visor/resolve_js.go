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
	"github.com/skycoin/skywire/pkg/skynetweb"
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
	add("rewards", svc.RewardSystemDmsg)
	for i, ds := range svc.DmsgServers {
		var pk cipher.PubKey
		if err := pk.Set(ds.Static); err == nil && !pk.Null() {
			m[fmt.Sprintf("dmsg%d", i)] = pk
		}
	}
	// Self-loopback alias: "skywire.dmsg" → this visor's own PK, so it serves the
	// tab's own hosted "/" landing page — matching the native resolving proxy,
	// which seeds "skywire" → LocalPK (pkg/dmsgweb Aliases). Without this the wasm
	// resolver had no mapping and skywire.dmsg failed to resolve.
	if !self.Null() {
		m["skywire"] = self
	}
	resolverAliases = m
}

// resolveFetchHost maps a resolver hostname to a dmsg pk[:port] for FetchOverDmsg,
// plus the vhost (Host header) to send, or returns an in-process home-directory
// body for home.dmsg / home.skynet.
//
// For a "<vhost>.<pk>.<suffix>" host it uses the SAME parser as the native socks5
// resolving proxy (skynetweb.ParseResolverHost), so the in-tab browser resolves
// name-based virtual hosts identically — e.g.
// http://bunkerofdoom.com.<pk>.dmsg/ resolves to pk=<pk>, vhost="bunkerofdoom.com"
// rather than mis-parsing the whole label as a public key.
func resolveFetchHost(pkHost string) (resolved, vhost string, homeBody []byte) {
	host, port := pkHost, ""
	if h, p, err := net.SplitHostPort(pkHost); err == nil {
		host, port = h, p
	}
	if dmsgweb.IsHomeHost(host, ".dmsg") || dmsgweb.IsHomeHost(host, ".skynet") {
		return "", "", dmsgweb.RenderHomePage(resolverAliases, ".dmsg", selfFetchPK)
	}
	// <vhost>.<r1>…<rN>.<pk>.<suffix> — extract dest PK + vhost via the shared
	// resolver-host parser (aliases resolve too, so tpd.dmsg / skywire.dmsg work).
	lower := strings.ToLower(host)
	for _, suffix := range []string{".dmsg", ".skynet"} {
		if strings.HasSuffix(lower, suffix) {
			if vh, _, dest, _, err := skynetweb.ParseResolverHost(host, suffix, resolverAliases); err == nil {
				resolved = dest.String()
				vhost = vh
				if port != "" {
					resolved += ":" + port
				}
				return resolved, vhost, nil
			}
			break
		}
	}
	// No recognized suffix: treat the bare label as an alias or a raw PK.
	if pk, ok := resolverAliases[lower]; ok {
		resolved = pk.String()
	} else {
		resolved = host // assume it's already a raw PK
	}
	if port != "" {
		resolved += ":" + port
	}
	return resolved, "", nil
}
