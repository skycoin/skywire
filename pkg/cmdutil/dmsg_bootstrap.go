// Package cmdutil pkg/skywire-utilities/pkg/cmdutil/dmsg_bootstrap.go
package cmdutil

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// DmsgBootstrap holds the result of bootstrapping a service's DMSG connection.
type DmsgBootstrap struct {
	Client  *dmsg.Client
	DClient disc.APIClient
	Close   func()
}

// dmsgBootstrapOpts collects the optional knobs a caller can set via the
// variadic DmsgBootstrapOption arguments to BootstrapDmsg.
type dmsgBootstrapOpts struct {
	carriers []string
	strict   bool
}

// DmsgBootstrapOption customizes BootstrapDmsg. Options are additive and
// default to the previous behavior (default carrier preference, all
// advertised endpoints kept), so existing callers are unaffected.
type DmsgBootstrapOption func(*dmsgBootstrapOpts)

// WithCarriers sets the ordered dmsg CARRIER preference on the bootstrap
// client — how each server session's byte pipe is dialed (dmsg.CarrierWT /
// CarrierWS / CarrierQUIC / CarrierTCP). The first listed carrier a server
// advertises wins. Empty (the default) leaves dmsg's built-in preference
// (QUIC when advertised, else TCP).
func WithCarriers(carriers ...string) DmsgBootstrapOption {
	return func(o *dmsgBootstrapOpts) { o.carriers = carriers }
}

// WithStrictCarrier strips the TCP/QUIC fallback endpoints (Address /
// AddressV6 / AddressUDP / AddressUDPV6) from the bootstrap server entries,
// so a dial over the requested carrier cannot silently fall back to another
// transport. Paired with WithCarriers it makes "the session came up over
// carrier X" authoritative — e.g. an e2e proving dmsg-over-WebTransport
// actually rode WebTransport rather than quietly falling back to TCP.
// The browser-reachable AddressWT/AddressWS endpoints are left intact.
func WithStrictCarrier() DmsgBootstrapOption {
	return func(o *dmsgBootstrapOpts) { o.strict = true }
}

// stripFallbackAddrs returns copies of the server entries with the
// TCP/QUIC endpoints cleared so no silent carrier fallback is possible.
// The entries are copied (not mutated in place) because they may be shared
// with the caller (e.g. the embedded dmsg.Prod.DmsgServers set).
func stripFallbackAddrs(servers []*disc.Entry) []*disc.Entry {
	out := make([]*disc.Entry, 0, len(servers))
	for _, e := range servers {
		if e == nil || e.Server == nil {
			out = append(out, e)
			continue
		}
		entryCopy := *e
		srvCopy := *e.Server
		srvCopy.Address = ""
		srvCopy.AddressV6 = ""
		srvCopy.AddressUDP = ""
		srvCopy.AddressUDPV6 = ""
		entryCopy.Server = &srvCopy
		out = append(out, &entryCopy)
	}
	return out
}

