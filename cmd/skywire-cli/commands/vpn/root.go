// Package clivpn root.go
package clivpn

import (
	"github.com/spf13/cobra"
)

var (
	path         string
	isPkg        bool
	isUnFiltered bool
	ver          string
	country      string
	isStats      bool
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "vpn",
	Short: "controls for VPN client",
}
