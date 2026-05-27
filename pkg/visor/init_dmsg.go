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
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/dmsgfirst"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgctrl"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgscp"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
		return nil
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

	// Start DMSG HTTP connection in background so it doesn't block visor startup.
	// Downstream modules check v.dmsgHTTP != nil before using DMSG transport
	// and fall back to plain HTTP if it's not ready yet.
	go func() {
		dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, v.MasterLogger().PackageLogger("dmsg_http:dmsgDC"),
			v.conf.PK, v.conf.SK, dClient, dmsg.DefaultConfig())
		if err != nil {
			log.WithError(err).Warn("DMSG HTTP transport unavailable")
			return
		}

		dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}

		v.pushCloseStack("dmsg_http", func() error {
			closeDmsgDC()
			return nil
		})

		v.initLock.Lock()
		v.dmsgHTTP = &dmsgHTTP
		v.dmsgDC = dmsgDC
		v.initLock.Unlock()
		close(v.dmsgHTTPReady)

		log.Info("DMSG HTTP transport ready")
	}()

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
	discURL := primary
	if primaryDmsg != "" && v.dmsgHTTP != nil {
		if _, err := getHTTPClient(ctx, v, primaryDmsg); err == nil {
			discURL = primaryDmsg
			log.Info("Using DMSG-HTTP for dmsg discovery")
		} else if discURL != "" {
			log.WithError(err).Warn("DMSG-HTTP discovery failed, using plain HTTP")
		} else {
			return fmt.Errorf("DMSG-only deployment but DMSG discovery unreachable: %w", err)
		}
	} else if discURL == "" && primaryDmsg != "" {
		// DMSG URL set but dmsgHTTP not ready — can't proceed without either
		discURL = primaryDmsg
		log.Warn("HTTP discovery URL empty, attempting DMSG discovery without dmsgHTTP transport")
	}

	httpC, err := getHTTPClient(ctx, v, discURL)
	if err != nil {
		return err
	}
	// Override the discovery URL used by the DMSG client
	dmsgConf := *v.conf.Dmsg
	dmsgConf.Discovery = discURL
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
	dmsgC := dmsgc.New(v.conf.PK, v.conf.SK, v.ebc, &dmsgConf, httpC, v.MasterLogger())
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
	v.initLock.Unlock()

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
	// FIRST, before swapping in the dmsgfirst disc client below. The
	// dmsg-discovery's own PK is in this list, and dmsgfirst's
	// primary path needs to DialStream to that PK — without the
	// seed already in place, the moment dmsgC.SetDiscoveryClients
	// installs dmsgfirst, any background AvailableServers /
	// PutEntry refresh that dmsgC kicks off recurses (DialStream →
	// cache miss → disc → dmsgfirst.primary → DialStream(dmsgdiscPK)
	// → cache miss → ...) until the goroutine stack overflows.
	v.seedDmsgServiceEntries(dmsgC, log)

	// Now safe to upgrade. dmsgC's background discovery refreshes
	// will hit the seeded dmsgdiscPK entry from cache, route
	// through the delegated-server path, and never recurse back
	// into the disc client looking for that PK.
	//
	// The initial-construction httpC was forced to plain HTTP
	// whenever initDmsgHTTP's dmsgDC wasn't ready yet (a startup
	// race), which pinned the dmsgC's discovery refresh to HTTP
	// for the whole process lifetime — so an outage on the public
	// HTTP fronting (Caddy, etc.) would break the visor's
	// discovery refresh even when DMSG was healthy.
	//
	// Run the upgrade in a goroutine that waits for v.dmsgHTTPReady
	// (initDmsgHTTP's dmsgDC) before wiring it in as dmsgfirst's
	// primary path. dmsgDC is direct.Client-backed — it carries a
	// synthetic entry for the dmsg-disc PK with all known server PKs
	// as delegated, so DialStream resolves without ever needing an
	// HTTP-discovery lookup. Pre-fix the primary used the main dmsgC,
	// whose own discovery was dmsgfirst, so every Entry/PutEntry call
	// from the visor's own updateClientEntryLoop fell back to HTTP
	// after the DMSG primary's DialStream timed out — dmsg-disc has
	// no entry in its own DB (root of trust, by design) so the dial
	// never had a chance.
	//
	// Until dmsgHTTPReady fires (i.e., dmsgDC has bootstrapped its
	// session set), dmsgC keeps the plain-HTTP discovery clients
	// constructed by dmsgc.New. That's the same conservative behavior
	// as before this fix landed.
	go func() {
		select {
		case <-v.dmsgHTTPReady:
		case <-ctx.Done():
			return
		}
		v.initLock.Lock()
		dmsgDC := v.dmsgDC
		v.initLock.Unlock()
		if dmsgDC == nil {
			return
		}
		upgradeDmsgDiscToDmsgfirst(dmsgC, dmsgDC, v.conf.Dmsg, log)
	}()

	// Start periodic config refresh for dynamic key sets
	go v.startConfigRefresh(ctx) //nolint:errcheck,gosec

	// Refresh the on-disk dmsg-servers cache from dmsg-discovery so the
	// next bootstrap uses the live addresses, not whatever's in
	// skywire.json. Uses a separate disc.HTTP client (the dmsg.Client
	// doesn't expose its inner discovery client).
	if v.dmsgServersCache != nil && discURL != "" {
		go v.refreshDmsgServersCacheLoop(ctx, discURL, httpC)
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
// upgradeDmsgDiscToDmsgfirst swaps each of dmsgC's per-deployment
// disc.APIClient instances for a dmsgfirst-wrapped one so the dmsg
// client's own discovery refresh tries DMSG first and only falls back
// to plain HTTP when the dmsg dial fails. The dmsg-discovery's PK is
// extracted from each deployment's `discovery_dmsg` URL — when that's
// absent, the entry stays on plain HTTP because dmsgfirst.New needs a
// PK to dial. A no-op if no deployments yield a non-zero PK.
//
// primaryDmsgC is the dmsg client dmsgfirst will use for its DMSG-
// primary path. It must be the direct.Client-backed dmsgDC, not the
// main dmsgC — the main dmsgC's own discovery is what we're
// upgrading here, so using it for the primary path would recurse on
// every Entry() lookup. dmsgDC carries the dmsg-disc PK as a synthetic
// direct.Client entry with all known server PKs as delegated, so its
// DialStream(dmsg-disc) resolves locally and goes through a session
// dmsg-disc actually has (it preloads the same server set via
// direct.StartDmsg in dmsgdisc.go).
func upgradeDmsgDiscToDmsgfirst(dmsgC *dmsg.Client, primaryDmsgC *dmsg.Client, conf *dmsgc.DmsgConfig, log *logging.Logger) {
	if conf == nil {
		return
	}
	deployments := conf.AllDeployments()
	if len(deployments) == 0 {
		return
	}
	upgraded := make([]dmsgdisc.APIClient, len(deployments))
	anyUpgraded := false
	for i, d := range deployments {
		pk := cmdutil.PKFromDmsgURL(d.DiscoveryDmsg)
		if pk == (cipher.PubKey{}) {
			upgraded[i] = dmsgdisc.NewHTTP(d.Discovery, &http.Client{}, log)
			continue
		}
		upgraded[i] = dmsgfirst.New(primaryDmsgC, pk, d.Discovery, &http.Client{}, log)
		anyUpgraded = true
		log.WithField("url", d.Discovery).WithField("pk", pk).Info("dmsg discovery client upgraded to dmsgfirst (primary via direct-client dmsgDC)")
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
// Falls back to embedded deployment defaults for missing fields.
func (v *Visor) dmsgServicePKs() cipher.PubKeys {
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	dmsgURLs := []string{
		v.conf.Dmsg.DiscoveryDmsg,
		v.conf.Transport.DiscoveryDmsg,
		v.conf.Transport.AddressResolverDmsg,
		v.conf.Routing.RouteFinderDmsg,
		v.conf.Launcher.ServiceDiscDmsg,
		pick(v.conf.ConfServiceDmsg, deployment.Prod.ConfDmsg),
	}
	if v.conf.UptimeTracker != nil {
		dmsgURLs = append(dmsgURLs, v.conf.UptimeTracker.AddrDmsg)
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
func (v *Visor) seedDmsgServiceEntries(dmsgC *dmsg.Client, log *logging.Logger) {
	var serverPKs []cipher.PubKey
	for _, srv := range v.conf.Dmsg.Servers {
		serverPKs = append(serverPKs, srv.Static)
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
	if v.conf.Dmsgpty != nil {
		if v.conf.Dmsgpty.Whitelist != nil {
			whitelistedPKs = append(whitelistedPKs, v.conf.Dmsgpty.Whitelist...)
		}
	}

	lsAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, "", whitelistedPKs, &v.survey.data, printLog)

	// Set visor as health stats provider for /health endpoint
	lsAPI.SetHealthStatsProvider(v)

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

	// Mount the dmsgpty web terminal at /pty, gated by the same
	// whitelist the dmsgpty Host enforces on direct connections —
	// configured Dmsgpty.Whitelist + Hypervisors + the visor's own
	// PK (a hypervisor running locally can reach itself). The
	// dialer prefers the local CLI socket when it's set up; that
	// avoids a self-loop through dmsg for a request that's already
	// in-process. When no CLI socket is configured we fall back to
	// dialing our own dmsg client at DmsgPtyPort, which works the
	// same way the hypervisor's per-PK ptyUI does for remote visors.
	if v.conf.Dmsgpty != nil {
		var ptyDialer dmsgpty.UIDialer
		if v.conf.Dmsgpty.CLINet != "" {
			ptyDialer = dmsgpty.NetUIDialer(v.conf.Dmsgpty.CLINet, v.conf.Dmsgpty.CLIAddr)
		} else {
			ptyDialer = dmsgpty.DmsgUIDialer(dmsgC, dmsg.Addr{PK: v.conf.PK, Port: skyenv.DmsgPtyPort})
		}
		ptyUI := dmsgpty.NewUI(ptyDialer, dmsgpty.DefaultUIConfig())
		ptyHandler := ptyUI.Handler(map[string][]string{
			"update": visorconfig.UpdateCommand(),
		})
		ptyWL := []cipher.PubKey{v.conf.PK}
		ptyWL = append(ptyWL, v.conf.Hypervisors...)
		if v.conf.Dmsgpty.Whitelist != nil {
			ptyWL = append(ptyWL, v.conf.Dmsgpty.Whitelist...)
		}
		lsAPI.SetPtyHandler(ptyHandler, ptyWL)
		logger.WithField("whitelist_size", len(ptyWL)).Info("Mounted /pty on logserver")
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
	conf := v.conf.Dmsgpty

	if conf == nil {
		log.Debug("'dmsgpty' is not configured, skipping.")
		return nil
	}

	// Unlink dmsg socket files (just in case).
	if conf.CLINet == "unix" {
		if runtime.GOOS == "windows" {
			conf.CLIAddr = dmsgpty.ParseWindowsEnv(conf.CLIAddr)
		}

		if err := osutil.UnlinkSocketFiles(v.conf.Dmsgpty.CLIAddr); err != nil {
			log.WithError(err).Errorf("Insufficient permissions to unlink socket file %q", v.conf.Dmsgpty.CLIAddr)
			return err
		}
	}

	wl := dmsgpty.NewMemoryWhitelist()

	// Initialize the dmsgpty whitelist
	if err := wl.Add(v.conf.Dmsgpty.Whitelist...); err != nil {
		return err
	}

	// Ensure hypervisors are added to the whitelist.
	if err := wl.Add(v.conf.Hypervisors...); err != nil {
		return err
	}
	// add the visor's own public key to the whitelist to allow local pty
	if err := wl.Add(v.conf.PK); err != nil {
		v.log.Errorf("Cannot add itself to the pty whitelist: %s", err)
	}

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
	// compat for every other NewHost caller (cmd/dmsg/dmsgpty-host,
	// sshd CLI, tests).
	pty := dmsgpty.NewHostWithDialer(dmsgC, wl, buildDmsgptyDialer(dmsgC))
	// Expose the Host on the visor so the RPC layer can drive Exec
	// directly (see pkg/visor/rpc_visor.go DmsgPtyExec). Without this
	// the integrated `skywire cli dmsg pty exec` path is forced
	// through the host's CLI control socket — a separate listener
	// with separate permissions from the visor's RPC.
	v.dmsgPty = pty

	if ptyPort := conf.DmsgPort; ptyPort != 0 {
		serveCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is called in pushCloseStack
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := pty.ListenAndServe(serveCtx, ptyPort); err != nil {
				runtimeErrors <- fmt.Errorf("listen and serve stopped: %w", err)
			}
		}()

		v.pushCloseStack("router.serve", func() error {
			cancel()
			wg.Wait()
			return nil
		})

		// Parallel skynet listener — accepts dmsgpty over
		// appnet.SkywireNetworker so remote peers that have a
		// negotiated route to us can reach the pty service without
		// opening a fresh dmsg stream. Returns nil close-func when
		// the skynet networker isn't wired yet (init ordering) or
		// when Listen fails; in either case the dmsg listener
		// above keeps the service functional.
		runtimeErrors := getErrors(ctx)
		if closer := startSkywirePtyListener(context.Background(), pty, v.conf.PK, ptyPort, runtimeErrors); closer != nil {
			v.pushCloseStack("dmsgpty.skywire.serve", closer)
		}

	}

	// Direct-TCP dmsgpty entry point — operator opts in via
	// Dmsgpty.SshListen ("" disables). Same whitelist as the
	// dmsg-overlay path; XK-noise handshake gates the accepted PK
	// before the stream reaches the dmsgpty mux. See
	// dmsgpty/host_tcp.go for the per-connection flow. Exposed at
	// CLI as `skywire cli ssh` / `skywire cli sshd`.
	if tcpAddr := conf.SshListen; tcpAddr != "" {
		tcpCtx, tcpCancel := context.WithCancel(context.Background()) //nolint:gosec // cancel called in pushCloseStack
		tcpWg := new(sync.WaitGroup)
		tcpWg.Add(1)
		go func() {
			defer tcpWg.Done()
			runtimeErrors := getErrors(ctx)
			if err := pty.ListenAndServeTCP(tcpCtx, tcpAddr, v.conf.PK, v.conf.SK); err != nil {
				runtimeErrors <- fmt.Errorf("dmsgpty tcp listen %s stopped: %w", tcpAddr, err)
			}
		}()
		v.pushCloseStack("dmsgpty.tcp.serve", func() error {
			tcpCancel()
			tcpWg.Wait()
			return nil
		})
		log.WithField("addr", tcpAddr).Info("Mounted dmsgpty direct-TCP entry point")
	}

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
			ownWL := dmsgpty.NewMemoryWhitelist()
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
			if err := pty.ServeCLI(serveCtx, cliL); err != nil {
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
