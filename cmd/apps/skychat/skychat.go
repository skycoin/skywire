// Package main cmd/apps/skychat/skychat.go
package main

import (
	cc "github.com/ivanpirog/coloredcobra"

	"github.com/skycoin/skywire/cmd/apps/skychat/commands"
)

func main() {
	cc.Init(&cc.Config{
		RootCmd:         commands.RootCmd,
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
	commands.Execute()
}
