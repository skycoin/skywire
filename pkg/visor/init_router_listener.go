package visor

import (
	"context"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// initRouterListener pre-opens the DmsgAwaitSetupPort (136) listener as
// soon as v.dmsgC is ready, before initRouter runs (which depends on the
// transport manager and so was previously held up by ~20s of transport
// init). The listener is stashed on *Visor and picked up by initRouter
// via Config.AwaitSetupListener — peers dialing 136 during the gap now
// land on a real listener instead of triggering "request has no
// associated listener" warnings.
//
// If the listener fails to open (port already reserved, etc.), this
// function returns nil and lets initRouter open its own listener as
// before. Failing visor startup over a debug-noise mitigation isn't
// worth it.
func initRouterListener(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		return nil
	}
	lis, err := v.dmsgC.Listen(skyenv.DmsgAwaitSetupPort)
	if err != nil {
		log.WithError(err).
			WithField("port", skyenv.DmsgAwaitSetupPort).
			Debug("Pre-open of await-setup listener failed; router will open it")
		return nil
	}
	v.initLock.Lock()
	v.awaitSetupListener = lis
	v.initLock.Unlock()
	return nil
}
