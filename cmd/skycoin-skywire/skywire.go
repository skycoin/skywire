// Package main cmd/skycoin-skywire/skywire.go
/*
skywire + skycoin
*/
package main

import (
	skycoin "github.com/skycoin/skycoin/cmd/skycoin-wallet/commands"

	"github.com/skycoin/skywire/cmd/skywire/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	commands.RootCmd.AddCommand(
		skycoin.RootCmd,
	)
	skycoin.RootCmd.Use = "skycoin"
	skycoin.RootCmd.Short = "skycoin daemon & cli"
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
