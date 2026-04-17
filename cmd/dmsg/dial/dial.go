// package main cmd/dial/dial.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dial/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
