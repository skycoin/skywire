// Package main cmd/svc/skywire-services/services.go c4-net-discovery
package main

import (
	"github.com/skycoin/skywire/cmd/svc/skywire-services/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
