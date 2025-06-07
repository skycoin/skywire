// Package main cmd/sw-env/sw-env.go
package main

import (
	"github.com/skycoin/skywire/cmd/sw-env/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
