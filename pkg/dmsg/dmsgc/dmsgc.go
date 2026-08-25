// Package dmsgc pkg/dmsg/dmsgc/dmsgc.go c1-net-dmsg
// dmsg configuration surface: the wire-format types and their JSON
// methods live in pkg/dmsg/dmsgc/spec (WASM-clean), and pkg/dmsg/dmsgc here
// aliases them so existing callers keep working unchanged while
// composing the dmsg.Client constructor and the LAN-priority
// discovery wrapper that need the heavier transitive deps.
package dmsgc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
)

// Deployment is the wire-format type for one (dmsg-discovery,
// transit-servers) pair. Aliased from pkg/dmsg/dmsgc/spec so existing
// callers writing `dmsgc.Deployment{...}` keep compiling, while the
// canonical definition lives in the WASM-clean spec package.
type Deployment = spec.Deployment

// DmsgConfig is the wire-format type for the visor-side dmsg
// subsystem configuration. Aliased from pkg/dmsg/dmsgc/spec; see
// spec.DmsgConfig for the type doc and the JSON-polymorphism notes.
type DmsgConfig = spec.DmsgConfig

// lanPriorityDisc wraps a disc.APIClient to prepend LAN server entries
// to discovery results. LAN servers are tried first, with automatic
// fallback to public servers if the LAN server is unreachable.
type lanPriorityDisc struct {
	disc.APIClient
	lanEntries []*disc.Entry
}

func (d *lanPriorityDisc) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := d.APIClient.AvailableServers(ctx)
	if err != nil {
		if len(d.lanEntries) > 0 {
			return d.lanEntries, nil
		}
		return nil, err
	}
	return append(d.lanEntries, entries...), nil
}

func (d *lanPriorityDisc) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := d.APIClient.AllServers(ctx)
	if err != nil {
		if len(d.lanEntries) > 0 {
			return d.lanEntries, nil
		}
		return nil, err
	}
	return append(d.lanEntries, entries...), nil
}

