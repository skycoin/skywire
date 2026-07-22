// Package visor pkg/visor/init_dmsg.go c3-vis-core
// init_dmsg.go contains DMSG initialization logic.
package visor

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgctrl"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgscp"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/skyenv"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

func initDmsgHTTP(ctx context.Context, v *Visor, _ *logging.Logger) error {
	var keys cipher.PubKeys
	// Prefer entries the visor has previously learned from dmsg-discovery
	// (cached on disk in <local_path>/dmsg_servers.json) over the addresses
	// in skywire.json — those can be months stale if a server has rotated
	// IP since the config was generated. The cache is written by the
	// background refresh loop in initDmsg. ResolvedServers unions the
	// top-level Servers list with any per-discovery Configs[].Servers,
	// so multi-deployment configs (each discovery with its own disjoint
	// server set) are honored.
	configured := v.conf.Dmsg.ResolvedServers()
	if v.dmsgServersCache != nil {
		configured = v.dmsgServersCache.MergePreferringCache(configured)
	}
	log := v.MasterLogger().PackageLogger("dmsg_http")
	// --dmsg-server pins the direct dmsg client (dmsgDC) to a single
	// server. The discovery-driven dmsgC is pinned separately by
	// dmsg.Client.serve() via the "dmsgServer" context value.
	if dmsgServer != "" {
		var pinned []*dmsgdisc.Entry
		if dmsgServerAddr != "" {
			// pk@host:port form: synthesize the entry from the flag so
			// dmsg-http works on a bootstrap visor that has no cached
			// servers and possibly no working discovery.
			var pk cipher.PubKey
			if err := pk.Set(dmsgServer); err != nil {
				log.WithError(err).WithField("dmsg_server", dmsgServer).
					Error("--dmsg-server: invalid public key; dmsg-http will be unavailable")
			} else {
				pinned = []*dmsgdisc.Entry{{
					Static: pk,
					Server: &dmsgdisc.Server{Address: dmsgServerAddr},
				}}
				log.WithField("dmsg_server", dmsgServer).
					WithField("addr", dmsgServerAddr).
					Info("--dmsg-server pk@host:port: skipping discovery for dmsg-http")
			}
		} else {
			for _, e := range configured {
				if e != nil && e.Static.Hex() == dmsgServer {
					pinned = []*dmsgdisc.Entry{e}
					break
				}
			}
			if len(pinned) == 0 {
				log.WithField("dmsg_server", dmsgServer).
					Warn("--dmsg-server PK not in configured/cached servers; dmsg-http will be unavailable")
			}
		}
		configured = pinned
	}
	servers := shuffleServers(configured)

	if len(servers) == 0 {
		// No configured/cached dmsg servers (a minimal dmsg-only config on
		// first boot, before dmsg_servers.json is written). Fall back to the
		// embedded deployment set so v.dClient is NEVER left nil — mirrors
		// seedDmsgServiceEntries' dmsg.Prod.DmsgServers fallback. A nil
		// v.dClient nil-derefs getHTTPClient on dmsg:// URLs (crashes visor
		// startup) AND makes dmsgc.New fall through to a plain-HTTP disc with
		// no seeded server entries, whose first resolve recurses
		// resolve->RoundTrip->resolve into a goroutine-stack overflow.
		servers = shuffleServers(deployment.Prod.ToDiscEntries())
		if len(servers) == 0 {
			// Truly nothing embedded (e.g. an empty-keyring build); leaving
			// dClient nil is unavoidable, but downstream nil-guards must hold.
			return nil
		}
		log.WithField("count", len(servers)).
			Warn("no configured/cached dmsg servers; seeding direct client from embedded deployment set")
	}

	keys = append(keys, v.conf.PK)
	// Add deployment service PKs so the direct client can look them up
	// without querying the HTTP discovery (services run as direct clients
	// and don't register in discovery). GetAllEntries creates a synthetic
	// client entry for each PK with all servers as delegated servers.
	keys = append(keys, v.dmsgServicePKs()...)
	entries := direct.GetAllEntries(keys, servers)
	dClient := direct.NewClient(entries, v.MasterLogger().PackageLogger("dmsg_http:direct_client"))

	// Set dClient immediately for direct discovery access.
	v.initLock.Lock()
	v.dClient = dClient
	v.initLock.Unlock()

	// SINGLE-CLIENT CONVERGENCE: do NOT spin up a separate "direct" dmsg
	// client (dmsgDC) here. It shared the visor PK with the main dmsgC, and
	// dmsg servers key sessions by client-PK with newest-session-wins
	// eviction (#3035), so the two clients evicted each other on every shared
	// server — a continuous self-eviction storm (server-confirmed ~56
	// session-deaths/min/server, 100% from visors). initDmsg now builds ONE
	// dmsgC whose disc is a registering fallback over this dClient (static
	// direct-first reads + self-registration), and wires v.dmsgHTTP to a
	// dmsg-HTTP transport over that same client. dClient (set above) carries
	// the preloaded static entries the single client resolves against.
	return nil
}

