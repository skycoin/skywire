// Package main cmd/apps/skysocks-client/skysocks-client.go
package main

import (
	cc "github.com/ivanpirog/coloredcobra"

	"github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
)

func main() {
	cc.Init(&cc.Config{
		RootCmd:       commands.RootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	commands.Execute()
}
