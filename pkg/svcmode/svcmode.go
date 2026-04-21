// Package svcmode provides a shared helper for skywire deployment
// services (transport-discovery, address-resolver, route-finder,
// service-discovery, uptime-tracker, config-bootstrapper) that need
// to listen on HTTP, dmsghttp, or both simultaneously. It centralizes:
//
// Note on dmsg-discovery specifically: dmsg-discovery's HTTP listener
// is load-bearing — dmsg-servers themselves use disc.NewHTTP(...)
// (plain HTTP client) to register with it (see cmd/dmsg-commands/
// dmsg-server/commands/start/root.go). If dmsg-discovery were to run
// in ModeDmsg only, dmsg-servers would have no way to register
// because they ARE the dmsg transport layer. dmsg-discovery must run
// in ModeHTTP or ModeDual; ModeDmsg is not meaningful for it. This
// package does not enforce that constraint — dmsg-discovery's
// command wrapper is responsible for rejecting ModeDmsg explicitly.
//
//   - Mode selection (http | dmsg | dual) via the --mode flag, with a
//     sensible default derived from whether a DMSG secret key was
//     provided.
//   - DMSG bootstrap via the existing cmdutil.BootstrapDmsg helper
//     (which uses the embedded services-config.json dmsg_servers list
//     so services can come up against private deployments without a
//     pre-populated dmsg-discovery).
//   - HTTP listener (tcpproxy.ListenAndServe).
//   - dmsghttp listener (dmsghttp.ListenAndServe) and survey whitelist
//     debug handler (dmsghttp.ServeDebug) when dmsg is enabled.
//   - Consistent shutdown: a single Handle.Close() that tears down
//     both listeners and the bootstrap dmsg client.
//
// Services keep ownership of their API handler, their CLI flag
// parsing, and any service-specific extras (UDP listeners, private
// admin HTTP, CXO publishers, etc.) — svcmode only handles the
// HTTP + dmsghttp pair that every service has in common.
package svcmode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
)

// Mode selects which inbound listeners a service exposes. It does
// NOT control whether the service has a dmsg client at all — that is
// determined purely by whether a secret key is configured. A service
// running in ModeHTTP with an SK still bootstraps a dmsg.Client
// (useful for outbound-only dmsg consumers such as TPD's CXO
// publisher); it simply does not spawn a dmsghttp.ListenAndServe.
type Mode string

const (
	// ModeHTTP exposes only the bound HTTP address for inbound
	// client requests. If a secret key is present the dmsg client
	// is still bootstrapped — which means the service connects to
	// dmsg-servers via HTTP dmsg-discovery lookup — but no
	// dmsghttp listener is spawned. Useful for public deployments
	// that only want inbound HTTP but still need a dmsg.Client for
	// outbound purposes or for services that historically ran this
	// way by setting --sk but never had a dmsghttp in their chain.
	ModeHTTP Mode = "http"
	// ModeDmsg listens only on dmsghttp (dmsg://<pk>:<port>) for
	// inbound requests. No plaintext HTTP listener is exposed.
	// Useful for corporate / private network deployments that must
	// not serve plaintext HTTP. Requires SK.
	ModeDmsg Mode = "dmsg"
	// ModeDual listens on both http and dmsghttp. This is the
	// default when a DMSG secret key is available and matches the
	// existing pre-svcmode behavior of all services.
	ModeDual Mode = "dual"
)

// ModeEnv is the environment variable name that overrides --mode.
// Useful for container deployments that want to toggle mode without
// rewriting the command line.
const ModeEnv = "SKYWIRE_SVC_MODE"

// ParseMode normalizes a mode string to one of the known Mode values.
// Empty / unrecognized input returns an error. Use ResolveMode to
// apply the "default based on SK presence" fallback.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", errors.New("svcmode: empty mode")
	case "http":
		return ModeHTTP, nil
	case "dmsg", "dmsghttp":
		return ModeDmsg, nil
	case "dual", "both":
		return ModeDual, nil
	default:
		return "", fmt.Errorf("svcmode: unknown mode %q (want http|dmsg|dual)", s)
	}
}

