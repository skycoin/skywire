// Package cliconfig cmd/skywire-cli/commands/config/parse.go
package cliconfig

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var parseCmd = &cobra.Command{
	Use:   "parse <skywire-config.json>",
	Short: "check for errors in parsing skywire config",
	Args:  cobra.ExactArgs(1), // Require exactly one argument
	Run: func(_ *cobra.Command, args []string) {
		logger.WithField("filepath", args[0]).Info()
		f, err := os.ReadFile(filepath.Clean(args[0]))
		if err != nil {
			logger.WithError(err).Fatal("Failed to read config file.")
		}
		r := bytes.NewReader(f)
		_, compat, err := visorconfig.Parse(logger, r, args[0], buildinfo.Get())
		if err != nil {
			logger.WithError(err).Fatal("Failed to read in config.")
		}
		if !compat {
			logger.Fatalf("config version is incompatible")
		}
		logger.Info("Config successfully parsed.")
	},
}

func init() {
	RootCmd.AddCommand(parseCmd)
}
