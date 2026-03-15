// Package main cmd/gojq/gojq.go
package main

import (
	"log"

	cc "github.com/ivanpirog/coloredcobra"

	clijq "github.com/skycoin/skywire/cmd/skywire-cli/commands/jq"
)

func main() {
	clijq.RootCmd.Hidden = false
	cc.Init(&cc.Config{
		RootCmd:         clijq.RootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := clijq.RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
