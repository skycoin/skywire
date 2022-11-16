// Package clidmsgget cmd/skywire-cli/commands/dmsgget/root.go
package clidmsgget

import (
	"context"
	"flag"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/dmsgget"
)

// RootCmd is the command that contains sub-commands which interacts with dmsgget.
var RootCmd = &cobra.Command{
	Use:   "dmsgget",
	Short: "Interact with remote visors",
	Run: func(cmd *cobra.Command, _ []string) {
		log := logging.MustGetLogger(dmsgget.ExecName)

		dg := dmsgget.New(flag.CommandLine)
		flag.Parse()

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		if err := dg.Run(ctx, log, "", flag.Args()[1:]); err != nil {
			log.WithError(err).Fatal()
		}
	},
}
