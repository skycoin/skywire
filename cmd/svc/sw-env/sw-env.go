// Package main cmd/svc/sw-env/sw-env.go c4-com-env
package main

import (
	"github.com/skycoin/skywire/cmd/svc/sw-env/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
