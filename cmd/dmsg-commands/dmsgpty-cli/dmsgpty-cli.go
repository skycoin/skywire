// package main cmd/dmsgpty-cli/dmsgpty-cli.go
package main

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"

	"github.com/skycoin/skywire/cmd/dmsg-commands/dmsgpty-cli/commands"
)

func init() {
	flags.InitFlags(commands.RootCmd, true)
}

func main() {
	commands.Execute()
}
