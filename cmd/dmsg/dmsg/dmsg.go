// Package main cmd/dmsg/dmsg/dmsg.go c1-net-dmsg
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
