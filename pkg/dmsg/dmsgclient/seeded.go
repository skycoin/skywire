// Package dmsgclient pkg/dmsg/dmsgclient/seeded.go
package dmsgclient

import (
	"context"
	"errors"
	"net/http"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// StartDmsgSeeded starts a dmsg client seeded with explicit server entries and
// preferring the WebSocket transport — the bootstrap a browser (js/wasm) needs
// against a dmsg-only deployment where HTTP discovery isn't reachable.
//
// How it bootstraps the chicken-and-egg (the client needs discovery to find
// servers, but a browser can't reach a dmsg-only discovery until it HAS a
// server):
//  1. A direct.Client preloaded with the seedServers entries lets the dmsg
//     client connect straight to a seed server — over WebSocket, since
//     Config.PreferWS is set and the seed entry carries Server.AddressWS. No
//     HTTP discovery is consulted for this first hop.
//  2. Once the session is live, discovery is upgraded to run over dmsg itself
//     (dmsghttp transport → discDmsgAddr, a "dmsg://<disc-pk>:<port>" URL), so
//     the client can resolve arbitrary peers by PK and register its own entry
//     in the real discovery — which is what makes it inbound-reachable (peers
//     dial it by PK and a dmsg server bridges the stream back over the WS
//     session). Pass an empty discDmsgAddr to skip the upgrade (seed-only: the
//     client can be dialed/serve listeners but can't itself resolve new peers).
//
// seedServers must carry Server.AddressWS for the WebSocket dial to be chosen;
// a browser build has no working TCP/QUIC fallback.
func StartDmsgSeeded(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, seedServers []*disc.Entry, discDmsgAddr string, preferWS bool) (*dmsg.Client, func(), error) {
	if len(seedServers) == 0 {
		return nil, nil, errors.New("dmsg: no seed servers provided")
	}

	keys := cipher.PubKeys{pk}
	entries := direct.GetAllEntries(keys, seedServers)

	// Seed the discovery's OWN client entry too, delegated to the seed servers.
	// Without it, the first attempt to reach the discovery over dmsg (to register
	// our entry / resolve a peer) has to resolve the discovery's location — which
	// goes back through the dmsg-discovery → dials the discovery → resolves it →
	// recurses until the stack overflows. With the entry preloaded, resolving the
	// discovery PK short-circuits and the stream is bridged via a seed server the
	// discovery is also connected to.
	var seedServerPKs []cipher.PubKey
	for _, s := range seedServers {
		seedServerPKs = append(seedServerPKs, s.Static)
	}
	if discDmsgAddr != "" {
		if discPKHex := dmsg.ExtractPKFromDmsgAddr(discDmsgAddr); discPKHex != "" {
			var discPK cipher.PubKey
			if err := discPK.UnmarshalText([]byte(discPKHex)); err == nil {
				entries = append(entries, &disc.Entry{
					Version: "0.0.1",
					Static:  discPK,
					Client:  &disc.Client{DelegatedServers: seedServerPKs},
				})
			}
		}
	}
	dClient := direct.NewClient(entries, log)

	conf := dmsg.DefaultConfig()
	conf.PreferWS = preferWS // browser (wasm) seeds over WebSocket; native uses TCP
	conf.MinSessions = 1

	dmsgC, stop, err := direct.StartDmsg(ctx, log, pk, sk, dClient, conf)
	if err != nil {
		return nil, nil, err
	}

	if discDmsgAddr != "" {
		// Upgrade discovery to a registering fallback: READS resolve against the
		// preloaded direct client first (the seed servers + the discovery PK
		// short-circuit, so resolving the discovery's own location never recurses
		// into HTTP-over-dmsg), and only unknown peers + WRITES go to the
		// dmsg-backed HTTP client — which publishes our entry to the real
		// discovery and makes us inbound-reachable by PK. Replacing the disc
		// outright (instead of falling back) makes teardown recurse resolving the
		// discovery server's own entry.
		dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		httpDisc := disc.NewHTTP(discDmsgAddr, dmsgHTTP, log)
		dmsgC.SetDiscoveryClients([]disc.APIClient{NewRegisteringFallbackDiscClient(dClient, httpDisc, log)})
	}

	return dmsgC, stop, nil
}

// StartDmsgEmbedded starts a dmsg client seeded from the embedded production
// dmsg-server set (dmsg.Prod.DmsgServers) and with discovery over dmsg
// (deployment.Prod.DmsgDiscoveryDmsg) — a self-contained, clearnet-free dmsg
// bootstrap for native standalone tools (e.g. the reward UI) on a dmsg-only
// deployment, where the clearnet HTTP discovery is gone. preferWS=false uses TCP.
func StartDmsgEmbedded(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, preferWS bool) (*dmsg.Client, func(), error) {
	servers := dmsg.Prod.DmsgServers
	if len(servers) == 0 {
		return nil, nil, errors.New("dmsg: no embedded dmsg servers in deployment config")
	}
	seeds := make([]*disc.Entry, len(servers))
	for i := range servers {
		seeds[i] = &servers[i]
	}
	return StartDmsgSeeded(ctx, log, pk, sk, seeds, deployment.Prod.DmsgDiscoveryDmsg, preferWS)
}
