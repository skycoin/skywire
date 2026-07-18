// Package main cmd/dmsg/pty-host/pty-host.go c1-net-dmsg
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/pty-host/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
