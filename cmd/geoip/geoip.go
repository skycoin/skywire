// Package main cmd/geoip/geoip.go
package main

import (
	"github.com/skycoin/skywire/cmd/geoip/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
