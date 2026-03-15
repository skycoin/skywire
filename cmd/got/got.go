// Package main cmd/got/got.go
package main

import (
	"log"

	cc "github.com/ivanpirog/coloredcobra"

	cligot "github.com/skycoin/skywire/cmd/skywire-cli/commands/got"
)

func main() {
	cc.Init(&cc.Config{
		RootCmd:         cligot.RootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := cligot.RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
