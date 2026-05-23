// Package dmsgdisc pkg/services/dmsgdisc/dmsgdisc.go
//
// dmsg-discovery as a pkg/services.Service. Both the standalone
// cobra command (`skywire dmsg disc`) and the multi-service
// supervisor (`skywire svc run`) end up calling Run(ctx, cfg, log) —
// the per-flag CLI path lives in cmd/dmsg/dmsg-discovery and
// constructs the Config from flags + optional --config file before
// handing off to Run.
//
// dmsg-discovery is a "register-by-HTTP, query-by-HTTP-or-DMSG"
// service: dmsg-servers reach it over plain HTTP (so --mode=dmsg is
// rejected), but visors and operators may query it over either
// transport. The Run loop bootstraps an HTTP listener immediately
// and, when SecKey is set and the resolved Mode includes DMSG,
// brings up a dmsg.Client for the dmsghttp / pprof / CXO publisher
// surfaces.
package dmsgdisc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/api"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/svcmode"
)

// Type is the registry key used in services.json blocks.
const Type = "dmsg-discovery"

// RedisPasswordEnvName is the env var the redis store reads its
// password from. Re-exported so the cobra command's help text and
// the multi-service runner's docs can reference the same constant.
const RedisPasswordEnvName = "REDIS_PASSWORD"

func init() {
	services.Register(Type, factory)
}

// factory parses a services.json block into a Config and returns a
// Service. The supervisor calls this once per dmsg-discovery block.
func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	cfg, err := ParseBlock(raw)
	if err != nil {
		return nil, err
	}
	return New(cfg, log), nil
}

// New builds a Service from an already-parsed Config. Used by the
// cobra command (which builds Config from flags) and by factory
// (which builds Config from JSON).
func New(cfg *Config, log *logging.Logger) services.Service {
	return &service{cfg: cfg, log: log}
}

// service is the runnable instance. Lowercase: callers go through
// New / factory so we can change the internal layout freely.
type service struct {
	cfg *Config
	log *logging.Logger
}

// Run is the long-lived run loop. Returns when ctx cancels or a
// fatal startup error occurs (bad config, invalid PK pairing,
// unrecoverable redis init).
//
// Lifecycle:
//  1. Resolve SecKey/PubKey, optional pprof, metrics endpoint
//  2. Open redis store and build the dmsg-discovery API
//  3. Resolve --mode (rejects pure DMSG mode for this service)
//  4. Start HTTP listener; if dmsg-capable, start dmsg client +
//     dmsghttp + debug/pprof + optional CXO publisher
//  5. Block on ctx.Done()
func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg
	log := s.log

	pk, sk := cfg.PubKey, cfg.SecKey
	if pk.Null() && !sk.Null() {
		var err error
		if pk, err = sk.PubKey(); err != nil {
			log.WithError(err).Warn("dmsg-discovery: derive public key from secret key failed; skipping dmsg surfaces")
		}
	}

	stopPProf := dmsgcmdutil.InitPProf(log, cfg.PProfMode, cfg.PProfAddr)
	defer stopPProf()

	metricsutil.ServeHTTPMetrics(log, cfg.MetricsAddr)

	db, err := openStore(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("dmsg-discovery: open store: %w", err)
	}

	var m metrics.Metrics
	if cfg.MetricsAddr == "" {
		m = metrics.NewEmpty()
	} else {
		m = metrics.NewVictoriaMetrics()
	}

	var dmsgAddr string
	if !pk.Null() {
		dmsgPort := cfg.DmsgPort
		if dmsgPort == 0 {
			dmsgPort = dmsg.DefaultDmsgHTTPPort
		}
		dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
	}
	enableMetrics := cfg.MetricsAddr != ""
	entryTimeout := cfg.EntryTimeout.Std()
	a := api.New(log, db, m, cfg.TestMode, cfg.EnableLoadTesting, enableMetrics, dmsgAddr, cfg.AuthPassphrase, entryTimeout)

	for _, k := range cfg.Whitelist {
		api.WhitelistPKs.Set(k)
	}

	a.OfficialServers = officialServersMap(cfg.OfficialServers)

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("dmsg-discovery: invalid mode %q: %w", cfg.Mode, err)
	}
	if resolvedMode == svcmode.ModeDmsg {
		return errors.New("dmsg-discovery: cannot run in mode=dmsg — dmsg-servers must reach this service over plain HTTP to register; use mode=http or mode=dual")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go a.RunBackgroundTasks(runCtx, log)

	addr := cfg.Addr
	if addr == "" {
		addr = ":9090"
	}
	log.WithField("addr", addr).Info("Serving discovery API...")
	go func() {
		if listenErr := listenAndServe(addr, a); listenErr != nil {
			log.Errorf("ListenAndServe: %v", listenErr)
			cancel()
		}
	}()

	if !pk.Null() && resolvedMode.IncludesDmsg() {
		if err := s.runDMSG(runCtx, cancel, cfg, a, pk, sk, log); err != nil {
			return err
		}
	}

	<-runCtx.Done()
	return nil
}

