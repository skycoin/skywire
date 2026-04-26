// package main cmd/dmsg-server/dmsg-server.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsg-server/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
