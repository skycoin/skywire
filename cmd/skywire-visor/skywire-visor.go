// Package main cmd/skywire-visor/skywire-visor.go c4-vis-cli
package main

import (
	"github.com/skycoin/skywire/pkg/flags"
	commands "github.com/skycoin/skywire/pkg/visor"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
