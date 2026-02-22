// Package main cmd/setup-node/setup-node.go
package main

import (
	"github.com/skycoin/skywire/cmd/setup-node/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
