// Package main cmd/edit/edit.go
package main

import (
	"log"

	cc "github.com/ivanpirog/coloredcobra"

	cliedit "github.com/skycoin/skywire/cmd/skywire-cli/commands/edit"
)

func main() {
	cliedit.RootCmd.Hidden = false
	cc.Init(&cc.Config{
		RootCmd:         cliedit.RootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := cliedit.RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
