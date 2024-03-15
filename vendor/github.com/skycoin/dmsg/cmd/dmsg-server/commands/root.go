// Package commands cmd/dmsg-server/commands/root.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/cmd/dmsg-server/commands/config"
	"github.com/skycoin/dmsg/cmd/dmsg-server/commands/start"
)

func init() {
	RootCmd.AddCommand(
		config.RootCmd,
		start.RootCmd,
	)

}

// RootCmd contains the root dmsg-server command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	││││││└─┐│ ┬ ─ └─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘┴└─ └┘ └─┘┴└─
DMSG Server
skywire dmsg server config gen -o dmsg-config.json
skywire dmsg server start dmsg-config.json`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
