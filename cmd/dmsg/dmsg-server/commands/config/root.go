// Package config cmd/dmsg-server/commands/cofnig/root.go
package config

import (
	"github.com/spf13/cobra"
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate a dmsg-server config",
}
