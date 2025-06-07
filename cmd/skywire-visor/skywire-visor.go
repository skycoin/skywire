// Package main cmd/skywire-visor/skywire-visor.go
package main

import (
	commands "github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
