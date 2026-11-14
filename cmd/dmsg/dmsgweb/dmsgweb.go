// Package main cmd/dmsg/dmsgweb/dmsgweb.go c3-net-dmsg
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsgweb/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
