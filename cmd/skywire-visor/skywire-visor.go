// Package main cmd/skywire-visor/skywire-visor.go
package main

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
	commands "github.com/skycoin/skywire/pkg/visor"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
