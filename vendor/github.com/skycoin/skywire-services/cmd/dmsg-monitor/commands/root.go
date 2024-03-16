// Package commands cmd/dmsg-monitor/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/dmsg-monitor/api"
)

var (
	confPath            string
	dmsgURL             string
	utURL               string
	addr                string
	tag                 string
	logLvl              string
	sleepDeregistration time.Duration
	batchSize           int
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9080", "address to bind to.\033[0m")
	RootCmd.Flags().DurationVarP(&sleepDeregistration, "sleep-deregistration", "s", 60, "Sleep time for derigstration process in minutes\033[0m")
	RootCmd.Flags().IntVarP(&batchSize, "batchsize", "b", 20, "Batch size of deregistration\033[0m")
	RootCmd.Flags().StringVarP(&confPath, "config", "c", "dmsg-monitor.json", "config file location.\033[0m")
	RootCmd.Flags().StringVarP(&dmsgURL, "dmsg-url", "d", "", "url to dmsg data.\033[0m")
	RootCmd.Flags().StringVarP(&utURL, "ut-url", "u", "", "url to uptime tracker visor data.\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "dmsg_monitor", "logging tag\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG monitor of DMSG discovery entries.",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
	 │││││└─┐│ ┬───││││ │││││ │ │ │├┬┘
	─┴┘┴ ┴└─┘└─┘   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─
`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		mLogger := logging.NewMasterLogger()
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLogger.Fatal("Invalid log level")
		}
		logging.SetLevel(lvl)

		conf := api.InitConfig(confPath, mLogger)

		if dmsgURL == "" {
			dmsgURL = conf.Dmsg.Discovery
		}
		if utURL == "" {
			utURL = conf.UptimeTracker.Addr + "/uptimes"
		}

		var srvURLs api.ServicesURLs
		srvURLs.DMSG = dmsgURL
		srvURLs.UT = utURL

		logger := mLogger.PackageLogger(tag)

		logger.WithField("addr", addr).Info("Serving DMSG-Monitor API...")

		monitorSign, _ := cipher.SignPayload([]byte(conf.PK.Hex()), conf.SK) //nolint

		var monitorConfig api.DMSGMonitorConfig
		monitorConfig.PK = conf.PK
		monitorConfig.Sign = monitorSign
		monitorConfig.BatchSize = batchSize

		dmsgMonitorAPI := api.New(logger, srvURLs, monitorConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go dmsgMonitorAPI.InitDeregistrationLoop(ctx, conf, sleepDeregistration)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, dmsgMonitorAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
		if err := dmsgMonitorAPI.Visor.Close(); err != nil {
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
