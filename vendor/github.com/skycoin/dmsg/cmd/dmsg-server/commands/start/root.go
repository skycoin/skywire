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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/internal/dmsg-server/api"
	"github.com/skycoin/dmsg/internal/servermetrics"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgserver"
)

var (
	sf      cmdutil.ServiceFlags
	limitIP int
)

func init() {
	sf.Init(RootCmd, "dmsg_srv", dmsgserver.DefaultConfigPath)
	RootCmd.Flags().IntVar(&limitIP, "limit-ip", 15, "set limitation of IPs want connect to specific dmsg-server, default value is 15")
}

// RootCmd contains commands for dmsg-server
var RootCmd = &cobra.Command{
	Use:     "start",
	Short:   "Start Dmsg Server",
	PreRunE: func(cmd *cobra.Command, args []string) error { return sf.Check() },
	Run: func(_ *cobra.Command, args []string) {
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
			LimitIP:        limitIP,
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
