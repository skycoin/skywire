// Package main skywire.go
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
	flags.InitFlags(commands.RootCmd, true)
	commands.RootCmd.AddCommand(
		skycoin.RootCmd,
	)
	skycoin.RootCmd.Use = "skycoin"
	skycoin.RootCmd.Short = "skycoin daemon & cli"
}

func main() {
	commands.Execute()
}
