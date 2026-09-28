// Package visor pkg/visor/init.go c3-vis-core
//
// This file is the "table of contents" for visor initialization. It declares all
// init modules and provides the shared context helpers used by the init
// functions spread across the init_*.go files. The modules are registered with
// their dependencies in one of two files, picked by build tag:
//   - init_modules_full.go   — every module (desktop, server, wasm builds)
//   - init_modules_mobile.go — the lite phone core (`mobile` tag): the client
//     modules keep their init functions, the rest are registered as no-ops
//
// Actual initialization logic lives in:
//   - init_dmsg.go      — DMSG client, ctrl, pty, ping, server latency
//   - init_dmsg_server.go — the in-process dmsg server (not in the mobile build)
//   - init_transport.go — Transport manager, STCPR/SUDPH/STCP, address resolver, TPD
//   - init_router.go    — Router, route setup hooks, embedded route setup, node health
//   - init_apps.go      — App launcher, CLI/RPC, hypervisors
//   - init_services.go  — Event broadcaster, uptime, survey, forwarding, ping, UI server
package visor

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

type visorCtxKey int

const visorKey visorCtxKey = iota

type runtimeErrsCtxKey int

const runtimeErrsKey runtimeErrsCtxKey = iota

const ownerRWX = 0700

// Visor initialization is split into modules, that can be initialized independently.
// modules is one visor's module graph: each module needs an init function, its
// dependencies and its name. To add a piece of functionality to the visor, add a
// field here and register it in registerModules — in BOTH init_modules_full.go
// and init_modules_mobile.go (as vinit.DoNothing there if the phone does not
// run it), so every field is a valid Module in either build.
type modules struct {
	// Event broadcasting system
	ebc vinit.Module
	// Address resolver
	ar vinit.Module
	// App discovery
	disc vinit.Module
	// Stun module
	sc vinit.Module
	// SUDPH module
	sudphC vinit.Module
	// STCPR module
	stcprC vinit.Module
	// STCP module
	stcpC vinit.Module
	// QUIC module (#2607 QUIC follow-on)
	quicC vinit.Module
	// WS module: serves the WebSocket transport over the stcpr+WS cmux so
	// browser (wasm) visors can reach this visor over WebSocket
	wsC vinit.Module
	// WT module: serves the WebTransport (QUIC/HTTP3) transport + registers its
	// cert hash with the AR so browser (wasm) visors can dial it
	wtC vinit.Module
	// dmsg pty: a remote terminal to the visor working over dmsg protocol
	ptyModule vinit.Module
	// Dmsg module
	dmsgC vinit.Module
	// Transportability checker ensures the visor can accept transports by creating a self-transport or exiting after 3 failed attempts to create one
	tc vinit.Module
	// TPD concurrency checker removes transports from tpd that the visor does not have registered locally
	tpdco vinit.Module
	// Transport manager
	tr vinit.Module
	// Transport setup
	trs vinit.Module
	// Routing system
	rt vinit.Module
	// Application launcher
	launch vinit.Module
	// CLI
	cli vinit.Module
	// hypervisors to control this visor
	hvs vinit.Module
	// Uptime tracker
	ut vinit.Module
	// Public visors: automatically establish connections to public visors
	pvs vinit.Module
	// Public visor: advertise current visor as public
	pv vinit.Module
	// Transport module (this is not a functional module but a grouping of all heavy transport types initializations)
	tm vinit.Module
	// hypervisor module
	hv vinit.Module
	// Dmsg ctrl module
	dmsgCtrl vinit.Module
	// Dmsg http log server module
	dmsgHTTPLogServer vinit.Module
	// Embedded deployment services on the dmsg HTTP port
	embeddedServices vinit.Module
	// System survey module
	systemSurvey vinit.Module
	// Dmsg http module
	dmsgHTTP vinit.Module
	// Dmsg trackers module
	dmsgTrackers vinit.Module
	// Skywire Forwarding conn module
	skyFwd vinit.Module
	// Ping module (skywire routes)
	pi vinit.Module
	// Dmsg ping module (dmsg direct connection)
	dmsgPi vinit.Module
	// In-process dmsg server sharing the visor's PK/SK (config-gated, default off)
	dmsgSrv vinit.Module
	// Dmsg server latency tracking (self-ping via each server)
	dmsgServerLatency vinit.Module
	// Embedded Transport Setup Node (separate dmsg client with TPS identity)
	embTPS vinit.Module
	// Embedded Route Setup Node (separate dmsg client with route setup identity)
	embRouteSetup vinit.Module
	// Embedded dmsgweb resolver (localhost SOCKS5 for .dmsg browsing)
	embDmsgWeb vinit.Module
	embWisp    vinit.Module
	// Clearnet HTTP forward-proxy over dmsg (opt-in, whitelist-gated)
	embFwdProxy vinit.Module
	// Embedded skynetweb resolver (localhost SOCKS5 for .skynet browsing)
	embSkynetWeb vinit.Module
	// Additional resolving proxies from the `resolvers` config list
	embResolvers vinit.Module
	// Native real-origin browse proxy (loopback reverse-proxy origin over dmsg)
	meshProxy vinit.Module
	// Embedded SMTP→skywire bridge (localhost SMTP listener for *.skynet recipients)
	embSkymailBridge vinit.Module
	skymail          vinit.Module
	// UI server module (serves tp-viz)
	uiServer vinit.Module
	// Node health tracking for TPS and RSN
	nodeHealth vinit.Module
	// Auto-register localhost ports for skynet forwarding
	skynetPorts vinit.Module
	// Self-probe: periodic dmsg listener reachability check
	selfProbe vinit.Module
	// Pre-open DmsgAwaitSetupPort listener so it's ready before initRouter
	routerListener vinit.Module
	// Visor-local telemetry store (bbolt + sampler)
	statsMod        vinit.Module
	cxoUserFeedsMod vinit.Module
	// Chat-pair feed manager (opt-in per-partner CXO feeds).
	pairingMod vinit.Module
	// Chat-group feed manager (D1 owner-centric CXO feeds with
	// multi-PK allowlist).
	groupingMod vinit.Module
	// Skychat 1:1 voice-call manager (dmsg+skynet signaling, RTP media).
	voiceMod vinit.Module
	// coinNodesMod forwards + advertises configured fibercoin nodes
	coinNodesMod vinit.Module
	// regCXOMod publishes this visor's discovery entry as a CXO feed
	// (registration-over-CXO) when opted in
	regCXOMod vinit.Module
	// arBindCXOMod mirrors this visor's AR bindings onto a CXO feed the
	// address-resolver aggregates (AR-bind-over-CXO), always-on/additive
	arBindCXOMod vinit.Module
	// sdRegCXOMod mirrors this visor's live service-discovery entry set onto
	// a CXO feed the service-discovery aggregates (SD-registration-over-CXO),
	// always-on/additive
	sdRegCXOMod vinit.Module
	// visor that groups all modules together
	vis vinit.Module
}

