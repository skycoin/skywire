// Package main cmd/address-resolver/address-resolver.go
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
