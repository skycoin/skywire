// Package start cmd/dmsg-server/commands/start/root.go
package start

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/rpc"
	"net/url"
	"os"
	"strconv"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/spf13/cobra"

	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
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
		srv := dmsg.NewServer(conf.PubKey, conf.SecKey, disc.NewHTTP(conf.Discovery, &http.Client{}, log), &srvConf, m)
		srv.SetLogger(log)

		srvAPI.SetDmsgServer(srv)
		defer func() { log.WithError(srvAPI.Close()).Info("Closed server.") }()

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		go srvAPI.RunBackgroundTasks(ctx)
		log.WithField("addr", conf.HTTPAddress).Info("Serving server API...")
		go func() {
			if err := srvAPI.ListenAndServe(conf.LocalAddress, conf.PublicAddress, conf.HTTPAddress); err != nil {
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
			entries := direct.GetAllEntries(cipher.PubKeys{conf.PubKey}, []*disc.Entry{serverEntry})
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
