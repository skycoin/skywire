// Package main cmd/svc/setup-node/setup-node.go c2-net-routing
package main

import (
	"github.com/skycoin/skywire/cmd/svc/setup-node/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
