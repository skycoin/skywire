// Package commands cmd/svc/service-discovery/service-discovery.go c4-net-discovery
package main

import (
	"github.com/skycoin/skywire/cmd/svc/service-discovery/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
