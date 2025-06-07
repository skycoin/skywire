// Package main cmd/version/version.go
package main

import (
	"github.com/skycoin/skywire/cmd/version/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
