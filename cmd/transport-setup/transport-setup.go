// Package main cmd/transport-setup/transport-setup.go
package main

import (
	"github.com/skycoin/skywire/cmd/transport-setup/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
