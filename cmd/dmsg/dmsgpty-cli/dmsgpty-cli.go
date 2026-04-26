// package main cmd/dmsgpty-cli/dmsgpty-cli.go
package main

import (
	"github.com/skycoin/skywire/cmd/dmsg/dmsgpty-cli/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