func shuffleServers(in []*dmsgdisc.Entry) []*dmsgdisc.Entry {
	n := len(in)
	for i := n - 1; i > 0; i-- {
		jBig, err := crand.Int(crand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		in[i], in[j] = in[j], in[i]
	}
	return in
}

/*
func rotateServers(servers []*dmsgdisc.Entry) {
	if len(servers) == 0 {
		return
	}
	first := servers[0]
	copy(servers, servers[1:])
	servers[len(servers)-1] = first
}
*/

func initDmsg(ctx context.Context, v *Visor, log *logging.Logger) (err error) {
	if v.conf.Dmsg == nil {
		return fmt.Errorf("cannot initialize dmsg: empty configuration")
	}

	// Pick the primary deployment for the discovery dial choice. The
	// top-level Discovery / DiscoveryDmsg mirror Deployments[0] after
	// UnmarshalJSON, so single-deployment configs work without
	// changes; multi-deployment configs surface deployments[0] here
	// and the rest are attached inside dmsgc.New.
	primary := v.conf.Dmsg.Discovery
	primaryDmsg := v.conf.Dmsg.DiscoveryDmsg

	// Prefer DMSG-HTTP for discovery if configured (more private, no DNS dependency),
	// fall back to plain HTTP URL. If HTTP URL is empty (DMSG-only deployment),
	// DMSG is required — not optional.
	_ = primary // discovery URL selection now lives in dmsgc.New (Discovery || DiscoveryDmsg)
	_ = primaryDmsg
	dmsgConf := *v.conf.Dmsg
	// --dmsg-server pins the client to one server (discovery filter in
	// dmsg.Client.serve). Force sessions_count=1 so the client doesn't
	// burn retries trying to open additional sessions when only one
	// server is reachable. Deep-copy Deployments so the override stays
	// local to this dmsgConf and doesn't leak into v.conf.Dmsg via the
	// shared slice header.
	if dmsgServer != "" {
		if len(dmsgConf.Deployments) > 0 {
			deps := make([]dmsgc.Deployment, len(dmsgConf.Deployments))
			copy(deps, dmsgConf.Deployments)
			deps[0].SessionsCount = 1
			dmsgConf.Deployments = deps
		}
		dmsgConf.SessionsCount = 1
		log.WithField("dmsg_server", dmsgServer).
			Info("--dmsg-server set: forcing dmsg.sessions_count=1")
	}

	// SINGLE-CLIENT CONVERGENCE. One dmsg client with a deferred dmsg-HTTP
	// transport: build the client with an empty http.Client, then wire its
	// Transport to MakeHTTPTransport(dmsgC) so discovery lookups AND the
	// visor's own registration ride this client's own sessions. No request
	// is issued before Serve, so the empty transport is never used. dmsgc.New
	// wraps the disc in a registering fallback over v.dClient (preloaded with
	// servers + dmsg-disc + service entries) so those resolve statically with
	// no HTTP-over-dmsg round-trip — eliminating the second same-PK client
	// (dmsgDC) that self-evicted on the dmsg servers.
	httpC := &http.Client{}
	directOnly := v.conf.Dmsg != nil && v.conf.Dmsg.DirectOnly
	dmsgC := dmsgc.New(v.conf.PK, v.conf.SK, v.ebc, &dmsgConf, httpC, v.dClient, directOnly, v.MasterLogger())
	httpC.Transport = dmsghttp.MakeHTTPTransport(ctx, dmsgC)

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgC.Serve(ctx)
	}()

	v.pushCloseStack("dmsg", func() error {
		if err := dmsgC.Close(); err != nil {
			return err
		}
		wg.Wait()
		return nil
	})

	v.initLock.Lock()
	v.dmsgC = dmsgC
	v.dmsgDC = dmsgC // single client: dmsgDC consumers (router, resolver, vpn) ride dmsgC
	v.dmsgHTTP = httpC
	v.initLock.Unlock()
	select {
	case <-v.dmsgHTTPReady:
	default:
		close(v.dmsgHTTPReady)
	}

	// Wait for DMSG to connect before returning. All modules that depend on
	// dmsg will only start after this, ensuring DMSG is ready before any
	// service tries to use it. Without this, services start dialing over DMSG
	// before sessions are established, causing unnecessary HTTP fallbacks.
	//
	// Two readiness regimes:
	//   - Unpinned (default): wait up to dmsgInitTimeout and continue
	//     either way; services fall back to HTTP if dmsg is slow.
	//   - Pinned (--dmsg-server): there's exactly one server we can
	//     reach, so a timeout doesn't mean "be patient" — it means
	//     "give up." Poll the dmsg client's pinned-failure counter
	//     instead and abort startup once it exceeds the configured
	//     attempt cap. Each failed pass already costs at least one
	//     backoff (5s → 60s), so 5 attempts is on the order of
	//     2–3 minutes before shutdown.
	const dmsgInitTimeout = 30 * time.Second
	if dmsgServer != "" {
		maxAttempts := dmsgServerMaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}
		log.WithField("dmsg_server", dmsgServer).
			WithField("max_attempts", maxAttempts).
			Info("--dmsg-server set: waiting for pinned server (will abort after max attempts)")
		ticker := time.NewTicker(1 * time.Second)
	pinnedWait:
		for {
			select {
			case <-dmsgC.Ready():
				log.Info("DMSG client connected and ready.")
				break pinnedWait
			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			case <-ticker.C:
				if attempts := dmsgC.PinnedFailureCount(); attempts >= int64(maxAttempts) {
					ticker.Stop()
					log.WithField("dmsg_server", dmsgServer).
						WithField("attempts", attempts).
						WithField("max_attempts", maxAttempts).
						Error("--dmsg-server pinned but unreachable; aborting startup")
					return fmt.Errorf("dmsg server %s (from --dmsg-server) unreachable after %d attempts", dmsgServer, attempts)
				}
			}
		}
		ticker.Stop()
	} else {
		select {
		case <-dmsgC.Ready():
			log.Info("DMSG client connected and ready.")
		case <-time.After(dmsgInitTimeout):
			log.Warn("DMSG client not ready after timeout, continuing (services may fall back to HTTP)")
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Seed the DMSG client's entry cache with deployment service PKs
	// FIRST, before swapping in the dmsg-only disc client below. The
	// dmsg-discovery's own PK is in this list, and the dmsg-only disc
	// client's dmsg-HTTP transport needs to DialStream to that PK —
	// without the seed already in place, the moment dmsgC.SetDiscoveryClients
	// installs it, any background AvailableServers / PutEntry refresh that
	// dmsgC kicks off recurses (DialStream → cache miss → disc →
	// dmsg-HTTP → DialStream(dmsgdiscPK) → cache miss → ...) until the
	// goroutine stack overflows.
	v.seedDmsgServiceEntries(dmsgC, log)

	// No disc upgrade needed here: dmsgc.New already wired dmsgC's disc to a
	// registering client whose HTTP half rides this single client's own
	// dmsg-HTTP transport (httpC, set above). The one client both registers
	// itself over dmsg and resolves seeded static entries direct-first.

	// Start periodic config refresh for dynamic key sets
	go v.startConfigRefresh(ctx) //nolint:errcheck,gosec

	// Refresh the on-disk dmsg-servers cache from dmsg-discovery so the next
	// bootstrap uses live addresses. Rides httpC (the single client's
	// dmsg-HTTP transport).
	if v.dmsgServersCache != nil {
		refreshURL := v.conf.Dmsg.Discovery
		if refreshURL == "" {
			refreshURL = v.conf.Dmsg.DiscoveryDmsg
		}
		if refreshURL != "" {
			go v.refreshDmsgServersCacheLoop(ctx, refreshURL, httpC)
		}
	}

	return nil
}

// dmsgServersCacheRefreshInterval is how often the cache is refreshed
// from dmsg-discovery once dmsgC is up. 5m is short enough that the
// cache converges on the live state within a few minutes of a server
// rotating its address, long enough that the refresh isn't a load
// concern on dmsgd.
const dmsgServersCacheRefreshInterval = 5 * time.Minute

// refreshDmsgServersCacheLoop runs until ctx is canceled, refreshing
// v.dmsgServersCache from dmsg-discovery's all_servers endpoint at a
// fixed interval. Empty refreshes (dmsgd unreachable, no entries) are
// skipped — DmsgServersCache.Replace treats empty input as a no-op so
// a transient outage doesn't wipe the cache.
// upgradeDmsgDiscToDmsgOnly swaps each of dmsgC's per-deployment disc.APIClient
// instances for one that registers + resolves the discovery STRICTLY over dmsg
// (dmsg-HTTP through primaryDmsgC), with NO plain-HTTP fallback. The
// dmsg-discovery's PK is extracted from each deployment's `discovery_dmsg` URL;
// when that's absent the entry is left on the explicit plain-HTTP Discovery URL
// (a legacy / non-dmsg config with no dmsg-disc PK to dial). A no-op if no
// deployment yields a non-zero PK, or if primaryDmsgC is nil.
//
// The plain-HTTP RUNTIME fallback was removed here (was: dmsgfirst, which tried
// dmsg first and retried on plain HTTP when the dmsg dial failed). The
// deployment discovery services are no longer served over clear HTTP, so a
// fallback on a transient dmsg failure only added a doomed roundtrip + a WARN
// per refresh. A transient dmsg error now simply lets the caller retry over
// dmsg on its next periodic discovery refresh / re-registration, which
// self-heals once sessions realign. (The pk-absent branch above is NOT that
// fallback: it is the explicit "operator configured no dmsg discovery" path.)
//
// primaryDmsgC is the dmsg client used for the dmsg-HTTP transport. It must be
// the direct.Client-backed dmsgDC, not the main dmsgC — the main dmsgC's own
// discovery is what we're upgrading here, so using it would recurse on every
// Entry() lookup. dmsgDC carries the dmsg-disc PK as a synthetic direct.Client
// entry with all known server PKs as delegated, so its DialStream(dmsg-disc)
// resolves locally and goes through a session dmsg-disc actually has (it
// preloads the same server set via direct.StartDmsg in dmsgdisc.go).
func upgradeDmsgDiscToDmsgOnly(dmsgC *dmsg.Client, primaryDmsgC *dmsg.Client, conf *dmsgc.DmsgConfig, log *logging.Logger) {
	if conf == nil || primaryDmsgC == nil {
		return
	}
	deployments := conf.AllDeployments()
	if len(deployments) == 0 {
		return
	}
	dmsgHTTPClient := &http.Client{Transport: dmsghttp.MakeHTTPTransport(context.Background(), primaryDmsgC)}
	upgraded := make([]dmsgdisc.APIClient, len(deployments))
	anyUpgraded := false
	for i, d := range deployments {
		pk := cmdutil.PKFromDmsgURL(d.DiscoveryDmsg)
		if pk == (cipher.PubKey{}) {
			upgraded[i] = dmsgdisc.NewHTTP(d.Discovery, &http.Client{}, log)
			continue
		}
		dmsgURL := fmt.Sprintf("http://%s:%d", pk.Hex(), dmsg.DefaultDmsgHTTPPort)
		upgraded[i] = dmsgdisc.NewHTTP(dmsgURL, dmsgHTTPClient, log)
		anyUpgraded = true
		log.WithField("pk", pk).Info("dmsg discovery client upgraded to dmsg-only (no HTTP fallback)")
	}
	if !anyUpgraded {
		return
	}
	dmsgC.SetDiscoveryClients(upgraded)
}

func (v *Visor) refreshDmsgServersCacheLoop(ctx context.Context, discURL string, httpC *http.Client) {
	cacheLog := v.MasterLogger().PackageLogger("dmsg_servers_cache")
	dc := dmsgdisc.NewHTTP(discURL, httpC, cacheLog)
	refresh := func() {
		entries, err := dc.AllServers(ctx)
		if err != nil {
			cacheLog.WithError(err).Debug("Skipping cache refresh (dmsgd error)")
			return
		}
		if err := v.dmsgServersCache.Replace(entries); err != nil {
			cacheLog.WithError(err).Warn("Failed to write dmsg-servers cache file")
			return
		}
		cacheLog.WithField("count", len(entries)).Debug("dmsg-servers cache refreshed")
	}
	refresh()
	t := time.NewTicker(dmsgServersCacheRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

// dmsgServicePKs extracts public keys from dmsg:// URLs in the visor config.
// Falls back to embedded deployment defaults for each missing field so a
// minimal config (which is the common case — most operators only override
// the few fields they need) still seeds every direct-client deployment
// service. Without these fallbacks all but ConfDmsg dropped to "" silently,
// so DialStream for e.g. TPD hit the HTTP discovery, found no entry (TPD
// is direct-client by design), and bailed with "entry not found".
func (v *Visor) dmsgServicePKs() cipher.PubKeys {
	// Resolve the deployment service endpoints through the SHARED resolver
	// (pkg/visor/visorcore) — the same one the browser wasm-visor uses — so the two
	// visors can't drift on the operator-override-else-deployment-default rule. This
	// replaced six inline pick() calls; ResolveServices applies identical semantics
	// (and handles the nullable UptimeTracker/Launcher blocks).
	svc := visorcore.ResolveServices(v.conf)
	dmsgURLs := []string{
		svc.DmsgDiscoveryDmsg,
		svc.TransportDiscoveryDmsg,
		svc.AddressResolverDmsg,
		svc.RouteFinderDmsg,
		svc.ServiceDiscoveryDmsg,
		svc.ConfDmsg,
		svc.UptimeTrackerDmsg,
	}
	var pks cipher.PubKeys
	for _, rawURL := range dmsgURLs {
		if rawURL == "" {
			continue
		}
		var addr dmsg.Addr
		trimmed := rawURL
		if len(trimmed) > 7 && trimmed[:7] == "dmsg://" {
			trimmed = trimmed[7:]
		}
		if err := addr.Set(trimmed); err != nil {
			continue
		}
		pks = append(pks, addr.PK)
	}
	return pks
}

// seedDmsgServiceEntries injects synthetic client entries for deployment
// services into v.dmsgC's entry cache. These services run as direct DMSG
// clients (they don't register in the HTTP discovery), so without seeding
// the cache DialStream's discovery lookup fails with "entry not found".
//
// The synthetic entries list ALL known DMSG server PKs as delegated servers.
// This lets DialStream try each server the visor is connected to — one of
// them will be able to forward the stream to the service.
//
// Server PK list: prefer the visor config's explicit dmsg.servers list,
// fall back to the embedded prod set (dmsg.Prod.DmsgServers) when the
// config has none. Most minimal configs leave dmsg.servers empty and
// rely on discovery to populate sessions at runtime; before this
// fallback the seed returned early in that case and the service-PK
// entries were never installed, so direct-client services like TPD
// remained unreachable via DialStream until a manual seed.
func (v *Visor) seedDmsgServiceEntries(dmsgC *dmsg.Client, log *logging.Logger) {
	var serverPKs []cipher.PubKey
	for _, srv := range v.conf.Dmsg.Servers {
		serverPKs = append(serverPKs, srv.Static)
	}
	if len(serverPKs) == 0 {
		for _, srv := range dmsg.Prod.DmsgServers {
			serverPKs = append(serverPKs, srv.Static)
		}
	}
	if len(serverPKs) == 0 {
		return
	}
	pks := v.dmsgServicePKs()
	for _, pk := range pks {
		dmsgC.SeedEntryCache(pk, &dmsgdisc.Entry{
			Static: pk,
			Client: &dmsgdisc.Client{DelegatedServers: serverPKs},
		})
	}
	if len(pks) > 0 {
		log.WithField("count", len(pks)).Info("Seeded DMSG entry cache with deployment service PKs")
	}
}

func initDmsgCtrl(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// DMSG should already be ready (initDmsg waits for it).
	// Initialize the transport manager's DMSG client.
	logger := dmsgC.Logger()
	logger.Debug("Initializing DMSG transport client...")
	v.tpM.InitDmsgClient(ctx, dmsgC)

	// WebRTC signals over dmsg, so its client is started only now that the manager
	// has the dmsg client. InitClient also starts the dmsg signaling listener, so
	// the visor ACCEPTS WebRTC dials (e.g. from browser visors). On by default,
	// like stcpr/sudph — a visor accepts every transport type it can.
	logger.Info("Initializing WebRTC transport client...")
	v.tpM.InitClient(ctx, tptypes.WEBRTC, 0)

	// dmsgctrl setup — listen for incoming control streams (ping/pong).
	// Each accepted Control is self-serving (handles ping/pong in its own goroutine).
	// We drain the channel so the listener doesn't block on a full buffer.
	cl, err := dmsgC.Listen(skyenv.DmsgCtrlPort)
	if err != nil {
		return err
	}
	v.pushCloseStack("dmsgctrl", cl.Close)

	ctrlCh := dmsgctrl.ServeListener(cl, 16)
	go func() {
		for ctrl := range ctrlCh {
			// Each control is already self-serving via ctrl.serve().
			// We just hold a reference so the GC doesn't collect it prematurely.
			_ = ctrl
		}
	}()

	// Mirror on skynet at the same port. dmsgctrl.ServeListener
	// accepts any net.Listener, so the appnet listener slots in
	// directly and the resulting Controls flow through the same
	// drain goroutine pattern.
	goServeSkynetMirror(ctx, v.conf.PK, skyenv.DmsgCtrlPort, "dmsgctrl", logger,
		func(lis net.Listener) {
			ch := dmsgctrl.ServeListener(lis, 16)
			go func() {
				<-ctx.Done()
				if err := lis.Close(); err != nil {
					logger.WithError(err).Debug("Failed to close skynet dmsgctrl listener")
				}
			}()
			for ctrl := range ch {
				_ = ctrl
			}
		})
	return nil
}

func initDmsgHTTPLogServer(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return fmt.Errorf("cannot initialize dmsg log server: dmsg not configured")
	}
	logger := v.MasterLogger().PackageLogger("dmsghttp_logserver")

	var printLog bool
	if v.MasterLogger().GetLevel() == logrus.DebugLevel || v.MasterLogger().GetLevel() == logrus.TraceLevel {
		printLog = true
	}

	//whitelist access to the surveys for the hypervisor, dmsggpty whitelist, and for the surveywhitelist of keys which is fetched from the conf service
	// The visor's own PK is always whitelisted — it should have full
	// access to its own log server, surveys, and pprof.
	whitelistedPKs := []cipher.PubKey{v.conf.PK}
	if sw := v.conf.EffectiveSurveyWhitelist(); sw != nil {
		whitelistedPKs = append(whitelistedPKs, sw...)
	}
	if v.conf.Hypervisors != nil {
		whitelistedPKs = append(whitelistedPKs, v.conf.Hypervisors...)
	}
	if v.conf.Pty != nil {
		if v.conf.Pty.Whitelist != nil {
			whitelistedPKs = append(whitelistedPKs, v.conf.Pty.Whitelist...)
		}
	}

	lsAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, "", whitelistedPKs, &v.survey.data, printLog)

	// Set visor as health stats provider for /health endpoint
	lsAPI.SetHealthStatsProvider(v)
	// Self-identify on /health and the landing page: PK + dmsg listen
	// address (the log server's own dmsg port, where this surface is served).
	dmsgAddr := fmt.Sprintf("%s:%d", v.conf.PK.Hex(), visorconfig.DmsgHTTPPort)
	lsAPI.SetIdentity(v.conf.PK.Hex(), dmsgAddr)

	// Store the log server API reference for public autocheck to use later
	v.initLock.Lock()
	v.logServer.api = lsAPI
	v.initLock.Unlock()

	// Register the log server handler so the sky-forwarding server
	// can dispatch skynet connections to it directly (no localhost
	// TCP bounce). Uses the SAME handler (lsAPI) as the DMSG HTTP
	// server — a request arriving via skynet is served identically
	// to one arriving via DMSG.
	v.services.Register(visorconfig.DmsgHTTPPort, "log_server", HTTPHandler(lsAPI))
	logger.WithField("port", visorconfig.DmsgHTTPPort).Info("Registered log server in service registry")

	// Wire the service catalog so /services on the log server shows
	// what ports are available for skynet forwarding.
	lsAPI.SetServiceLister(v.services)
	lsAPI.SetForwardedPortLister(v.forwardedPorts)
	// Related nodes (managed visors + configured hypervisors) — shown as
	// links to authenticated callers on the landing page.
	lsAPI.SetRelatedNodesProvider(v)

	// Mount the dmsgpty web terminal at /pty, gated by the same
	// whitelist the dmsgpty Host enforces on direct connections —
	// configured Dmsgpty.Whitelist + Hypervisors + the visor's own
	// PK (a hypervisor running locally can reach itself). The
	// dialer prefers the local CLI socket when it's set up; that
	// avoids a self-loop through dmsg for a request that's already
	// in-process. When no CLI socket is configured we fall back to
	// dialing our own dmsg client at DmsgPtyPort, which works the
	// same way the hypervisor's per-PK ptyUI does for remote visors.
	if v.conf.Pty != nil {
		var ptyDialer pty.UIDialer
		if v.conf.Pty.CLINet != "" {
			ptyDialer = pty.NetUIDialer(v.conf.Pty.CLINet, v.conf.Pty.CLIAddr)
		} else {
			ptyDialer = pty.DmsgUIDialer(dmsgC, dmsg.Addr{PK: v.conf.PK, Port: skyenv.DmsgPtyPort})
		}
		ptyUI := pty.NewUI(ptyDialer, pty.DefaultUIConfig())
		ptyHandler := ptyUI.Handler(map[string][]string{
			"update": visorconfig.UpdateCommand(),
		})
		// Gate /pty on the shared peer whitelist (own PK + hypervisors
		// + configured pty whitelist, plus any runtime transitive
		// additions) so the web terminal honors the same live set as
		// the dmsgpty host.
		lsAPI.SetPtyHandler(ptyHandler, v.peerWhitelist)
		logger.Info("Mounted /pty on logserver (gated by shared peer whitelist)")
	}

	// Mount the website handler for port 80 — rewards UI if configured,
	// otherwise the forwarded-port reverse proxy if one is registered.
	v.refreshWebsiteHandler(logger)

	lis, err := dmsgC.Listen(visorconfig.DmsgHTTPPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			logger.WithError(err).Error("Failed to close DMSG HTTP listener")
		}
	}()

	logger.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Debug("Serving...")
	// Increased timeouts for dmsg latency characteristics
	// DMSG has higher latency than direct TCP due to multi-hop routing
	srv := &http.Server{
		ReadTimeout:       skyenv.HTTPReadTimeout,
		WriteTimeout:      skyenv.HTTPWriteTimeout,
		IdleTimeout:       skyenv.HTTPIdleTimeout,
		ReadHeaderTimeout: skyenv.HTTPReadHeaderTimeout,
		Handler:           lsAPI,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		err = srv.Serve(lis)
		if errors.Is(err, dmsg.ErrEntityClosed) {
			return
		}
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		if err != nil {
			logger.WithError(err).Error("Logserver exited with error.")
		}
	}()

	// Skynet mirror of the HTTP log server at the same port. Same
	// http.Server (and therefore same handler, timeouts, and graceful
	// shutdown) — only the listener differs. Operators dialing the
	// log server over skynet hit the identical surface as dmsg.
	goServeSkynetMirror(ctx, v.conf.PK, visorconfig.DmsgHTTPPort, "dmsg_http", logger,
		func(skyLis net.Listener) {
			if err := srv.Serve(skyLis); err != nil &&
				!errors.Is(err, http.ErrServerClosed) &&
				!errors.Is(err, dmsg.ErrEntityClosed) &&
				!errors.Is(err, net.ErrClosed) {
				logger.WithError(err).Debug("Skynet logserver exited")
			}
		})

	v.pushCloseStack("dmsghttp.logserver", func() error {
		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.WithError(err).Warn("HTTP server shutdown error")
		}
		wg.Wait()
		return nil
	})

	// Also serve on localhost so the skynet forwarding server can
	// reach /health and other endpoints. When LogServer.LocalAddr
	// is configured, use that; otherwise auto-bind on :0 (OS-
	// assigned port) so every visor gets a localhost listener for
	// skynet forwarding without manual config.
	localAddr := ""
	if v.conf.LogServer != nil && v.conf.LogServer.LocalAddr != "" {
		localAddr = v.conf.LogServer.LocalAddr
	} else {
		localAddr = "localhost:0" // auto-assign
	}
	if localAddr != "" {
		logger.WithField("local_addr", localAddr).Info("Starting localhost log server")

		// Create a separate API without whitelist authentication for localhost
		localAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, "", nil, &v.survey.data, printLog)

		// Set visor as health stats provider for /health endpoint
		localAPI.SetHealthStatsProvider(v)
		localAPI.SetIdentity(v.conf.PK.Hex(), dmsgAddr)

		// Store the localhost API for potential future use
		v.logServer.localAPI = localAPI

		localLis, err := net.Listen("tcp", localAddr)
		if err != nil {
			logger.WithError(err).WithField("local_addr", localAddr).Warn("Failed to start localhost log server")
		} else {
			// Capture the actual bound address (important when
			// localAddr was ":0" for auto-assignment).
			boundAddr := localLis.Addr().String()
			logger.WithField("bound_addr", boundAddr).Info("Localhost log server bound")

			// Register the port for skynet forwarding so
			// .skynet URLs can reach /health, /ping, etc.
			if _, portStr, splitErr := net.SplitHostPort(boundAddr); splitErr == nil {
				if port, convErr := strconv.Atoi(portStr); convErr == nil && port > 0 {
					v.allowed.mu.Lock()
					v.allowed.ports[port] = true
					v.allowed.mu.Unlock()
					logger.WithField("port", port).Info("Log server port registered for skynet forwarding")
				}
			}

			localSrv := &http.Server{
				ReadTimeout:       5 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       60 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				Handler:           localAPI,
			}

			localWg := new(sync.WaitGroup)
			localWg.Add(1)

			go func() {
				defer localWg.Done()
				if err := localSrv.Serve(localLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.WithError(err).Error("Localhost logserver exited with error")
				}
			}()

			v.pushCloseStack("localhost.logserver", func() error {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := localSrv.Shutdown(shutdownCtx); err != nil {
					logger.WithError(err).Warn("Localhost HTTP server shutdown error")
				}
				localWg.Wait()
				return nil
			})

			logger.WithField("local_addr", localAddr).Info("Localhost log server started")
		}
	}

	return nil
}