type initFn func(context.Context, *Visor, *logging.Logger) error

// ErrNoVisorInCtx is returned when visor is not set in module initialization context
var ErrNoVisorInCtx = errors.New("visor not set in module initialization context")

// ErrNoErrorsCtx is returned when errors channel is not set in module initialization context
var ErrNoErrorsCtx = errors.New("errors not set in module initialization context")

// withInitCtx wraps init function and returns a hook that can be used in
// the module system
// Passed context should have visor value under visorKey key, this visor will be used
// in the passed function
// Passed context should have errors channel for module runtime errors. It can be accessed
// through a function call
func withInitCtx(f initFn) vinit.Hook {
	return func(ctx context.Context, log *logging.Logger) (err error) {
		val := ctx.Value(visorKey)
		v, ok := val.(*Visor)
		if !ok && v == nil {
			return ErrNoVisorInCtx
		}
		val = ctx.Value(runtimeErrsKey)
		errs, ok := val.(chan error)
		if !ok && errs == nil {
			return ErrNoErrorsCtx
		}
		// Recover a panicking module init so one bad module can't crash the
		// whole visor process. This matters most during Resume (Suspend/Resume,
		// api_suspend.go), which RE-RUNS the module graph and can hit
		// non-idempotent init (a module init runs in its own goroutine under
		// InitConcurrent, so an un-recovered panic there kills the process, not
		// just the module). Convert it to an error the init system reports, and
		// log the stack so the offending module is identifiable.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("module init panicked: %v", r)
				logging.LogRecovered(log, "module init", r)
			}
		}()
		return f(ctx, v, log)
	}
}

