// Package main cmd/address-resolver/address-resolver.go
package main

import (
	"github.com/skycoin/skywire/cmd/address-resolver/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
