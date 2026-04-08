// Package main cmd/dmsg-socks5/dmsg-socks5.go
package main

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"

	"github.com/skycoin/skywire/cmd/dmsg-commands/dmsg-socks5/commands"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
