//go:build withoutgotop

// Package cligotop cmd/skywire-cli/commands/gotop/stub.go c4-vis-cli
package cligotop

import (
	"github.com/spf13/cobra"
)

// RootCmd is a no-op stub when gotop is excluded via build tags.
var RootCmd = &cobra.Command{
	Use:    "gotop",
	Short:  "gotop (not available in this build)",
	Hidden: true,
}
