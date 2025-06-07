// Package main skywire-cli.go
package main

import (
	"github.com/skycoin/skywire/cmd/skywire-cli/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
