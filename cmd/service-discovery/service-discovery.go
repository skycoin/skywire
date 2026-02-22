// Package commands cmd/service-discovery/service-discovery.go
package main

import (
	"github.com/skycoin/skywire/cmd/service-discovery/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
