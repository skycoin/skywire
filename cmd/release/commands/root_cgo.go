//go:build cgo && !386 && !(windows && arm64)

package commands

import (
	skyhw "github.com/skycoin/skycoin/cmd/hardware-wallet/commands"
	skycoin "github.com/skycoin/skycoin/cmd/skycoin-wallet/commands"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
)

func init() {
	skycoin.RootCmd.AddCommand(skyhw.RootCmd)
	skyhw.RootCmd.Use = "skyhw"
	skyhw.RootCmd.Short = "skycoin hardware wallet utilities"
	skyhw.RootCmd.Version = buildinfo.DepVersion("github.com/skycoin/skycoin")
}
