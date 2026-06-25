// Package direct pkg/direct/direct.go
package direct

import (
	"context"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// StartDmsg starts dmsg directly without the discovery.
func StartDmsg(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey,
	dClient disc.APIClient, config *dmsg.Config) (dmsgDC *dmsg.Client, stop func(), err error) {
	return StartDmsgWithSetup(ctx, log, pk, sk, dClient, config, nil)
}

// StartDmsgWithSetup builds the dmsg client, runs beforeServe (if non-nil) to
// FINISH configuring it — installing the final discovery client and/or the
// HTTP-over-dmsg transport — and only THEN serves and waits for Ready.
//
// Running setup BEFORE Serve is load-bearing, not cosmetic: the client's entry-
// update (registration) loop starts during Serve, so a discovery client or
// transport installed AFTER Serve misses the first registration tick and the
// client can sit unregistered for minutes / forever. This is the shared
// register-before-serve invariant — the native visor wires dmsg exactly this way
// (pkg/visor/init_dmsg.go: NewClient(disc) → set transport → Serve), and the
// seeded/edge path (dmsgclient.StartDmsgSeeded) regressed precisely by violating
// it (serve-with-bare-client, upgrade-after) until #3277. Both now funnel through
// here so they cannot drift on it again.
func StartDmsgWithSetup(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey,
	dClient disc.APIClient, config *dmsg.Config, beforeServe func(*dmsg.Client) error) (dmsgDC *dmsg.Client, stop func(), err error) {

	dmsgDC = dmsg.NewClient(pk, sk, dClient, config)
	dmsgDC.SetLogger(log)

	if beforeServe != nil {
		if err := beforeServe(dmsgDC); err != nil {
			return nil, nil, err
		}
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgDC.Serve(ctx)
	}()

	stop = func() {
		err := dmsgDC.Close()
		log.WithError(err).Debug("Disconnected from dmsg network.\n")
		wg.Wait()
	}

	log.WithField("public_key", pk.String()).Debug("Connecting to dmsg network...\n")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgDC.Ready():
		log.Debug("Dmsg network ready.")
		return dmsgDC, stop, nil
	}
}