// BootstrapDmsg creates a DMSG client for a service using the bootstrap priority:
//  1. Embedded deployment config (dmsg.Prod/Test servers) — no network needed
//  2. HTTP discovery fallback (only when dmsgDiscoveryDmsg is empty AND
//     dmsgDisc is set) if embedded servers are empty
//
// dmsgDiscoveryDmsg, when non-empty, switches the discovery fallback and
// the background refresh onto dmsg-HTTP — the http.Client used by the
// disc.APIClient gets its Transport wired to dmsghttp.MakeHTTPTransport,
// routing all discovery RPC over the dmsg client's sessions. No plain-
// HTTP egress occurs in this mode; dmsgDisc is ignored.
//
// After connecting, a background goroutine refreshes the server list from
// discovery: dmsg-HTTP only when dmsgDiscoveryDmsg is set, otherwise
// dmsg-first with HTTP fallback.
func BootstrapDmsg(
	ctx context.Context,
	log *logging.Logger,
	pk cipher.PubKey,
	sk cipher.SecKey,
	embeddedServers []disc.Entry,
	dmsgDisc string,
	dmsgDiscoveryDmsg string,
	dmsgServerType string,
	options ...DmsgBootstrapOption,
) (*DmsgBootstrap, error) {
	opts := &dmsgBootstrapOpts{}
	for _, o := range options {
		o(opts)
	}

	// Step 1: Use embedded servers for initial bootstrap
	var servers []*disc.Entry
	for i := range embeddedServers {
		servers = append(servers, &embeddedServers[i])
	}

	if len(servers) > 0 {
		log.Infof("Using %d embedded DMSG servers for bootstrap", len(servers))
	}

	// Step 2: If no embedded servers AND we're in plain-HTTP mode,
	// fall back to HTTP discovery to seed the initial server set.
	// In dmsg-only mode (dmsgDiscoveryDmsg set) there is no plain-HTTP
	// fallback — operators ship a non-empty embedded set or the
	// service simply has no targets to dial at boot.
	if len(servers) == 0 && dmsgDiscoveryDmsg == "" && dmsgDisc != "" {
		log.Info("No embedded DMSG servers, fetching from discovery via HTTP...")
		servers = dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, log)
	}

	if len(servers) == 0 {
		log.Warn("No DMSG servers available for bootstrap")
	}

	// Filter by server type if specified
	if dmsgServerType != "" {
		var filtered []*disc.Entry
		for _, s := range servers {
			if s.Server != nil && s.Server.ServerType == dmsgServerType {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) > 0 {
			servers = filtered
		}
	}

	// Strict carrier: drop the TCP/QUIC fallback endpoints so the session can
	// ONLY come up over the requested carrier (WithCarriers). Applied after the
	// server set is finalized (embedded seed or HTTP-discovery fetch) and before
	// the direct client / seed cache are built from it.
	if opts.strict {
		servers = stripFallbackAddrs(servers)
	}

	// Create direct client pre-loaded with the bootstrap server entries.
	// Its only job is to serve AllServers / AvailableServers from memory
	// so the dmsg client can dial the embedded set without an HTTP round
	// trip — useful when the dmsg-discovery's HTTP endpoint is briefly
	// unreachable (offline install, DNS failure, Caddy outage).
	//
	// When dmsgDiscoveryDmsg is set, also seed the dmsg-discovery's own
	// PK as a synthetic client entry delegating to every bootstrap
	// server. Without this, Entry() lookups for the dmsg-disc PK fall
	// through to dmsg-HTTP, whose own routing requires Entry() to
	// resolve the dmsg-disc PK — recursive lookup that burns CPU at
	// hundreds of calls/s once dmsg-only routing is in steady state.
	// Same trick setup-node uses (pkg/router/setupnode.go: see
	// dmsgServicePKsFromConf) and transport-setup uses
	// (pkg/transport-setup/api/api.go: dmsgServicePKs).
	keys := cipher.PubKeys{pk}
	if dmsgDiscoveryDmsg != "" {
		if discPK := PKFromDmsgURL(dmsgDiscoveryDmsg); !discPK.Null() {
			keys = append(keys, discPK)
		}
	}
	directDClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

	// Wrap the direct client with a discovery fallback so per-PK
	// Entry() lookups query the real dmsg-discovery instead of the
	// in-memory bootstrap set. Without this, dmsg.DialStream returned
	// "dmsg error 100 - entry is not found in discovery" for every PK
	// not in the embedded server list — even though direct.Client never
	// actually contacts a discovery service. AllServers / PostEntry
	// stay on the direct client so the bootstrap phase is unchanged;
	// only the Entry() path now consults real discovery.
	//
	// When dmsgDiscoveryDmsg is set, the fallback http.Client has its
	// Transport wired to dmsghttp.MakeHTTPTransport(ctx, dmsgDC) AFTER
	// dmsgDC is constructed but BEFORE Serve runs — same single-client
	// trick post-#3161 setup-node/transport-setup use to dodge the
	// dual-client self-eviction storm. The dmsg client and the http
	// client that backs its disc fallback are wired to each other.
	// When dmsgDiscoveryDmsg is empty the caller has explicitly opted
	// out of dmsg-only discovery; plain-HTTP behavior follows dmsgDisc.
	dClient := directDClient
	var dmsgHTTPC *http.Client
	switch {
	case dmsgDiscoveryDmsg != "":
		dmsgHTTPC = &http.Client{}
		httpDiscClient := disc.NewHTTP(dmsgDiscoveryDmsg, dmsgHTTPC, log)
		dClient = dmsgclient.NewFallbackDiscClient(directDClient, httpDiscClient, log)
	case dmsgDisc != "":
		httpDiscClient := disc.NewHTTP(dmsgDisc, &http.Client{Timeout: 30 * time.Second}, log)
		dClient = dmsgclient.NewFallbackDiscClient(directDClient, httpDiscClient, log)
	}

	config := &dmsg.Config{
		MinSessions:          0, // 0 = connect to all available servers
		UpdateInterval:       dmsg.DefaultUpdateInterval,
		ConnectedServersType: dmsgServerType,
	}
	// Ordered carrier preference (WithCarriers). Empty leaves dmsg's default
	// (QUIC-when-advertised, else TCP); non-empty forces e.g. WebTransport.
	if len(opts.carriers) > 0 {
		config.Carriers = opts.carriers
	}

	// Build dmsgDC inline (rather than via direct.StartDmsg) so the
	// dmsg-HTTP transport can be wired into dmsgHTTPC AFTER NewClient
	// but BEFORE Serve. The disc fallback is invoked lazily — only on
	// Entry() for PKs not in the direct seed — so wiring before Serve
	// is sufficient; any earlier-startup AvailableServers / PostEntry
	// short-circuit through the direct client.
	dmsgDC := dmsg.NewClient(pk, sk, dClient, config)
	dmsgDC.SetLogger(log)
	for _, e := range servers {
		if e == nil || e.Static.Null() || e.Server == nil {
			continue
		}
		dmsgDC.SeedEntryCache(e.Static, e)
	}
	if dmsgDiscoveryDmsg != "" {
		dmsgHTTPC.Transport = dmsghttp.MakeHTTPTransport(ctx, dmsgDC)
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgDC.Serve(ctx)
	}()
	closeDmsgDC := func() {
		if err := dmsgDC.Close(); err != nil {
			log.WithError(err).Debug("Disconnected from dmsg network.")
		}
		wg.Wait()
	}

	select {
	case <-ctx.Done():
		closeDmsgDC()
		return nil, ctx.Err()
	case <-dmsgDC.Ready():
	}

	// Background: refresh servers. dmsg-only when dmsgDiscoveryDmsg
	// is set (no plain-HTTP egress); otherwise dmsg-first with HTTP
	// fallback. Updates are PostEntry'd into the direct client so
	// AllServers stays current; the fallback wrapper delegates
	// PostEntry to direct.
	go updateServersDmsgFirst(ctx, dClient, dmsgDC, dmsgDisc, dmsgDiscoveryDmsg, dmsgServerType, log)

	return &DmsgBootstrap{
		Client:  dmsgDC,
		DClient: dClient,
		Close:   closeDmsgDC,
	}, nil
}

