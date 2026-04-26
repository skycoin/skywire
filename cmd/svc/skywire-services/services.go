// Package main cmd/skywire-services/services.go
package main

import (
	"github.com/skycoin/skywire/cmd/svc/skywire-services/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
