// Package main cmd/uptime-tracker/uptime-tracker.go
package main

import (
	"github.com/skycoin/skywire/cmd/uptime-tracker/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
