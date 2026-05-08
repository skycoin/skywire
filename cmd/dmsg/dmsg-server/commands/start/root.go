// Package start cmd/dmsg-server/commands/start/root.go
package start

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"net/url"
	"os"
	"strconv"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/dmsgfirst"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
	"github.com/skycoin/skywire/pkg/geoip"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	sf             cmdutil.ServiceFlags
	authPassphrase string
	pprofMode      string
	pprofAddr      string
)

func init() {
	sf.Init(RootCmd, "dmsg_srv", dmsgserver.DefaultConfigPath)
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port\033[0m")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
}

// RootCmd contains commands for dmsg-server
var RootCmd = &cobra.Command{
	Use:     "start",
	Short:   "Start Dmsg Server",
	PreRunE: func(_ *cobra.Command, _ []string) error { return sf.Check() },
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		log := sf.Logger()

		var conf dmsgserver.Config
		if err := sf.ParseConfig(os.Args, true, &conf, configNotFound); err != nil {
			log.WithError(err).Fatal("parsing config failed, generating default one...")
		}

		logLvl, _, err := cmdutil.LevelFromString(conf.LogLevel)
		if err != nil {
			log.Printf("Failed to set log level: %v", err)
		}
		logging.SetLevel(logLvl)

		stopPProf := dmsgcmdutil.InitPProf(log, pprofMode, pprofAddr)
		defer stopPProf()

		if conf.MaxSessions <= 0 {
			conf.MaxSessions = dmsg.DefaultMaxSessions
		}

		if conf.HTTPAddress == "" {
			u, err := url.Parse(conf.LocalAddress)
			if err != nil {
				log.Fatal("unable to parse local address url: ", err)
			}
			hp, err := strconv.Atoi(u.Port())
			if err != nil {
				log.Fatal("unable to parse local address url: ", err)
			}
			httpPort := strconv.Itoa(hp + 1)
			conf.HTTPAddress = ":" + httpPort
		}

		var m metrics.Metrics
		if sf.MetricsAddr == "" {
			m = metrics.NewEmpty()
		} else {
			m = metrics.NewVictoriaMetrics()
		}

		metricsutil.ServeHTTPMetrics(log, sf.MetricsAddr)

		r := chi.NewRouter()
		r.Use(middleware.RequestID)
		r.Use(middleware.RealIP)
		r.Use(middleware.Logger)
		r.Use(middleware.Recoverer)

		srvAPI := dmsgserver.NewServerAPI(r, log, m)

		// Convert peer config to dmsg.PeerEntry.
		var peers []dmsg.PeerEntry
		for _, p := range conf.Peers {
			peers = append(peers, dmsg.PeerEntry{PK: p.PubKey, Addr: p.Address})
		}

		srvConf := dmsg.ServerConfig{
			MaxSessions:    conf.MaxSessions,
			UpdateInterval: conf.UpdateInterval,
			AuthPassphrase: authPassphrase,
			Peers:          peers,
		}

		// Resolve the configured deployments. NormalizedDeployments
		// folds the legacy Discovery / DiscoveryDmsg / PublicAddress
		// fields into a one-element list so existing config files
		// continue to work unchanged.
		deployments := conf.NormalizedDeployments()
		if len(deployments) == 0 {
			log.Fatal("no dmsg-discoveries configured; set 'discovery' or 'dmsg' in config")
		}

		// Primary discovery is the first in the list. NewServer takes a
		// single primary; we attach extras via srv.AddDiscovery once the
		// server is constructed. Using plain HTTP at construction time;
		// once the server's outbound dmsg.Client is up later, the
		// discovery clients are swapped for dmsgfirst-wrapped versions
		// so registration prefers DMSG when available.
		primaryHTTP := disc.NewHTTP(deployments[0].Discovery, &http.Client{}, log)
		srv := dmsg.NewServer(conf.PubKey, conf.SecKey, primaryHTTP, &srvConf, m)
		srv.SetLogger(log)

		// Wire the geo lookup hook so visors using LookupIPGeo can get
		// their public IP and its geolocation in a single round-trip
		// without HTTP-calling the geoip service. The DB is embedded
		// in pkg/geoip and shared with the standalone geoip service
		// and the visor's local fallback path. A failure to open the
		// DB (corrupt embed, etc.) leaves the hook unset; older
		// LookupIP callers still work, and LookupIPGeo callers see
		// empty geo fields and fall back to local lookup.
		if geoDB, err := geoip.OpenEmbedded(); err != nil {
			log.WithError(err).Warn("failed to open embedded geoip DB; LookupIPGeo will return empty geo fields")
		} else {
			srv.SetGeoLookup(func(ip net.IP) (country, region string, lat, lon float64) {
				res, err := geoip.Lookup(geoDB, ip.String())
				if err != nil || res == nil {
					return "", "", 0, 0
				}
				if res.Latitude != nil {
					lat = *res.Latitude
				}
				if res.Longitude != nil {
					lon = *res.Longitude
				}
				return res.CountryCode, res.RegionCode, lat, lon
			})
		}

		// Attach extras (each with its own per-deployment advertised
		// address). The primary's advertised address is passed through
		// the legacy Serve(..., publicAddr) path below.
		for i := 1; i < len(deployments); i++ {
			extra := deployments[i]
			srv.AddDiscovery(
				disc.NewHTTP(extra.Discovery, &http.Client{}, log),
				extra.AdvertisedAddress,
				dmsgserver.PKFromDmsgURL(extra.DiscoveryDmsg),
			)
		}

		srvAPI.SetDmsgServer(srv)
		defer func() { log.WithError(srvAPI.Close()).Info("Closed server.") }()

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// The Server's primary discovery uses the first deployment's
		// advertised address as the default (passed through Serve).
		// AdvertisedAddress on extras overrides per-endpoint inside
		// EntityCommon's update loop.
		primaryAdvertised := deployments[0].AdvertisedAddress
		if primaryAdvertised == "" {
			primaryAdvertised = conf.PublicAddress
		}

		go srvAPI.RunBackgroundTasks(ctx)
		log.WithField("addr", conf.HTTPAddress).Info("Serving server API...")
		go func() {
			if err := srvAPI.ListenAndServe(conf.LocalAddress, primaryAdvertised, conf.HTTPAddress); err != nil {
				log.Errorf("Serve: %v", err)
				cancel()
			}
		}()

		// Serve DMSG services using a direct client through ourselves:
		// - /health on port 80 (DMSG HTTP)
		// - /debug/pprof on port 81 (DMSG debug)
		// - Route setup-node on port 36 (DMSG RPC)
		go func() {
			<-srv.Ready()

			serverEntry := &disc.Entry{
				Version: "0.0.1",
				Static:  conf.PubKey,
				Server: &disc.Server{
					Address:           conf.PublicAddress,
					AvailableSessions: conf.MaxSessions,
				},
			}

			// Build the direct client's static view: this server's own
			// entry plus every peer dmsg-server from the embedded
			// deployment list (excluding self). Without the peer entries
			// the direct client could only resolve its own PK, so any
			// outbound dmsg dial from this process — most importantly
			// the DHT bootstrap pings and inter-server DHT replication
			// over the dmsg DHT transport — failed with
			// "entry of public key is not found", and every bootstrap
			// peer wound up cached as non-DHT until restart. Mirrors
			// the BootstrapDmsg helper that every other deployment
			// service uses (pkg/cmdutil/dmsg_bootstrap.go).
			// Build the transit set: the union of (a) per-discovery
			// servers from the config (multi-deployment case where
			// each discovery has its own disjoint server set), (b) the
			// embedded deployment keyring (single-deployment fallback),
			// minus this server itself. Each discovery's `servers` list
			// expresses which dmsg-servers reach THAT discovery; the
			// union ensures dmsgC has a session-relayable path to every
			// configured discovery.
			servers := []*disc.Entry{serverEntry}
			seenPK := map[cipher.PubKey]struct{}{conf.PubKey: {}}
			addServer := func(e *disc.Entry) {
				if e == nil {
					return
				}
				if _, ok := seenPK[e.Static]; ok {
					return
				}
				seenPK[e.Static] = struct{}{}
				servers = append(servers, e)
			}
			for _, d := range deployments {
				for _, e := range d.Servers {
					addServer(e)
				}
			}
			for i := range dmsg.Prod.DmsgServers {
				addServer(&dmsg.Prod.DmsgServers[i])
			}
			entries := direct.GetAllEntries(cipher.PubKeys{conf.PubKey}, servers)
			dClient := direct.NewClient(entries, log)

			debugConfig := &dmsg.Config{
				MinSessions: 0,
			}
			dmsgC, closeDebug, err := direct.StartDmsg(ctx, log, conf.PubKey, conf.SecKey, dClient, debugConfig)
			if err != nil {
				log.WithError(err).Error("failed to start dmsg client for server services")
				return
			}
			defer closeDebug()

			// Upgrade each configured discovery client from plain HTTP
			// to dmsgfirst — registration then tries DMSG first and
			// only falls back to HTTP if the dmsg dial fails. The PK
			// is extracted from the deployment's discovery_dmsg URL;
			// when discovery_dmsg is unset, dmsgfirst can't dial it
			// over DMSG, so we leave that entry on plain HTTP and log it.
			upgraded := make([]disc.APIClient, len(deployments))
			for i, d := range deployments {
				pk := dmsgserver.PKFromDmsgURL(d.DiscoveryDmsg)
				if pk == (cipher.PubKey{}) {
					upgraded[i] = disc.NewHTTP(d.Discovery, &http.Client{}, log)
					log.WithField("url", d.Discovery).Debug("discovery_dmsg unset; staying on plain HTTP")
					continue
				}
				upgraded[i] = dmsgfirst.New(dmsgC, pk, d.Discovery, &http.Client{}, log)
				log.WithField("url", d.Discovery).WithField("pk", pk).Info("discovery upgraded to dmsg-first registration")
			}
			srv.SetDiscoveryClients(upgraded)

			// Health endpoint on port 80
			startedAt := time.Now()
			dmsgAddr := fmt.Sprintf("%s:%d", conf.PubKey.Hex(), dmsg.DefaultDmsgHTTPPort)
			healthMux := http.NewServeMux()
			healthMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				// Collect peer server PKs for the health response.
				var peerPKs []string
				for _, p := range conf.Peers {
					peerPKs = append(peerPKs, p.PubKey.Hex())
				}

				resp := httputil.HealthCheckResponse{
					ServiceName:   "dmsg-server",
					BuildInfo:     buildinfo.Get(),
					StartedAt:     startedAt,
					DmsgAddr:      dmsgAddr,
					DmsgDiscovery: conf.Discovery,
					PeerServers:   peerPKs,
				}
				json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
			})

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, conf.SecKey, healthMux, dClient,
					dmsg.DefaultDmsgHTTPPort, dmsgC, log); err != nil {
					log.WithError(err).Error("DMSG HTTP health server stopped")
				}
			}()
			log.Infof("DMSG HTTP health endpoint available at %s", dmsgAddr)

			// Debug/pprof endpoint on port 81
			go func() {
				if debugErr := dmsghttp.ServeDebug(ctx, dmsgC, log, deployment.Prod.SurveyWhitelist); debugErr != nil {
					log.Errorf("dmsghttp.ServeDebug: %v", debugErr)
				}
			}()

			// Route setup-node on port 36 (optional, enabled via config)
			if conf.EnableRouteSetup {
				go func() {
					snLog := logging.MustGetLogger("dmsg-server:setup-node")
					snLog.Info("Starting integrated route setup-node")

					lis, lisErr := dmsgC.Listen(skyenv.DmsgSetupPort)
					if lisErr != nil {
						snLog.WithError(lisErr).Error("Failed to listen on setup port")
						return
					}
					defer lis.Close() //nolint:errcheck

					snLog.WithField("dmsg_port", skyenv.DmsgSetupPort).Info("Route setup-node listening")

					const maxConcurrent = 20
					sem := make(chan struct{}, maxConcurrent)
					setupMetrics := setupmetrics.NewEmpty()

					for {
						conn, accErr := lis.AcceptStream()
						if accErr != nil {
							if ctx.Err() != nil || errors.Is(accErr, dmsg.ErrEntityClosed) {
								snLog.Debug("Route setup-node listener stopped")
								return
							}
							snLog.WithError(accErr).Warn("Failed to accept setup stream")
							continue
						}

						reqPK := conn.RemoteAddr().(dmsg.Addr).PK
						snLog.WithField("remote_pk", reqPK).Debug("Accepted route setup request")

						gw := &router.SetupRPCGateway{
							Metrics: setupMetrics,
							Ctx:     ctx,
							Conn:    conn,
							ReqPK:   reqPK,
							Dialer:  router.WrapDmsgClient(dmsgC),
							Timeout: 2 * time.Minute,
						}

						rpcS := rpc.NewServer()
						if regErr := rpcS.Register(gw); regErr != nil {
							snLog.WithError(regErr).Error("Failed to register setup RPC")
							conn.Close() //nolint:errcheck,gosec
							continue
						}

						go func() {
							sem <- struct{}{}
							defer func() { <-sem }()
							rpcS.ServeConn(conn)
						}()
					}
				}()
			} else {
				log.Info("Route setup-node disabled (enable with enable_route_setup in config)")
			}

			<-ctx.Done()
		}()

		<-ctx.Done()
	},
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}

func configNotFound() (io.ReadCloser, error) {
	return nil, errors.New("no config location specified")
}
