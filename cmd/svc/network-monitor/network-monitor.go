// Package main cmd/svc/network-monitor/network-monitor.go c2-net-monitor
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
