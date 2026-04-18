package visor

import (
	"context"

	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func initDHT(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.DHT
	if conf == nil || !conf.Enable {
		return nil
	}

	if v.dmsgC == nil {
		log.Warn("DHT requires DMSG client, skipping")
		return nil
	}

	// Wait for DMSG to be ready before starting DHT.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	dhtCfg := dht.Config{
		BootstrapPKs:   conf.BootstrapPKs,
		FullNode:       conf.FullNode,
		WhitelistedPKs: conf.WhitelistedPKs,
		TrustedPKs:     conf.TrustedPKs,
	}

	tp := dht.NewDMSGTransport(v.dmsgC)
	node := dht.New(dhtCfg, v.conf.PK, v.conf.SK, tp, log)

	if err := node.Start(ctx); err != nil {
		return err
	}

	v.initLock.Lock()
	v.dhtNode = node
	v.initLock.Unlock()

	v.pushCloseStack("dht", func() error {
		return node.Stop()
	})

	log.WithField("id", node.ID().String()[:16]).
		WithField("bootstrap_peers", len(conf.BootstrapPKs)).
		WithField("full_node", conf.FullNode).
		Info("DHT node started")

	return nil
}
