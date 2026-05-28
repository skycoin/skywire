// Package main cmd/dmsgpty-ui/dmsgpty-ui.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/pty-ui/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
