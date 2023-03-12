// Package skysocksc root.go
package skysocksc

import (
	"github.com/spf13/cobra"
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
