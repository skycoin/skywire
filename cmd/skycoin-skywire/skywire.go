// Package main cmd/skycoin-skywire/skywire.go c4-vis-cli
/*
skywire + skycoin
*/
package main

import (
	skycoin "github.com/skycoin/skywire/cmd/skycoin/commands"
	"github.com/skycoin/skywire/cmd/skywire/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
	// Use/Short/Version and the subcommand tree are set by the package itself
	// (cmd/skycoin/commands), which assembles skycoin's commands on skywire's
	// side rather than importing skycoin's own assembly. Importing skycoin's
	// would link the skycoin-lite cipher wasm into this binary too.
	commands.RootCmd.AddCommand(
		skycoin.RootCmd,
	)
}

func main() {
	commands.Execute()
}