// ResolveMode resolves the effective mode from (in order of precedence):
//  1. SKYWIRE_SVC_MODE environment variable
//  2. The passed-in flag value (usually from --mode)
//  3. The default: ModeDual if SK is non-null, else ModeHTTP
//
// Returns an error only if an explicit value from step 1 or 2 is
// unrecognized.
func ResolveMode(flagValue string, skNonNull bool) (Mode, error) {
	if env := os.Getenv(ModeEnv); env != "" {
		return ParseMode(env)
	}
	if flagValue != "" {
		return ParseMode(flagValue)
	}
	if skNonNull {
		return ModeDual, nil
	}
	return ModeHTTP, nil
}

// IncludesHTTP reports whether the mode spawns an http listener.
func (m Mode) IncludesHTTP() bool { return m == ModeHTTP || m == ModeDual }

// IncludesDmsg reports whether the mode spawns a dmsghttp listener.
func (m Mode) IncludesDmsg() bool { return m == ModeDmsg || m == ModeDual }

// Config configures a svcmode.Handle. All fields are optional unless
// required by the chosen Mode; Start returns a descriptive error if
// a required field is missing.
type Config struct {
	// Mode is the effective listener mode. Use ResolveMode to build
	// it from flag/env/SK.
	Mode Mode

	// HTTPAddr is the bind address for the http listener
	// (e.g. ":9092"). Required when Mode includes http.
	HTTPAddr string

	// Handler is the http.Handler used for BOTH the http and
	// dmsghttp listeners. Services typically pass their API object
	// directly since it satisfies http.Handler.
	Handler http.Handler

	// DMSG identity. Required when Mode includes dmsg. If SK is
	// null and Mode includes dmsg, Start returns an error.
	PK cipher.PubKey
	SK cipher.SecKey

	// DmsgPort is the dmsghttp listener port. Defaults to
	// dmsg.DefaultDmsgHTTPPort (80) if zero.
	DmsgPort uint16

	// DmsgDiscovery is the URL of the dmsg-discovery service, used
	// as a fallback for the embedded-server bootstrap and for
	// periodic server-list refresh. Required when Mode includes
	// dmsg unless EmbeddedDmsgServers is non-empty.
	DmsgDiscovery string

	// DmsgServerType, if non-empty, restricts the dmsg client to
	// servers whose ServerType matches (e.g. "official").
	DmsgServerType string

	// EmbeddedDmsgServers is the list of dmsg server entries to use
	// for initial bootstrap. Services should typically pass
	// dmsg.Prod.DmsgServers (or dmsg.Test.DmsgServers when in test
	// environment) from the embedded services-config.json.
	EmbeddedDmsgServers []disc.Entry

	// SurveyWhitelist is the list of public keys permitted to
	// access the dmsghttp debug handler (pprof, etc.). Typically
	// deployment.Prod.SurveyWhitelist.
	SurveyWhitelist []cipher.PubKey

	// Log is the shared logger for svcmode bookkeeping and
	// downstream helpers.
	Log *logging.Logger

	// OnDmsgServersUpdated, if non-nil, is called periodically with
	// the current list of connected dmsg server PKs. Services that
	// publish this list via their API (e.g. in /health) should set
	// this so svcmode's background poll can keep it fresh.
	OnDmsgServersUpdated func([]string)

	// DmsgServerPollInterval is the interval at which
	// OnDmsgServersUpdated is called. Defaults to 1 second if zero
	// (matching the existing per-service polling loops).
	DmsgServerPollInterval time.Duration

	// DisableDHT prevents the automatic DHT full node from starting.
	// Disabled by default — DMSG servers handle DHT, not deployment
	// services. Set to false explicitly to enable on a service.
	DisableDHT bool
}

