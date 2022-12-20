// Package commands cmd/skywire-visor/commands/root.go
package commands

import (
	"fmt"
	_ "net/http/pprof" // nolint:gosec // https://golang.org/doc/diagnostics.html#profiling

	cc "github.com/ivanpirog/coloredcobra"

	"github.com/skycoin/skywire/pkg/visor"
)

// Execute executes root CLI command.
func Execute() {
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
