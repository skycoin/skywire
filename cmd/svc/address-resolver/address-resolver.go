// Package main cmd/svc/address-resolver/address-resolver.go c4-net-discovery
package main

import (
	"github.com/skycoin/skywire/cmd/svc/address-resolver/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
