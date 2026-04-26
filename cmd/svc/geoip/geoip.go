// Package main cmd/geoip/geoip.go
package main

import (
	"github.com/skycoin/skywire/cmd/svc/geoip/commands"
	"github.com/skycoin/skywire/pkg/flags"
)

func init() {
	flags.InitFlags(commands.RootCmd, false)
}

func main() {
	commands.Execute()
}
