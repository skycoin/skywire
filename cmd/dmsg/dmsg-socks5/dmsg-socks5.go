// Package main cmd/dmsg/dmsg-socks5/dmsg-socks5.go c1-net-dmsg
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsg-socks5/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