// updateServersDmsgFirst periodically refreshes the DMSG server list.
// dmsgDiscoveryDmsg (when non-empty) forces dmsg-HTTP — no plain-HTTP
// egress. Otherwise tries dmsg first, falls back to plain HTTP on
// dmsgDisc.
func updateServersDmsgFirst(
	ctx context.Context,
	dClient disc.APIClient,
	dmsgC *dmsg.Client,
	dmsgDisc string,
	dmsgDiscoveryDmsg string,
	dmsgServerType string,
	log *logging.Logger,
) {
	if dmsgDisc == "" && dmsgDiscoveryDmsg == "" {
		// Nothing to refresh against; the embedded seed is the
		// final word for this process. Same behavior as pre-
		// existing code when dmsgDisc was empty.
		return
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers := fetchServers(ctx, dmsgC, dmsgDisc, dmsgDiscoveryDmsg, dmsgServerType, log)
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint:errcheck,gosec
				if err := dmsgC.EnsureSession(ctx, server); err != nil {
					log.WithField("remote_pk", server.Static).WithError(err).Warn("Failed to establish session")
				}
			}
		}
	}
}

// fetchServers refreshes the dmsg-server list from discovery. dmsg-HTTP
// is the only path when dmsgDiscoveryDmsg is set; otherwise dmsg-first
// over dmsgDisc with a plain-HTTP fallback on the same URL.
func fetchServers(
	ctx context.Context,
	dmsgC *dmsg.Client,
	dmsgDisc string,
	dmsgDiscoveryDmsg string,
	dmsgServerType string,
	log *logging.Logger,
) []*disc.Entry {
	// dmsg-only mode: dmsg-HTTP exclusively. No plain-HTTP egress
	// regardless of dmsgDisc — operators have opted out of HTTP and
	// a failed refresh is preferable to a silent fallback.
	if dmsgDiscoveryDmsg != "" {
		if dmsgC == nil {
			return nil
		}
		dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		dmsgDiscClient := disc.NewHTTP(dmsgDiscoveryDmsg, dmsgHTTP, log)

		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		servers, err := dmsgDiscClient.AllServers(timeoutCtx)
		cancel()
		if err != nil {
			log.WithError(err).Debug("dmsg-HTTP server refresh failed (dmsg-only mode; no HTTP fallback)")
			return nil
		}
		log.Debugf("Refreshed %d servers via dmsg-HTTP", len(servers))
		return filterByType(servers, dmsgServerType)
	}

	// Legacy plain-HTTP-permitted mode: try dmsg first, fall back to
	// HTTP on the same URL.
	if dmsgC != nil {
		dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		dmsgDiscClient := disc.NewHTTP(dmsgDisc, dmsgHTTP, log)

		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		servers, err := dmsgDiscClient.AllServers(timeoutCtx)
		cancel()

		if err == nil && len(servers) > 0 {
			log.Debugf("Refreshed %d servers via DMSG", len(servers))
			return filterByType(servers, dmsgServerType)
		}
		log.WithError(err).Debug("DMSG server refresh failed, trying HTTP")
	}

	// Fall back to HTTP
	httpClient := disc.NewHTTP(dmsgDisc, &http.Client{Timeout: 30 * time.Second}, log)

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	servers, err := httpClient.AllServers(timeoutCtx)
	cancel()

	if err != nil {
		log.WithError(err).Error("HTTP server refresh also failed")
		return nil
	}

	log.Debugf("Refreshed %d servers via HTTP", len(servers))
	return filterByType(servers, dmsgServerType)
}

func filterByType(servers []*disc.Entry, serverType string) []*disc.Entry {
	if serverType == "" {
		return servers
	}
	var filtered []*disc.Entry
	for _, s := range servers {
		if s.Server != nil && s.Server.ServerType == serverType {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) > 0 {
		return filtered
	}
	return servers
}