func (s *service) runDMSG(
	ctx context.Context,
	cancel context.CancelFunc,
	cfg *Config,
	a *api.API,
	pk cipher.PubKey,
	sk cipher.SecKey,
	log *logging.Logger,
) error {
	// Source priority for the dmsg-server transit set:
	//   1. config.dmsg_servers (explicit list)
	//   2. embedded deployment keyring (Prod, or Test when
	//      TestEnvironment is set)
	//   3. legacy API self-poll (last-resort: blocks until at least
	//      one server registers over HTTP)
	var servers []*disc.Entry
	switch {
	case len(cfg.DmsgServers) > 0:
		servers = cfg.DmsgServers
		log.WithField("count", len(servers)).WithField("source", "config").
			Info("preloading dmsg-servers from config")
	default:
		src := deployment.Prod
		srcName := "deployment.Prod"
		if cfg.TestEnvironment {
			src = deployment.Test
			srcName = "deployment.Test"
		}
		servers = src.ToDiscEntries()
		if len(servers) > 0 {
			log.WithField("count", len(servers)).WithField("source", srcName).
				Info("preloading dmsg-servers from embedded deployment keyring")
		} else {
			log.Warn("no dmsg-servers in embedded deployment keyring; falling back to API self-poll (legacy behavior; blocks until at least one server registers over HTTP)")
			servers = pollServersUntilFound(ctx, a, cfg.DmsgServerType, log)
		}
	}

	dmsgCfg := &dmsg.Config{
		MinSessions:          0,
		UpdateInterval:       dmsg.DefaultUpdateInterval,
		ConnectedServersType: cfg.DmsgServerType,
	}
	keys := cipher.PubKeys{pk}
	dClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

	dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, log, pk, sk, dClient, dmsgCfg)
	if err != nil {
		return fmt.Errorf("dmsg-discovery: start direct dmsg client: %w", err)
	}
	go func() {
		<-ctx.Done()
		closeDmsgDC()
	}()

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.DmsgServers = dmsgDC.ConnectedServersPK()
			}
		}
	}()

	go updateServers(ctx, a, dClient, dmsgDC, cfg.DmsgServerType, log)

	go func() {
		if dmsgErr := dmsghttp.ListenAndServe(ctx, sk, a, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, log); dmsgErr != nil {
			log.Errorf("dmsghttp.ListenAndServe: %v", dmsgErr)
			cancel()
		}
	}()

	wl := deployment.Prod.SurveyWhitelist
	if cfg.TestEnvironment {
		wl = deployment.Test.SurveyWhitelist
	}
	go func() {
		if debugErr := dmsghttp.ServeDebug(ctx, dmsgDC, log, wl); debugErr != nil {
			log.Errorf("dmsghttp.ServeDebug: %v", debugErr)
		}
	}()

	pub, perr := api.StartClientsByServerCXOPublisher(dmsgDC, sk, log)
	if perr != nil {
		log.WithError(perr).Error("Failed to start CXO clients-by-server publisher, continuing without it")
	} else {
		a.SetClientsByServerCXOPublisher(pub)
		// Pre-warm the publisher's tree from existing redis entries so
		// the first Root carries the live (server, client) map instead
		// of being empty until the next set/del event — without this a
		// subscriber connecting in the post-restart gap times out at
		// firstSyncTimeout (10s).
		a.WarmCXOFromStore(ctx, log)
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}

	return nil
}

func openStore(ctx context.Context, cfg *Config, log *logging.Logger) (store.Storer, error) {
	dbConf := &store.Config{
		URL:      cfg.Redis,
		Password: os.Getenv(RedisPasswordEnvName),
		Timeout:  cfg.EntryTimeout.Std(),
	}
	if dbConf.URL == "" {
		dbConf.URL = store.DefaultURL
	}
	return store.NewStore(ctx, "redis", dbConf, log)
}

func officialServersMap(list []string) map[string]bool {
	if len(list) == 0 {
		return map[string]bool{}
	}
	m := make(map[string]bool, len(list))
	for _, k := range list {
		k = strings.TrimSpace(k)
		if k != "" {
			m[k] = true
		}
	}
	return m
}

// pollServersUntilFound is the legacy bootstrap loop kept as a
// last-resort safety net for binaries built with an empty embedded
// keyring. Polls the API every minute until a non-empty server set
// shows up, or ctx cancels (returns empty in that case).
func pollServersUntilFound(ctx context.Context, a *api.API, dmsgServerType string, log logrus.FieldLogger) []*disc.Entry {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		servers, err := a.AllServers(ctx, log)
		if err != nil {
			log.WithError(err).Error("error getting dmsg-servers")
		}
		if dmsgServerType != "" {
			servers = filterServersByType(servers, dmsgServerType)
		}
		if len(servers) > 0 {
			return servers
		}
		log.Warn("no dmsg-servers found, retrying in 1 minute")
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// updateServers periodically re-resolves the dmsg-server transit set
// and re-establishes sessions to anything new. Was the original
// runtime sync mechanism before the embedded keyring + config-file
// preload landed; kept for the case where new servers register after
// startup.
func updateServers(ctx context.Context, a *api.API, dClient disc.APIClient, dmsgC *dmsg.Client, dmsgServerType string, log logrus.FieldLogger) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers, err := a.AllServers(ctx, log)
			if err != nil {
				log.WithError(err).Error("error getting dmsg-servers")
				continue
			}
			if dmsgServerType != "" {
				servers = filterServersByType(servers, dmsgServerType)
			}
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint:errcheck,gosec
				if err := dmsgC.EnsureSession(ctx, server); err != nil {
					log.WithField("remote_pk", server.Static).WithError(err).
						Warn("failed to establish session")
				}
			}
		}
	}
}

func filterServersByType(in []*disc.Entry, dmsgServerType string) []*disc.Entry {
	out := make([]*disc.Entry, 0, len(in))
	for _, s := range in {
		if s.Server != nil && s.Server.ServerType == dmsgServerType {
			out = append(out, s)
		}
	}
	return out
}

func listenAndServe(addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
	}
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	proxyListener := &proxyproto.Listener{Listener: ln}
	defer proxyListener.Close() //nolint:errcheck
	return srv.Serve(proxyListener)
}