func initDmsgTrackers(_ context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC

	dtm := dmsgtracker.NewDmsgTrackerManager(v.MasterLogger(), dmsgC, 0, 0)
	v.pushCloseStack("dmsg_tracker_manager", func() error {
		return dtm.Close()
	})
	v.initLock.Lock()
	v.dmsgTracker.manager = dtm
	v.initLock.Unlock()
	v.dmsgTracker.readyOnce.Do(func() { close(v.dmsgTracker.ready) })
	return nil
}

// nolint: gocyclo
//
//gocyclo:ignore
func initDmsgpty(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Pty

	if conf == nil {
		log.Debug("'dmsgpty' is not configured, skipping.")
		return nil
	}

	// Unlink dmsg socket files (just in case).
	if conf.CLINet == "unix" {
		if runtime.GOOS == "windows" {
			conf.CLIAddr = pty.ParseWindowsEnv(conf.CLIAddr)
		}

		if err := osutil.UnlinkSocketFiles(v.conf.Pty.CLIAddr); err != nil {
			log.WithError(err).Errorf("Insufficient permissions to unlink socket file %q", v.conf.Pty.CLIAddr)
			return err
		}
	}

	// Use the visor's shared peer whitelist as the dmsgpty host's
	// whitelist. It's seeded with the configured pty whitelist +
	// hypervisors + the visor's own PK, and is the same reference
	// dmsgscp and the /pty web terminal consult — so a runtime
	// transitive addition (a connected hypervisor pushing its own
	// hypervisors via AddPtyWhitelist) reaches all of them at once.
	wl := v.peerWhitelist

	dmsgC := v.dmsgC
	if dmsgC == nil {
		err := errors.New("cannot create dmsgpty with nil dmsg client")
		return err
	}

	// Route the dmsgpty Host's OUTBOUND proxy dial through the
	// MultiDialer chain built in init_dmsg_skywire.go. Strategy
	// order is skywire-first / dmsg-fallback so `cli dmsg pty
	// start` rides the visor's already-negotiated transports when
	// a route exists, then falls through to dmsg on miss. Adding
	// the chain here rather than at NewHost preserves backward
	// compat for every other NewHost caller (cmd/dmsg/pty-host,
	// sshd CLI, tests).
	host := pty.NewHostWithDialer(dmsgC, wl, buildDmsgptyDialer(dmsgC, v.ensureFastTransport))
	// Opt-in persistent pty sessions: an interactive shell survives a dropped
	// stream and a reconnecting client (the web terminal / `cli pty`) reattaches
	// by id. Default TTL (0 → host default). The GC sweeper is started in the
	// pty AppFunc where it has a serving-lifetime ctx.
	host.SetPersistentSessions(conf.PersistentSessions, 0)
	// Expose the Host on the visor so the RPC layer can drive Exec
	// directly (see pkg/visor/rpc_visor.go DmsgPtyExec). Without this
	// the integrated `skywire cli dmsg pty exec` path is forced
	// through the host's CLI control socket — a separate listener
	// with separate permissions from the visor's RPC.
	v.dmsgPty = host

	// The dmsg + skynet + TCP listeners moved from the init module
	// into a launcher-managed Internal app (RFC #2775 Phase 3.3).
	// Register the AppFunc here while we have closure-access to the
	// constructed Host and the per-mode config. The launcher's
	// AutoStart pass (see init_apps.go) brings the listeners up
	// once the launcher is constructed; visor halt tears them down
	// via procM.Close. RestartPolicy=Always ensures stop just
	// triggers an auto-restart — operator-initiated `cli visor app
	// stop pty` doesn't permanently remove the pty surface from a
	// remote machine (lockout safety).
	ptyPort := conf.DmsgPort
	sshAddr := conf.SshListen
	launcher.RegisterApp("pty", buildPtyAppFunc(v, host, ptyPort, sshAddr))

	// dmsgscp Host (scp-over-dmsg). On by default — access is gated
	// by the same whitelist that dmsgpty uses. Operators opt OUT
	// via Dmsgscp.Disabled. When Dmsgscp.Whitelist is non-empty it
	// overrides the dmsgpty whitelist; otherwise the dmsgpty
	// whitelist (already constructed above with hypervisors + own
	// PK) is reused so trusting a peer for pty also trusts them
	// for file transfer.
	//
	// Listens on BOTH dmsg and the skywire router at the same port.
	// dmsg covers the bootstrap path; skynet covers steady-state
	// operation over arbitrary transports.
	scpConf := v.conf.Dmsgscp
	if scpConf == nil {
		scpConf = &visorconfig.Dmsgscp{} // all-defaults: enabled, port 23, default rootDir, dmsgpty whitelist
	}
	if !scpConf.Disabled {
		scpWL := wl
		if len(scpConf.Whitelist) > 0 {
			ownWL := pty.NewMemoryWhitelist()
			if err := ownWL.Add(scpConf.Whitelist...); err != nil {
				return fmt.Errorf("dmsgscp: seed whitelist: %w", err)
			}
			if err := ownWL.Add(v.conf.Hypervisors...); err != nil {
				return fmt.Errorf("dmsgscp: seed hypervisors: %w", err)
			}
			if err := ownWL.Add(v.conf.PK); err != nil {
				v.log.Errorf("Cannot add itself to the scp whitelist: %s", err)
			}
			scpWL = ownWL
		}
		// Share scp's whitelist with VisorCat so cat's listen-mode
		// auth matches scp's. Runtime mutations via the dmsgpty
		// whitelist gateway propagate without rebuilding because
		// memoryWhitelist mutates in place.
		v.dmsgWL = scpWL
		rootDir := scpConf.RootDir
		if rootDir == "" {
			rootDir = filepath.Join(v.conf.LocalPath, "scp-root")
		}
		scpHost, err := dmsgscp.NewHost(dmsgC, scpWL, rootDir)
		if err != nil {
			return fmt.Errorf("dmsgscp: build host: %w", err)
		}
		scpPort := scpConf.DmsgPort
		if scpPort == 0 {
			scpPort = dmsgscp.DefaultPort
		}
		serveCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel called in pushCloseStack
		wg := new(sync.WaitGroup)
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := scpHost.ListenAndServe(serveCtx, scpPort); err != nil {
				runtimeErrors <- fmt.Errorf("dmsgscp listen-and-serve stopped: %w", err)
			}
		}()
		// Skynet mirror — same port, same Host. Polls until the
		// router's skynet networker is registered (initRouter may
		// run after this), then binds and serves. Same accept loop
		// as the dmsg side; per-stream whitelist gate is uniform
		// across both transports.
		goServeSkynetMirror(serveCtx, v.conf.PK, scpPort, "dmsgscp", v.log,
			func(skyLis net.Listener) {
				if err := scpHost.ServeListener(serveCtx, skyLis); err != nil {
					getErrors(ctx) <- fmt.Errorf("dmsgscp skynet mirror stopped: %w", err)
				}
			})
		v.pushCloseStack("dmsgscp.serve", func() error {
			cancel()
			wg.Wait()
			return nil
		})
		log.WithField("port", scpPort).WithField("root", rootDir).Info("dmsgscp host started (dmsg + skynet).")
	} else {
		// scp disabled — VisorCat still needs an auth source. Fall
		// back to the dmsgpty whitelist (same shared reference the
		// pty Host + WhitelistGateway mutate).
		v.dmsgWL = wl
	}

	if conf.CLINet != "" {

		if conf.CLINet == "unix" {
			if err := os.MkdirAll(filepath.Dir(conf.CLIAddr), ownerRWX); err != nil {
				err := fmt.Errorf("failed to prepare unix file for dmsgpty cli listener: %w", err)
				return err
			}
		}

		cliL, err := net.Listen(conf.CLINet, conf.CLIAddr)
		if err != nil {
			err := fmt.Errorf("failed to start dmsgpty cli listener: %w", err)
			return err
		}

		serveCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is called in pushCloseStack
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := host.ServeCLI(serveCtx, cliL); err != nil {
				runtimeErrors <- fmt.Errorf("serve cli stopped: %w", err)
			}
		}()

		v.pushCloseStack("router.serve", func() error {
			cancel()
			err := cliL.Close()
			wg.Wait()
			return err
		})
	}

	return nil
}

