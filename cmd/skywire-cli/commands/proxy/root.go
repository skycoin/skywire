// Package skysocksc root.go
package skysocksc

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

var (
	isUnFiltered bool
	ver          string
	country      string
	isStats      bool
	pubkey       cipher.PubKey
	pk           string
	count        int
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
