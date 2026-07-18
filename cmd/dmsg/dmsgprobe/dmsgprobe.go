// Package main cmd/dmsg/dmsgprobe/dmsgprobe.go c1-net-dmsg
// package main cmd/dmsg/dmsgprobe/dmsgprobe.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsgprobe/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
