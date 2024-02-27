// Package commands cmd/skysocks-monitor/commands/root.go
package commands

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/skysocks-monitor/api"
)

var (
	confPath            string
	addr                string
	tag                 string
	sleepDeregistration time.Duration
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9081", "address to bind to.\033[0m")
	RootCmd.Flags().DurationVarP(&sleepDeregistration, "sleep-deregistration", "s", 10, "Sleep time for derigstration process in minutes\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "skysocks-monitor.json", "config file location.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "skysocks_monitor", "logging tag\033[0m")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   "skysocksmon",
	Short: "Skysocks monitor.",
	Long: `
	┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐───││││ │││││ │ │ │├┬┘
	└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		visorBuildInfo := buildinfo.Get()
		if _, err := visorBuildInfo.WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		mLogger := logging.NewMasterLogger()
		conf := api.InitConfig(confPath, mLogger)

		srvURLs := api.ServicesURLs{
			SD: conf.Launcher.ServiceDisc,
			UT: conf.UptimeTracker.Addr,
		}

		logger := mLogger.PackageLogger("skysocks_monitor")

		logger.WithField("addr", addr).Info("Serving discovery API...")

		smSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		smConfig := api.Config{
			PK:   conf.PK,
			SK:   conf.SK,
			Sign: smSign,
		}

		smAPI := api.New(logger, srvURLs, smConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go smAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, smAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := smAPI.Visor.Close(); err != nil {
			logger.WithError(err).Error("Visor closed with error.")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
