// Package main skywire.go
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
	// side rather than importing skycoin's own assembly.
	commands.RootCmd.AddCommand(
		skycoin.RootCmd,
	)
	// The skywire-mesh augmentation of the vendored `skycoin web` help lives
	// in skywire_skycoinweb.go (native only — the browser build stubs the
	// skycoin tree; see cmd/skycoin/commands/root_js.go).
	// Help presentation (the code-rain help screen and the --tui console) is
	// wired in commands.Execute, so this stays a thin wrapper matching
	// cmd/skycoin-skywire/skywire.go.
}

func main() {
	commands.Execute()
}
