// Package main cmd/network-monitor/network-monitor.go
package main

import (
	"github.com/skycoin/skywire/cmd/svc/network-monitor/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