func initDmsgPing(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	lis, err := dmsgC.Listen(skyenv.DmsgPingPort)
	if err != nil {
		return err
	}

	v.pushCloseStack("dmsg_ping", lis.Close)

	acceptPings := func(lis net.Listener, transport string) {
		var wg sync.WaitGroup
		defer wg.Wait()
		for {
			conn, err := lis.Accept()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					log.WithError(err).WithField("transport", transport).
						Error("Failed to accept ping conn")
				}
				return
			}
			log.WithField("transport", transport).Debugf("Accepted ping conn from %s", conn.RemoteAddr())
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleDmsgPingConn(log, conn)
			}()
		}
	}
	go acceptPings(lis, "dmsg")

	// Skynet mirror of the ping responder at the same port. Same
	// handler, same response wire — clients can ping the visor over
	// any transport the router can negotiate.
	goServeSkynetMirror(ctx, v.conf.PK, skyenv.DmsgPingPort, "dmsg_ping", log,
		func(skyLis net.Listener) {
			acceptPings(skyLis, "skynet")
		})

	log.WithField("port", skyenv.DmsgPingPort).Info("Dmsg ping listener started")
	return nil
}

func handleDmsgPingConn(log *logging.Logger, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("Error closing dmsg ping conn")
		}
	}()

	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Error("Failed to read dmsg ping packet")
			}
			return
		}

		var size PingSizeMsg
		err = json.Unmarshal(buf[:n], &size)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal dmsg ping size")
			return
		}

		// Ack the size message
		_, err = conn.Write([]byte("ok"))
		if err != nil {
			log.WithError(err).Error("Failed to write dmsg ping ack")
			return
		}

		// Read the full ping payload
		var ping []byte
		for len(ping) < size.Size {
			n, err = conn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.WithError(err).Error("Failed to read dmsg ping data")
				}
				return
			}
			ping = append(ping, buf[:n]...)
		}

		// Echo back for RTT measurement
		// If EchoFull is set, echo the full payload for bandwidth testing
		if size.EchoFull {
			_, err = conn.Write(ping)
			if err != nil {
				log.WithError(err).Error("Failed to write full dmsg ping echo")
				return
			}
			log.Debugf("Echoed full dmsg ping response (%d bytes)", len(ping))
		} else {
			_, err = conn.Write([]byte("pong"))
			if err != nil {
				log.WithError(err).Error("Failed to write dmsg ping echo")
				return
			}
			log.Debug("Echoed dmsg ping response")
		}
	}
}

