//+build windows

package visor

import (
	"context"

	"github.com/skycoin/skycoin/src/util/logging"
)

func initDmsgpty(ctx context.Context, log *logging.Logger) error {
	log.Error("dmsgpty is not supported on windows.")
	return nil
}
