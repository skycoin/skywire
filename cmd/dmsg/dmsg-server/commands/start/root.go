// Package start cmd/dmsg-server/commands/start/root.go
//
// Cobra entry point for `skywire dmsg server start`. Parses flags
// and the JSON config file into a dmsgsrv.Config, then hands off to
// pkg/services/dmsgsrv. The actual run loop lives there so the
// multi-service supervisor (`skywire svc run`) can host the same
// service from a JSON block.
package start

import (
	"context"
	"errors"
	"io"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services/dmsgsrv"
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

		logger := sf.Logger()

		var inner dmsgserver.Config
		if err := sf.ParseConfig(os.Args, true, &inner, configNotFound); err != nil {
			logger.WithError(err).Fatal("parsing config failed, generating default one...")
		}

		logLvl, _, err := cmdutil.LevelFromString(inner.LogLevel)
		if err != nil {
			log.Printf("Failed to set log level: %v", err)
		}
		logging.SetLevel(logLvl)

		cfg := &dmsgsrv.Config{
			Config:         inner,
			AuthPassphrase: authPassphrase,
			PProfMode:      pprofMode,
			PProfAddr:      pprofAddr,
			MetricsAddr:    sf.MetricsAddr,
		}

		stopPProf := dmsgcmdutil.InitPProf(logger, cfg.PProfMode, cfg.PProfAddr)
		defer stopPProf()

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()
		if err := dmsgsrv.New(cfg, logger).Run(ctx); err != nil {
			logger.WithError(err).Fatal("dmsg-server: run failed")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}

func configNotFound() (io.ReadCloser, error) {
	return nil, errors.New("no config location specified")
}