// initDmsgServerLatency initializes DMSG server latency tracking.
// It self-pings via each connected DMSG server on startup and hourly.
func initDmsgServerLatency(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	// Helper to measure latency to all connected servers
	measureServerLatencies := func() {
		servers := dmsgC.ConnectedServersPK()
		if len(servers) == 0 {
			log.Debug("No DMSG servers connected, skipping latency measurement")
			return
		}

		log.WithField("servers", len(servers)).Info("Measuring DMSG server latencies via self-ping")

		for _, serverPKStr := range servers {
			var serverPK cipher.PubKey
			if err := serverPK.Set(serverPKStr); err != nil {
				log.WithError(err).WithField("server", serverPKStr).Warn("Invalid server PK")
				continue
			}

			// Self-ping via this server (ping our own PK through the server)
			start := time.Now()
			conf := PingConfig{
				PK:       v.conf.PK,
				Tries:    3,
				PcktSize: 2, // 2KB
			}

			// Use DmsgPingViaServer to ping ourselves through this specific server
			latencies, err := v.DmsgPingViaServer(conf, serverPK)
			if err != nil {
				log.WithError(err).WithField("server", serverPKStr).Warn("Failed to measure server latency")
				continue
			}

			// Calculate average latency
			var totalLatency time.Duration
			for _, lat := range latencies {
				totalLatency += lat
			}
			avgLatency := totalLatency / time.Duration(len(latencies))

			// Store the latency
			v.dmsgLatency.mu.Lock()
			v.dmsgLatency.servers[serverPK] = avgLatency
			v.dmsgLatency.mu.Unlock()

			log.WithFields(logrus.Fields{
				"server":  serverPKStr,
				"latency": avgLatency.Round(time.Millisecond),
				"elapsed": time.Since(start).Round(time.Millisecond),
			}).Info("Measured DMSG server latency")
		}
	}

	// Initial measurement
	go func() {
		// Small delay to allow more servers to connect
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		measureServerLatencies()
	}()

	// Hourly measurement
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				measureServerLatencies()
			}
		}
	}()

	log.Info("DMSG server latency tracking started")
	return nil
}

