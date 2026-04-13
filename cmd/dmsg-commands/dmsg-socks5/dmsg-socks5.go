// Package main cmd/dmsg-socks5/dmsg-socks5.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg-commands/dmsg-socks5/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
