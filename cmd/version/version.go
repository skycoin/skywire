// Package main cmd/version/version.go c4-vis-cli
package main

import (
	"github.com/skycoin/skywire/cmd/version/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