// buildPtyAppFunc constructs the AppFunc registered as the "pty"
// Internal app (RFC #2775 Phase 3.3). Captures the constructed
// pty.Host plus the configured listener parameters from
// initDmsgpty so the launcher can start / stop the listeners
// after init has finished. The shape mirrors what initDmsgpty
// itself used to do before this phase:
//
//   - dmsg listener on conf.DmsgPort (primary entry point)
//   - skynet listener on the same port (routed peers)
//   - TCP listener on conf.SshListen (operator-opt-in, ssh-style)
//
// All three share the pty.Host's mux and whitelist; the host
// itself is constructed once at init time and re-used across
// restart cycles.
func buildPtyAppFunc(v *Visor, host *pty.Host, dmsgPort uint16, sshAddr string) appcommon.AppFunc {
	return func(ctx context.Context, _ []string) error {
		log := v.MasterLogger().PackageLogger("app:pty")

		// Complete the in-process IPC handshake the proc manager
		// is waiting for. Without this the launcher times out on
		// ProcStartTimeout (5s), marks the proc stopped, and the
		// app shows up in `cli visor app ls` as stopped even
		// though the listeners are running. Pty doesn't need the
		// app-RPC surface for its own work (it talks straight to
		// pty.Host), but holding the appCl alive keeps the
		// proc lifecycle reportable as the other internal apps
		// (skynet, skychat) do.
		appCl := app.NewClient(nil)
		defer appCl.Close()
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)
		log.Info("Starting pty listeners.")

		wg := new(sync.WaitGroup)
		listenerCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Reap detached/exited persistent pty sessions for the serving
		// lifetime. No-op when PersistentSessions is off.
		go host.RunSessionGC(listenerCtx)

		// dmsg listener — primary entry. ListenAndServe blocks
		// until listenerCtx cancels or the listener fails.
		if dmsgPort != 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := host.ListenAndServe(listenerCtx, dmsgPort); err != nil && !errors.Is(err, context.Canceled) {
					log.WithError(err).Warn("dmsg listener stopped with error")
				}
			}()

			// Parallel skynet listener — accepts pty over the
			// skywire networker so routed peers can reach the
			// service without opening a fresh dmsg stream. nil
			// runtimeErrors channel is OK; the helper's select
			// has a default case.
			if closer := startSkywirePtyListener(listenerCtx, host, v.conf.PK, dmsgPort, nil); closer != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-listenerCtx.Done()
					_ = closer() //nolint:errcheck
				}()
			}
		}

		// Direct-TCP entry point — operator opt-in via
		// Dmsgpty.SshListen. XK-noise handshake gates each accept;
		// the whitelist is shared with the dmsg path. Exposed at
		// CLI as `skywire cli pty shell` / `skywire cli pty host`.
		if sshAddr != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := host.ListenAndServeTCP(listenerCtx, sshAddr, v.conf.PK, v.conf.SK); err != nil && !errors.Is(err, context.Canceled) {
					log.WithError(err).WithField("addr", sshAddr).Warn("tcp listener stopped with error")
				}
			}()
			log.WithField("addr", sshAddr).Info("Mounted pty TCP entry point.")
		}

		appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)

		// Block until app ctx cancel — either operator-initiated
		// `cli visor app stop pty` (auto-restart by Always policy)
		// or visor halt (procM.Close cancels all proc contexts).
		<-ctx.Done()
		log.Info("Stopping pty listeners.")
		cancel()
		wg.Wait()
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		return nil
	}
}

// Ensure the launcher package is referenced. The RegisterApp call
// in initDmsgpty's body uses launcher.RegisterApp; this nolint hint
// keeps tooling that scans for unused imports happy in case the
// init path is conditionally compiled out somewhere.
var _ = launcher.RegisterApp