func getErrors(ctx context.Context) chan error {
	val := ctx.Value(runtimeErrsKey)
	errs, ok := val.(chan error)
	if !ok && errs == nil {
		// ok to panic because with check for this value in withInitCtx
		// probably will never be reached, but better than generic NPE just in case
		panic("runtime errors channel is not set in context")
	}
	return errs
}

func getHTTPClient(ctx context.Context, v *Visor, service string) (*http.Client, error) {

	var serviceURL dmsgcurl.URL
	var delegatedServers []cipher.PubKey
	err := serviceURL.Fill(service)

	if serviceURL.Scheme == "dmsg" {
		if err != nil {
			return nil, fmt.Errorf("provided URL is invalid: %w", err)
		}
		// Defense-in-depth: v.dClient is seeded from the embedded deployment
		// set in initDmsgHTTP so it is normally never nil, but an
		// empty-keyring build could still leave it nil — fail the call with a
		// clear error instead of nil-dereferencing and crashing visor startup.
		if v.dClient == nil {
			return nil, fmt.Errorf("dmsg direct client unavailable (no configured/cached/embedded dmsg servers); cannot resolve %s", serviceURL.Host)
		}
		// get delegated servers and add them to the client entry
		servers, err := v.dClient.AvailableServers(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting AvailableServers: %w", err)
		}
		// randomize dmsg servers list for load distribution (not security-
		// sensitive — a weak RNG is fine and crypto/rand would be needless).
		rand.Shuffle(len(servers), func(i, j int) { //nolint:gosec // non-crypto shuffle for dmsg-server load distribution
			servers[i], servers[j] = servers[j], servers[i]
		})
		for _, server := range servers {
			delegatedServers = append(delegatedServers, server.Static)
		}

		clientEntry := &dmsgdisc.Entry{
			Client: &dmsgdisc.Client{
				DelegatedServers: delegatedServers,
			},
			Static: serviceURL.Addr.PK,
		}

		err = v.dClient.PostEntry(ctx, clientEntry)
		if err != nil {
			return nil, fmt.Errorf("error saving clientEntry: %w", err)
		}
		// Wait for the background DMSG HTTP transport to become ready.
		// initDmsgHTTP starts the connection asynchronously; the channel
		// is closed once v.dmsgHTTP is set.
		select {
		case <-v.dmsgHTTPReady:
		case <-ctx.Done():
			return nil, fmt.Errorf("DMSG HTTP transport not ready: %w", ctx.Err())
		}
		if v.dmsgHTTP == nil {
			return nil, fmt.Errorf("DMSG HTTP transport failed to initialize")
		}
		return v.dmsgHTTP, nil
	}
	// Deployment services are dmsg-only: plain HTTP to a deployment service is no
	// longer supported. A non-dmsg URL here is a misconfiguration (a clearnet
	// service URL) — fail loud instead of silently building a clearnet client.
	return nil, fmt.Errorf("deployment service %q is not a dmsg:// URL — plain HTTP to deployment services is no longer supported", service)
}
