// Package start cmd/dmsg-server/commands/start/root.go
package start

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/spf13/cobra"

	dmsgcmdutil "github.com/skycoin/dmsg/pkg/cmdutil"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsg/metrics"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/dmsg/pkg/dmsgserver"
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

		// Serve pprof debug interface over dmsg using a direct client through ourselves
		go func() {
			// Wait for the dmsg server to be ready before connecting the debug client
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
				log.WithError(err).Error("failed to start debug dmsg client")
				return
			}
			defer closeDebug()

			if debugErr := dmsghttp.ServeDebug(ctx, dmsgC, log, deployment.Prod.SurveyWhitelist); debugErr != nil {
				log.Errorf("dmsghttp.ServeDebug: %v", debugErr)
			}
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
