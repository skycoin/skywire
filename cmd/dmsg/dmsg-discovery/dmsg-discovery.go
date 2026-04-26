// package main cmd/dmsg-discovery/dmsg-discovery.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsg-discovery/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
