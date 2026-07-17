// Package main cmd/svc/conf/conf.go c4-net-discovery
package main

import (
	"github.com/skycoin/skywire/cmd/svc/conf/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
