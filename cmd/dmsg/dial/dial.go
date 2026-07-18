// Package main cmd/dmsg/dial/dial.go c1-net-dmsg
// package main cmd/dial/dial.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dial/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
