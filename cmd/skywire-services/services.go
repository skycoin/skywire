// Package main cmd/skywire-services/services.go
package main

import (
	"github.com/skycoin/skywire/cmd/skywire-services/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
