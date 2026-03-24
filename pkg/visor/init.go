// Package visor pkg/visor/init.go
//
// This file is the "table of contents" for visor initialization. It declares all
// init modules, registers them with their dependencies, and provides the shared
// context helpers used by the init functions spread across the init_*.go files.
//
// Actual initialization logic lives in:
//   - init_dmsg.go      — DMSG client, ctrl, pty, ping, server latency
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
	"time"

	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsgcurl"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

type visorCtxKey int

const visorKey visorCtxKey = iota

type runtimeErrsCtxKey int

const runtimeErrsKey runtimeErrsCtxKey = iota

const ownerRWX = 0700

// Visor initialization is split into modules, that can be initialized independently
// Modules are declared here as package-level variables, but also need to be registered
// in the modules system: they need init function and dependencies and their name to be set
// To add new piece of functionality to visor, you need to create a new module variable
// and register it properly in registerModules function
var (
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
	// dmsg pty: a remote terminal to the visor working over dmsg protocol
	pty vinit.Module
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
	// Latency probe module (transport latency measurement)
	lp vinit.Module
	// Dmsg ping module (dmsg direct connection)
	dmsgPi vinit.Module
	// Dmsg server latency tracking (self-ping via each server)
	dmsgServerLatency vinit.Module
	// Embedded Transport Setup Node (separate dmsg client with TPS identity)
	embTPS vinit.Module
	// Embedded Route Setup Node (separate dmsg client with route setup identity)
	embRouteSetup vinit.Module
	// UI server module (serves tp-viz)
	uiServer vinit.Module
	// Node health tracking for TPS and RSN
	nodeHealth vinit.Module
	// visor that groups all modules together
	vis vinit.Module
	// config initialization
//	visorConfig vinit.Module
)

// register all modules: instantiate modules with correct names and dependencies, wrap init
// functions to have access to visor and runtime errors channel
func registerModules(logger *logging.MasterLogger) {
	// utility module maker, to avoid passing logger and wrapping each init function
	// in withVisorCtx
	maker := func(name string, f initFn, deps ...*vinit.Module) vinit.Module {
		return vinit.MakeModule(name, withInitCtx(f), logger, deps...)
	}
	//	visorConfig = maker("visor_config", initVisorConfig)
	dmsgHTTP = maker("dmsg_http", initDmsgHTTP)
	ebc = maker("event_broadcaster", initEventBroadcaster)
	ar = maker("address_resolver", initAddressResolver, &dmsgC, &sc, &dmsgHTTP)
	disc = maker("discovery", initDiscovery, &dmsgC, &sc, &dmsgHTTP)
	tr = maker("transport", initTransport, &ar, &ebc, &dmsgHTTP)

	sc = maker("stun_client", initStunClient)
	sudphC = maker("sudph", initSudphClient, &sc, &tr)
	stcprC = maker("stcpr", initStcprClient, &tr)
	stcpC = maker("stcp", initStcpClient, &tr)
	dmsgC = maker("dmsg", initDmsg, &ebc, &dmsgHTTP)
	dmsgCtrl = maker("dmsg_ctrl", initDmsgCtrl, &dmsgC, &tr)
	dmsgHTTPLogServer = maker("dmsghttp_logserver", initDmsgHTTPLogServer, &dmsgC, &tr)
	systemSurvey = maker("system_survey", initSystemSurvey, &dmsgHTTPLogServer)
	dmsgTrackers = maker("dmsg_trackers", initDmsgTrackers, &dmsgC)

	pty = maker("dmsg_pty", initDmsgpty, &dmsgC)
	embRouteSetup = maker("embedded_route_setup", initEmbeddedRouteSetup, &dmsgC)
	rt = maker("router", initRouter, &tr, &dmsgC, &dmsgHTTP, &embRouteSetup)
	launch = maker("launcher", initLauncher, &ebc, &disc, &dmsgC, &tr, &rt)
	cli = maker("cli", initCLI)
	hvs = maker("hypervisors", initHypervisors, &dmsgC)
	ut = maker("uptime_tracker", initUptimeTracker, &dmsgHTTP)
	pv = maker("public_autoconnect", initPublicAutoconnect, &tr, &disc)
	trs = maker("transport_setup", initTransportSetup, &dmsgC, &tr)
	tm = vinit.MakeModule("transports", vinit.DoNothing, logger, &sc, &sudphC, &dmsgCtrl, &dmsgHTTPLogServer, &dmsgTrackers, &launch)
	pvs = maker("public_visor", initPublicVisor, &tr, &ar, &disc, &stcprC)
	skyFwd = maker("sky_forward_conn", initSkywireForwardConn, &dmsgC, &dmsgCtrl, &tr, &launch)
	pi = maker("ping", initPing, &dmsgC, &tm)
	lp = maker("latency_probe", initLatencyProbe, &dmsgC, &tm)
	dmsgPi = maker("dmsg_ping", initDmsgPing, &dmsgC)
	dmsgServerLatency = maker("dmsg_server_latency", initDmsgServerLatency, &dmsgPi)
	tc = maker("transportable", initEnsureVisorIsTransportable, &dmsgC, &tm, &stcprC)
	tpdco = maker("tpd_concurrency", initEnsureTPDConcurrency, &dmsgC, &tm)
	embTPS = maker("embedded_tps", initEmbeddedTPS, &dmsgC)
	uiServer = maker("ui_server", initUIServer, &dmsgC, &tr, &embTPS)
	nodeHealth = maker("node_health", initNodeHealth, &dmsgC)
	vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &ebc, &ar, &disc, &pty,
		&tr, &rt, &launch, &cli, &hvs, &ut, &pv, &pvs, &trs, &stcpC, &stcprC, &skyFwd, &pi, &lp, &dmsgPi, &dmsgServerLatency, &systemSurvey, &tc, &tpdco, &embTPS, &embRouteSetup, &uiServer, &nodeHealth)

	hv = maker("hypervisor", initHypervisor, &vis)
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
	return func(ctx context.Context, log *logging.Logger) error {
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
		// get delegated servers and add them to the client entry
		servers, err := v.dClient.AvailableServers(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting AvailableServers: %w", err)
		}
		// randomize dmsg servers list
		rand.Shuffle(len(servers), func(i, j int) {
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
		return v.dmsgHTTP, nil
	}
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			IdleConnTimeout:   time.Second * 5,
		},
	}, nil
}
