// Package config cmd/dmsg-server/commands/cofnig/gen.go
package config

import (
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/dmsgserver"
)

var (
	output  string
	testEnv bool
)

func init() {
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "config output path/name")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment")
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		logger := mLog.PackageLogger("dmsg-server config generator")
		// generate config
		conf := new(dmsgserver.Config)

		// set default config values
		dmsgserver.GenerateDefaultConfig(conf)

		//use test deployment
		if testEnv {
			conf.Discovery = dmsgserver.DefaultDiscoverURLTest
		}

		// use output path/name
		if output != "" {
			conf.Path = output
		}

		// Save config to file.
		if err := conf.Flush(logger); err != nil {
			logger.WithError(err).Fatal("Failed to flush config to file.")
		}
	},
}
