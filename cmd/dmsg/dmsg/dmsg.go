// Package main cmd/dmsg/dmsg.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsg/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
