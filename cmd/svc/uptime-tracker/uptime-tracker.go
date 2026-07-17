// Package main cmd/svc/uptime-tracker/uptime-tracker.go c4-net-discovery
package main

import (
	"github.com/skycoin/skywire/cmd/svc/uptime-tracker/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
