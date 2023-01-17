// /* cmd/skywire-visor/skywire-visor.go
/*
skywire visor
*/
package main

import (
	"fmt"

	cc "github.com/ivanpirog/coloredcobra"

	"github.com/skycoin/skywire/pkg/visor"
)

func main() {
	cc.Init(&cc.Config{
		RootCmd:         visor.RootCmd,
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

	if err := visor.RootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
