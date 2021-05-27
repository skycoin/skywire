// +build !windows

package commands

import (
	"context"

	"github.com/skycoin/dmsg/cmdutil"
	"github.com/skycoin/skycoin/src/util/logging"
)

func signalContext(ctx context.Context, log *logging.MasterLogger) (context.Context, context.CancelFunc) {
	return cmdutil.SignalContext(ctx, log)
}