// New makes new dmsg client from configuration
// New builds the visor's SINGLE dmsg client. directClient (when non-nil)
// carries the preloaded static entries — every dmsg server + a synthetic
// dmsg-disc entry + the visor's own — so this one client connects and reaches
// dmsg-disc with no discovery round-trip, replacing the legacy second "direct"
// client (dmsgDC) that shared this PK and self-evicted on the servers
// (newest-session-wins). directOnly selects the mode: false (default) =
// "discovery" (registering fallback: direct-first reads + the visor publishes
// its entry to dmsg-disc over its own sessions); true = "direct-only" (static
// direct resolution only, no dmsg-discovery, no registration — experimental).
// The caller wires httpC.Transport = MakeHTTPTransport(ctx, dmsgC) AFTER this
// returns, so discovery HTTP rides the client's own sessions (the empty
// transport is never used before Serve).
func New(pk cipher.PubKey, sk cipher.SecKey, eb *appevent.Broadcaster, conf *DmsgConfig, httpC *http.Client, directClient disc.APIClient, directOnly bool, masterLogger *logging.MasterLogger) *dmsg.Client {
	primary := conf.Primary()
	dmsgConf := &dmsg.Config{
		MinSessions: primary.SessionsCount,
		Callbacks: &dmsg.ClientCallbacks{
			OnSessionDial: func(network, addr string) error {
				data := appevent.TCPDialData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPDial, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
				// @evanlinjin: An error is not returned here as this will cancel the session dial.
				return nil
			},
			OnSessionDisconnect: func(network, addr string, _ error) {
				data := appevent.TCPCloseData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPClose, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
			},
		},
		ConnectedServersType: primary.ConnectedServersType,
		Protocol:             primary.Protocol,
		// When this visor runs a dmsg server in-process under the same PK,
		// the client must skip its own server entry in the serve loop rather
		// than dial a transit session to itself.
		SkipSelfServer: conf.Server != nil && conf.Server.Enabled,
	}
	dmsgConf.ClientType = "visor"

	deployments := conf.AllDeployments()
	if len(deployments) == 0 {
		deployments = []Deployment{{}}
	}

	// Prefer the plain-HTTP discovery URL; in dmsg-only deployments it is
	// empty, so fall back to the dmsg:// URL — httpC's transport is dmsg-HTTP
	// over this very client (wired by the caller post-construction), so the
	// discovery lookups ride the client's own sessions.
	discURL := deployments[0].Discovery
	if discURL == "" {
		discURL = deployments[0].DiscoveryDmsg
	}
	// liveHTTPDisc is the UNWRAPPED live discovery client (queries dmsg-disc
	// over the client's own sessions). Carrier convergence resolves through it
	// so it sees a server's CURRENT AddressWT/CertHashWT/AddressUDP — the
	// primaryC below gets wrapped in a static-seed-first fallback (and the entry
	// cache is pre-seeded with ws-only config servers), which shadow those
	// rotating endpoints. See dmsgC.SetLiveDiscovery below.
	liveHTTPDisc := disc.NewHTTP(discURL, httpC, masterLogger.PackageLogger("dmsgC:disc"))
	primaryC := liveHTTPDisc
	// When the operator (or a hypervisor's auto-discovery push) has set
	// HypervisorDiscovery, wrap the public discovery as the fallback
	// behind it. The hypervisor's proxy serves locally-known PKs from
	// its embedded dmsg server's session list and forwards everything
	// else upstream — but if the hypervisor itself is unreachable, the
	// fallback ensures lookups still hit public discovery directly
	// instead of failing outright.
	if deployments[0].HypervisorDiscovery != "" {
		hypC := disc.NewHTTP(deployments[0].HypervisorDiscovery, httpC, masterLogger.PackageLogger("dmsgC:disc:hypervisor"))
		masterLogger.PackageLogger("dmsgC").
			Infof("Using hypervisor dmsg-discovery proxy as primary: %s (public fallback: %s)",
				deployments[0].HypervisorDiscovery, deployments[0].Discovery)
		primaryC = disc.NewFallbackClient(hypC, primaryC, masterLogger.PackageLogger("dmsgC:disc:fallback"))
	}
	if len(primary.LANServers) > 0 {
		masterLogger.PackageLogger("dmsgC").Infof("Using %d LAN DMSG servers (tried first)", len(primary.LANServers))
		primaryC = &lanPriorityDisc{APIClient: primaryC, lanEntries: primary.LANServers}
	}

	// Single-client convergence: resolve seeded direct entries (servers +
	// dmsg-disc + services) STATICALLY (no HTTP-over-dmsg round-trip to the
	// entry-less, root-of-trust dmsg-disc — that round-trip is what hot-loops),
	// while still registering over the client's own sessions. This makes the
	// ONE dmsgC subsume what the separate dmsgDC used to do, so no second
	// same-PK client competes for the per-server session slot.
	if directClient != nil {
		if directOnly {
			primaryC = directClient
		} else {
			primaryC = dmsgclient.NewRegisteringFallbackDiscClient(directClient, primaryC, masterLogger.PackageLogger("dmsgC:disc:fallback"))
		}
	}

	dmsgC := dmsg.NewClient(pk, sk, primaryC, dmsgConf)
	dmsgC.SetLogger(masterLogger.PackageLogger("dmsgC"))
	dmsgC.SetMasterLogger(masterLogger)
	// Give carrier convergence a live lookup that bypasses the static-seed
	// shadow (see liveHTTPDisc above).
	dmsgC.SetLiveDiscovery(liveHTTPDisc)

	// Pre-seed the entry cache with every configured server (across
	// all deployments + LAN servers). EnsureAndObtainSession resolves
	// server entries from this cache before falling through to the
	// disc client, which prevents a recursion that otherwise blows the
	// goroutine stack on shutdown / disrupted-network paths: when
	// ce.dc is the dmsgfirst wrapper, its primary is dmsghttp, and
	// dmsghttp.RoundTrip → DialStream → EnsureAndObtainSession →
	// getServerEntry → ce.dc.Entry → dmsghttp.RoundTrip … with
	// nothing to break the loop. With these entries pre-seeded, any
	// dial against a configured server short-circuits before the disc
	// client is even consulted, so the loop never starts.
	seenSeeds := make(map[cipher.PubKey]struct{})
	seedFn := func(srcLabel string, entries []*disc.Entry) {
		for _, srv := range entries {
			if srv == nil || srv.Static.Null() || srv.Server == nil {
				continue
			}
			if _, dup := seenSeeds[srv.Static]; dup {
				continue
			}
			seenSeeds[srv.Static] = struct{}{}
			dmsgC.SeedEntryCache(srv.Static, srv)
			masterLogger.PackageLogger("dmsgC").
				WithField("server_pk", srv.Static.String()).
				WithField("source", srcLabel).
				Debug("Pre-seeded dmsg server entry into cache")
		}
	}
	for i, dep := range deployments {
		seedFn(fmt.Sprintf("deployments[%d].servers", i), dep.Servers)
	}
	seedFn("primary.lan_servers", primary.LANServers)
	// Attach extra deployments so the visor publishes its client entry
	// to every configured discovery. Plain HTTP at construction time;
	// the visor's init_dmsg path can swap these for dmsgfirst-wrapped
	// clients once dmsgC has a usable session.
	for i := 1; i < len(deployments); i++ {
		dmsgC.AddDiscovery(
			disc.NewHTTP(deployments[i].Discovery, httpC, masterLogger.PackageLogger("dmsgC:disc")),
			spec.PKFromDmsgURL(deployments[i].DiscoveryDmsg),
		)
	}
	return dmsgC
}
