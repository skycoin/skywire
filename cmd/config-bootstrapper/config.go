// Package main cmd/config-bootstrapper/config.go
package main

import (
	"github.com/skycoin/skywire/cmd/config-bootstrapper/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
