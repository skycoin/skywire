/*
cmd/skywire/skywire.go
skywire
*/
// Package main cmd/skywire/skywire.go c4-vis-cli
package main

import (
	"github.com/skycoin/skywire/cmd/skywire/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
