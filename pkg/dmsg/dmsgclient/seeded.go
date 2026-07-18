// Package dmsgclient pkg/dmsg/dmsgclient/seeded.go c1-net-dmsg
package dmsgclient

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
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
func StartDmsgSeeded(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, seedServers []*disc.Entry, discDmsgAddr string, preferWS bool, servicePKs ...cipher.PubKey) (*dmsg.Client, func(), error) {
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
	// Preload non-registering SERVICE clients (route-finder, setup nodes, transport-
	// discovery) as direct entries delegated to the seed servers. Services run as
	// direct clients and never PUBLISH to the dmsg-discovery, so without this a tab's
	// attempt to resolve e.g. the route-finder 404s ("entry is not found in
	// discovery") and multihop DialRoutes fails at the route-finder POST. A 1-hop
	// route to a directly-transported peer skips the route-finder and so works
	// without this; a multihop route to a distant skysocks exit does not.
	for _, spk := range servicePKs {
		if spk.Null() {
			continue
		}
		entries = append(entries, &disc.Entry{
			Version: "0.0.1",
			Static:  spk,
			Client:  &disc.Client{DelegatedServers: seedServerPKs},
		})
	}
	dClient := direct.NewClient(entries, log)

	conf := dmsg.DefaultConfig()
	if preferWS {
		// Browser (wasm): prefer WebTransport, fall back to WebSocket. The seed
		// entry carries only AddressWS (a wss:// bootstrap endpoint) — there's no
		// WT cert hash to pin before discovery is reachable — so the FIRST session
		// is WS (wss, when served over HTTPS). Once discovery is up, server entries
		// carry AddressWT + CertHashWT, so subsequent sessions prefer WebTransport
		// (HTTP/3, CA-free, no redundant TLS-over-Noise) and WS is used only when
		// it must be: the bootstrap hop, or a server with no WT / a failed WT dial.
		conf.Carriers = []string{dmsg.CarrierWT, dmsg.CarrierWS}
	}
	// MinSessions 2 (not 1): once bootstrapped via the seed and registered in
	// discovery, the client learns the live server list from discovery
	// (seededDiscClient.AvailableServers → real) and holds a SECOND session, so a
	// single seed going down doesn't sever the client (and orphan it from the
	// discovery it would need to find another server). Capped at the known set.
	conf.MinSessions = 2

	// Install the registering-fallback discovery BEFORE serving — the shared
	// register-before-serve invariant (direct.StartDmsgWithSetup). The native visor
	// wires dmsg the same way (dmsg.NewClient(disc) → set transport → Serve, see
	// pkg/visor/init_dmsg.go). This path regressed by serving the bare direct client
	// first and upgrading AFTER (#3277): the entry-update loop's first tick ran
	// against the no-op direct client, recorded a "successful" update, and reset the
	// ~5-min timer, so real registration didn't fire for minutes and the tab stayed
	// 404 — which also blocks route setup (it dials the source's own @136 over dmsg,
	// needing the entry registered). Funneling through the shared helper keeps the
	// two visors from drifting on the ordering again.
	beforeServe := func(c *dmsg.Client) error {
		// Seed the deployment-service (and discovery) client entries into the
		// PERMANENT entry cache so they survive the discovery upgrade below.
		//
		// These services are dmsg DIRECT clients — they never publish to the
		// dmsg-discovery. The `entries` above are installed only in the direct
		// disc, which upgradeDiscovery then swaps for the real dmsg-discovery; a
		// subsequent getClientEntryCached lookup for e.g. service-discovery then
		// goes to the real discovery and 404s ("entry is not found in discovery"),
		// so DialStream fails. That left the wasm-visor unable to reach
		// SD/TPD/UT/AR/RF — empty network-view / services-health / uptime, broken
		// multihop route-finder POST — while a native visor seeds the same entries
		// permanently via seedDmsgServiceEntries (init_dmsg.go) and works.
		// getClientEntryCached checks this permanent cache BEFORE any discovery
		// call, so the seed short-circuits the lookup; the listed delegated servers
		// are the seed servers (which the services are also connected to), and a
		// Phase-2 mesh forward covers the case where the live session set drifts.
		seed := func(spk cipher.PubKey) {
			if spk.Null() {
				return
			}
			c.SeedEntryCache(spk, &disc.Entry{
				Version: "0.0.1",
				Static:  spk,
				Client:  &disc.Client{DelegatedServers: seedServerPKs},
			})
		}
		for _, spk := range servicePKs {
			seed(spk)
		}
		if discDmsgAddr != "" {
			if discPKHex := dmsg.ExtractPKFromDmsgAddr(discDmsgAddr); discPKHex != "" {
				var discPK cipher.PubKey
				if err := discPK.UnmarshalText([]byte(discPKHex)); err == nil {
					seed(discPK)
				}
			}
		}
		if discDmsgAddr == "" {
			return nil
		}
		// upgradeDiscovery is build-tagged: native backs the registering fallback
		// with dmsghttp (net/http over the client's own sessions); TinyGo uses the
		// net/http-free dmsgDiscClient (HTTP/1.1 over a dmsg stream). READS resolve
		// direct-first (seed servers + discovery PK short-circuit, no HTTP-over-dmsg
		// recursion); unknown peers + WRITES go to the real discovery, publishing our
		// entry so we become inbound-reachable by PK. On failure we degrade to
		// seed-only (dialable but can't resolve new peers) — never fatal.
		if err := upgradeDiscovery(ctx, log, c, dClient, seedServers, discDmsgAddr); err != nil {
			log.WithError(err).Warn("dmsg: discovery upgrade failed; continuing seed-only (dialable but can't resolve new peers)")
		}
		return nil
	}
	return direct.StartDmsgWithSetup(ctx, log, pk, sk, dClient, conf, beforeServe)
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