// Handle owns the listeners and the bootstrap dmsg client. Callers
// should defer Handle.Close() immediately after Start returns.
type Handle struct {
	// DmsgClient is the bootstrap dmsg client or nil when Mode is
	// ModeHTTP. Services that need raw dmsg access (e.g. to spawn
	// a CXO publisher or TPS listener) can use it directly.
	DmsgClient *dmsg.Client

	// DHTNode is the DHT full node, or nil when EnableDHT is false.
	DHTNode *dht.Node

	// Mode is the resolved listener mode.
	Mode Mode

	log       *logging.Logger
	boot      *cmdutil.DmsgBootstrap
	errCh     chan error
	closeOnce func()
}

// Errors exposes the async listener errors. Callers that want to
// terminate on listener failure should select on this channel in
// their main loop alongside <-ctx.Done().
func (h *Handle) Errors() <-chan error { return h.errCh }

// Close stops the dmsg bootstrap and its listeners. Safe to call
// multiple times. The http listener stops when the context passed to
// Start is canceled — svcmode does not own a separate http.Server
// instance because tcpproxy.ListenAndServe is not cancellable on its
// own; callers rely on process exit.
func (h *Handle) Close() {
	if h.closeOnce != nil {
		h.closeOnce()
	}
}

// Start wires up the listeners described by cfg and returns a Handle.
// The caller is responsible for blocking on ctx.Done() (or on
// Handle.Errors()) in its main loop, and for calling Handle.Close()
// on shutdown.
//
// dmsg bootstrap is performed whenever cfg.SK is non-null, even in
// ModeHTTP — a service's "mode" controls its inbound listeners only,
// not whether it has a dmsg.Client. Services with an SK always get
// a bootstrapped client they can reuse for outbound purposes
// (Handle.DmsgClient) regardless of mode.
//
// Start returns an error on any validation failure. It does NOT
// return an error if an async listener (http or dmsghttp) fails to
// serve — those errors are surfaced via Handle.Errors() instead.
func Start(ctx context.Context, cfg Config) (*Handle, error) {
	if cfg.Log == nil {
		cfg.Log = logging.MustGetLogger("svcmode")
	}
	if cfg.Handler == nil {
		return nil, errors.New("svcmode: Handler is required")
	}
	if cfg.Mode == "" {
		return nil, errors.New("svcmode: Mode is required (use ResolveMode)")
	}
	if cfg.Mode.IncludesHTTP() && cfg.HTTPAddr == "" {
		return nil, fmt.Errorf("svcmode: HTTPAddr required for mode %q", cfg.Mode)
	}
	if cfg.Mode.IncludesDmsg() && cfg.SK.Null() {
		return nil, fmt.Errorf("svcmode: DMSG secret key required for mode %q", cfg.Mode)
	}
	// If the caller provided an SK we will attempt dmsg bootstrap
	// even if the mode is ModeHTTP — that lets http-only services
	// still maintain a dmsg.Client for outbound use. The call needs
	// either the embedded server list OR an HTTP discovery URL to
	// bootstrap against.
	wantDmsgBootstrap := !cfg.SK.Null()
	if wantDmsgBootstrap && len(cfg.EmbeddedDmsgServers) == 0 && cfg.DmsgDiscovery == "" {
		return nil, errors.New("svcmode: dmsg bootstrap requires either EmbeddedDmsgServers or DmsgDiscovery")
	}
	if cfg.DmsgPort == 0 {
		cfg.DmsgPort = dmsg.DefaultDmsgHTTPPort
	}
	if cfg.DmsgServerPollInterval == 0 {
		cfg.DmsgServerPollInterval = time.Second
	}

	h := &Handle{
		Mode:  cfg.Mode,
		log:   cfg.Log,
		errCh: make(chan error, 2),
	}
	closedCh := make(chan struct{})
	h.closeOnce = func() {
		select {
		case <-closedCh:
			return
		default:
			close(closedCh)
		}
		if h.boot != nil && h.boot.Close != nil {
			h.boot.Close()
		}
	}

	// Bring up dmsg first (if we have a secret key) so http can
	// still start below even if dmsg bootstrap is slow. The dmsg
	// client is kept regardless of whether we're going to spawn
	// a dmsghttp listener — see note at top of Start.
	if wantDmsgBootstrap {
		boot, err := cmdutil.BootstrapDmsg(ctx, cfg.Log, cfg.PK, cfg.SK,
			cfg.EmbeddedDmsgServers, cfg.DmsgDiscovery, cfg.DmsgServerType)
		if err != nil {
			return nil, fmt.Errorf("svcmode: dmsg bootstrap: %w", err)
		}
		h.boot = boot
		h.DmsgClient = boot.Client

		if cfg.Mode.IncludesDmsg() {
			cfg.Log.Infof("svcmode: dmsghttp listener on port %d", cfg.DmsgPort)
			go func() {
				if err := dmsghttp.ListenAndServe(ctx, cfg.SK, cfg.Handler,
					boot.DClient, cfg.DmsgPort, boot.Client, cfg.Log); err != nil && !errors.Is(err, context.Canceled) {
					select {
					case h.errCh <- fmt.Errorf("dmsghttp: %w", err):
					default:
					}
				}
			}()

			if len(cfg.SurveyWhitelist) > 0 {
				go func() {
					if err := dmsghttp.ServeDebug(ctx, boot.Client, cfg.Log, cfg.SurveyWhitelist); err != nil && !errors.Is(err, context.Canceled) {
						cfg.Log.WithError(err).Warn("svcmode: ServeDebug exited with error")
					}
				}()
			}
		} else {
			cfg.Log.Info("svcmode: dmsg client bootstrapped for outbound use (no dmsghttp listener; mode=http)")
		}

		if cfg.OnDmsgServersUpdated != nil {
			go h.pollDmsgServers(ctx, closedCh, cfg.OnDmsgServersUpdated, cfg.DmsgServerPollInterval)
		}
	}

	if cfg.Mode.IncludesHTTP() {
		cfg.Log.Infof("svcmode: http listener on %s", cfg.HTTPAddr)
		go func() {
			if err := tcpproxy.ListenAndServe(cfg.HTTPAddr, cfg.Handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case h.errCh <- fmt.Errorf("http: %w", err):
				default:
				}
			}
		}()
	}

	// Start DHT full node when the service has a DMSG client and
	// DHT is explicitly enabled. Disabled by default for deployment
	// services — DMSG servers handle DHT networking. Services that
	// need DHT should set DisableDHT=false in their svcmode.Config.
	if !cfg.DisableDHT && h.DmsgClient != nil {
		dhtLog := cfg.Log.WithField("subsystem", "dht")
		bootstrapPKs := deployment.Prod.DHTBootstrapPKs()
		dhtCfg := dht.Config{
			BootstrapPKs: bootstrapPKs,
			FullNode:     true,
		}
		tp := dht.NewDMSGTransport(h.DmsgClient)
		dhtNode := dht.New(dhtCfg, cfg.PK, cfg.SK, tp, logging.MustGetLogger("dht"))
		if err := dhtNode.Start(ctx); err != nil {
			dhtLog.WithError(err).Warn("DHT node failed to start")
		} else {
			h.DHTNode = dhtNode
			dhtLog.WithField("id", dhtNode.ID().String()[:16]).
				WithField("bootstrap_peers", len(bootstrapPKs)).
				Info("DHT full node started")
		}
	}

	return h, nil
}

// pollDmsgServers pushes the current connected-server PK list into
// the service's OnDmsgServersUpdated callback at the configured
// interval until ctx or Close fires. Matches the per-service polling
// loops used before svcmode.
func (h *Handle) pollDmsgServers(ctx context.Context, closed <-chan struct{}, cb func([]string), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case <-ticker.C:
			if h.DmsgClient != nil {
				cb(h.DmsgClient.ConnectedServersPK())
			}
		}
	}
}
