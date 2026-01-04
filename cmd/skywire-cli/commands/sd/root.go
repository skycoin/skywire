// Package clisd root.go
package clisd

import (
	"github.com/spf13/cobra"
)

// RootCmd contains commands that interact with service discovery
var RootCmd = &cobra.Command{
	Use:   "sd",
	Short: "Service discovery",
	Long:  "Query and display service discovery data",
}

func init() {
	RootCmd.AddCommand(
		networkCmd,
	)
}
