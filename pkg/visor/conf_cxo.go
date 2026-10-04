// Package visor pkg/visor/conf_cxo.go c3-vis-core
//
// The visor's subscription to the conf service's services feed (published by
// pkg/deployment/conf/api/cxo_publisher.go on DmsgConfCXOPort). The feed is
// the deployment's current services config, signed by the conf service's
// key; when it changes, the new config is applied the same way the hourly
// dmsg-HTTP refresh applies it (applyDeploymentServices), so the deployment
// can change its service keys without a release.
//
// The subscription is held, not cycled: the document changes rarely, and the
// publisher's heartbeat (every 45s) keeps the one connection warm, so a
// change arrives within the publisher's interval at the cost of no new
// handshakes. The subscriber's own watchdog re-dials if that heartbeat stops.
// Until the first connect succeeds, startConfCXOSubscriber retries with
// backoff; the watchdog only takes over after one.
package visor

import (
	"context"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

const (
	confCXOConnectTimeout = 30 * time.Second
	confCXORetryMin       = 30 * time.Second
	confCXORetryMax       = 10 * time.Minute
)

// startConfCXOSubscriber subscribes to the conf service's services feed and
// applies each config it delivers until ctx ends.
func (v *Visor) startConfCXOSubscriber(ctx context.Context) {
	log := v.MasterLogger().PackageLogger("conf_cxo")

	confPK, ok := parseDmsgPeer(visorcore.ResolveServices(v.conf).ConfDmsg)
	if !ok {
		log.Debug("No dmsg conf service configured; not subscribing to its feed")
		return
	}
	if v.dmsgC == nil {
		return
	}

	sub, err := treestore.NewSubscriber(v.dmsgC, confPK, treestore.SubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgConfCXOPort,
	})
	if err != nil {
		log.WithError(err).Warn("Failed to create the conf feed subscriber")
		return
	}
	defer sub.Close() //nolint:errcheck

	// The callback runs under the subscriber's lock and must not block, so it
	// hands the newest document to the loop below; an older one still waiting
	// is replaced.
	updates := make(chan []byte, 1)
	offer := func(b []byte) {
		select {
		case <-updates:
		default:
		}
		select {
		case updates <- b:
		default:
		}
	}
	sub.OnUpdate(func(changes []treestore.UpdateEvent) {
		for _, c := range changes {
			if c.Path == deployment.ServicesCXOPath && c.Value != nil {
				offer(append([]byte(nil), c.Value...))
			}
		}
	})

	wait := confCXORetryMin
	for {
		cctx, cancel := context.WithTimeout(ctx, confCXOConnectTimeout)
		err := sub.ConnectAndWaitForRoot(cctx, confPK)
		cancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		log.WithError(err).Debug("Conf feed connect failed; retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > confCXORetryMax {
			wait = confCXORetryMax
		}
	}
	log.WithField("conf_pk", confPK).Info("Subscribed to the conf service's services feed")

	// The first Root may have filled before OnUpdate saw it as a change.
	if b, ok := sub.Get(deployment.ServicesCXOPath); ok {
		offer(append([]byte(nil), b...))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case b := <-updates:
			v.applyConfFeedDocument(b, log)
		}
	}
}

func (v *Visor) applyConfFeedDocument(b []byte, log *logging.Logger) {
	services, err := parseServicesConfig(cxoutils.Gunzip(b))
	if err != nil {
		log.WithError(err).Warn("Ignoring an unreadable services config from the conf feed")
		return
	}
	v.applyDeploymentServices(services, "conf service (cxo feed)", log)
}
