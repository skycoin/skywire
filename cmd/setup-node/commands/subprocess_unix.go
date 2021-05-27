// +build !windows

package commands

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cmdutil"
)

func signalContext(ctx context.Context, log logrus.FieldLogger) (context.Context, context.CancelFunc) {
	return cmdutil.SignalContext(ctx, log)
}
