// Package main cmd/route-finder/route-finder.go
package main

import (
	"github.com/skycoin/skywire/cmd/route-finder/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
