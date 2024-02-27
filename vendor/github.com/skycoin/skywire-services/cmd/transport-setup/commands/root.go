// Package commands cmd/transport-setup/commands/root.go
package commands

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-services/pkg/transport-setup/api"
	"github.com/skycoin/skywire-services/pkg/transport-setup/config"
)

var (
	logLvl     string
	configFile string
)

func init() {
	RootCmd.Flags().StringVarP(&configFile, "config", "c", "", "path to config file\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   "tps [config.json]",
	Short: "Transport setup server for skywire",
	Long: `
	┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐  ┌─┐┌─┐┌┬┐┬ ┬┌─┐
	 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │───└─┐├┤  │ │ │├─┘
	 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴   └─┘└─┘ ┴ └─┘┴  `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		if configFile == "" {
			log.Fatal("please specify config file")
		}
		const loggerTag = "transport_setup"
		log := logging.MustGetLogger(loggerTag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			log.Fatal("Invalid loglvl detected")
		}
		logging.SetLevel(lvl)

		conf := config.MustReadConfig(configFile, log)
		api := api.New(log, conf)
		srv := &http.Server{
			Addr:              fmt.Sprintf(":%d", conf.Port),
			ReadHeaderTimeout: 2 * time.Second,
			IdleTimeout:       30 * time.Second,
			Handler:           api,
		}
		if err := srv.ListenAndServe(); err != nil {
			log.Errorf("ListenAndServe: %v", err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
