// package main cmd/dmsg-discovery/dmsg-discovery.go
package main

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"

	"github.com/skycoin/skywire/cmd/dmsg-commands/dmsg-discovery/commands"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
