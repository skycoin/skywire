// Package main cmd/svc/route-finder/route-finder.go c2-net-routing
package main

import (
	"github.com/skycoin/skywire/cmd/svc/route-finder/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
