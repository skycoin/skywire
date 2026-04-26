// Package main cmd/transport-discovery/transport-discovery.go
package main

import (
	"github.com/skycoin/skywire/cmd/svc/transport-discovery/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
