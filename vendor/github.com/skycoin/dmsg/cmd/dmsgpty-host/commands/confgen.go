// Package commands cmd/dmsgpty-host/commands/confgen.go
package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/internal/fsutil"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
)

var unsafe = false

func init() {
	confgenCmd.Flags().BoolVar(&unsafe, "unsafe", unsafe,
		"will unsafely write config if set")

	RootCmd.AddCommand(confgenCmd)
}

var confgenCmd = &cobra.Command{
	Use:   "confgen <config.json>",
	Short: "generates config file",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {

		if len(args) == 0 {
			confPath = "./config.json"
		} else {
			confPath = args[0]
		}

		conf, err := getConfig(cmd, true)
		if err != nil {
			return fmt.Errorf("failed to get config: %w", err)
		}
		if unsafe {
			return dmsgpty.WriteConfig(conf, confPath)
		}

		exists, err := fsutil.Exists(confPath)
		if err != nil {
			return fmt.Errorf("failed to check if config file exists: %w", err)
		}
		if exists {
			return fmt.Errorf("config file %s already exists", confPath)
		}

		return dmsgpty.WriteConfig(conf, confPath)
	},
}
