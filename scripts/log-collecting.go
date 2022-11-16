package main

import (
	"context"
	"flag"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/dmsgget"
)

func main() {
	log := logging.MustGetLogger(dmsgget.ExecName)

	dg := dmsgget.New(flag.CommandLine)
	flag.Parse()

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	defer cancel()

	downloadTarget := []string{"dmsg://03e14c0cd9d823bec760a022e902f58f7a0f9ff3adc9c18867564acba2597ebccc:80/node-info.json"}

	if err := dg.Run(ctx, log, "", downloadTarget); err != nil {
		log.WithError(err).Fatal()
	}
}
