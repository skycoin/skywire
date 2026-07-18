// Package main cmd/svc/transport-setup/transport-setup.go c2-net-transport
package main

import (
	"github.com/skycoin/skywire/cmd/svc/transport-setup/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
