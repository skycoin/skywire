// Package start cmd/dmsg-server/commands/start/root.go
package start

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"strconv"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/internal/dmsg-server/api"
	"github.com/skycoin/dmsg/internal/servermetrics"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgserver"
)

var (
	sf             cmdutil.ServiceFlags
	authPassphrase string
	pprofAddr      string
)

func init() {
	sf.Init(RootCmd, "dmsg_srv", dmsgserver.DefaultConfigPath)
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)\033[0m")
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

		if pprofAddr != "" {
			pprofMux := http.NewServeMux()

			// Register the index (which links to everything else)
			pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
			pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

			// Register profile handlers using pprof.Handler
			for _, profile := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
				pprofMux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
			}

			go func() {
				log.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
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

		var m servermetrics.Metrics
		if sf.MetricsAddr == "" {
			m = servermetrics.NewEmpty()
		} else {
			m = servermetrics.NewVictoriaMetrics()
		}

		metricsutil.ServeHTTPMetrics(log, sf.MetricsAddr)

		r := chi.NewRouter()
		r.Use(middleware.RequestID)
		r.Use(middleware.RealIP)
		r.Use(middleware.Logger)
		r.Use(middleware.Recoverer)

		api := api.New(r, log, m)

		srvConf := dmsg.ServerConfig{
			MaxSessions:    conf.MaxSessions,
			UpdateInterval: conf.UpdateInterval,
			AuthPassphrase: authPassphrase,
		}
		srv := dmsg.NewServer(conf.PubKey, conf.SecKey, disc.NewHTTP(conf.Discovery, &http.Client{}, log), &srvConf, m)
		srv.SetLogger(log)

		api.SetDmsgServer(srv)
		defer func() { log.WithError(api.Close()).Info("Closed server.") }()

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		go api.RunBackgroundTasks(ctx)
		log.WithField("addr", conf.HTTPAddress).Info("Serving server API...")
		go func() {
			if err := api.ListenAndServe(conf.LocalAddress, conf.PublicAddress, conf.HTTPAddress); err != nil {
				log.Errorf("Serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func configNotFound() (io.ReadCloser, error) {
	return nil, errors.New("no config location specified")
}
