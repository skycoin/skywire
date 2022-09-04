package direct

import (
	"context"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

// StartDmsg starts dmsg directly without the discovery
func StartDmsg(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey,
	dClient disc.APIClient, config *dmsg.Config) (dmsgDC *dmsg.Client, stop func(), err error) {

	dmsgDC = dmsg.NewClient(pk, sk, dClient, config)
	dmsgDC.SetLogger(log)

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgDC.Serve(context.Background())
	}()

	stop = func() {
		err := dmsgDC.Close()
		log.WithError(err).Debug("Disconnected from dmsg network.")
	}

	log.WithField("public_key", pk.String()).
		Debug("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgDC.Ready():
		log.Debug("Dmsg network ready.")
		return dmsgDC, stop, nil
	}
}
